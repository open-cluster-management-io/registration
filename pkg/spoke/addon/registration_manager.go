package addon

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	operatorhelpers "github.com/openshift/library-go/pkg/operator/v1helpers"
	certificatesv1 "k8s.io/api/certificates/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	certificatesinformers "k8s.io/client-go/informers/certificates"
	"k8s.io/client-go/kubernetes"
	addonclient "open-cluster-management.io/api/client/addon/clientset/versioned"
	addonlisterv1alpha1 "open-cluster-management.io/api/client/addon/listers/addon/v1alpha1"
	"open-cluster-management.io/registration/pkg/clientcert"
	"open-cluster-management.io/registration/pkg/helpers"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addoninformerv1alpha1 "open-cluster-management.io/api/client/addon/informers/externalversions/addon/v1alpha1"
)

type AddOnRegistrationControllerManager interface {
	AddOnControllerManager
	GetKnownAddOnNames() []string
}

// addOnRegistrationController monitors ManagedClusterAddOns on hub and starts addOn registration
// according to the registrationConfigs read from annotations of ManagedClusterAddOns. Echo addOn
// may have multiple registrationConfigs. A clientcert.NewClientCertificateController will be started
// for each of them.
type addOnRegistrationManager struct {
	clusterName          string
	agentName            string
	kubeconfigData       []byte
	managementKubeClient kubernetes.Interface // in-cluster local management kubeClient
	spokeKubeClient      kubernetes.Interface
	hubAddOnLister       addonlisterv1alpha1.ManagedClusterAddOnLister
	hubCSRInformer       certificatesinformers.Interface
	hubKubeClient        kubernetes.Interface
	hubAddOnClient       addonclient.Interface
	recorder             events.Recorder

	startRegistrationFunc func(ctx context.Context, config registrationConfig) context.CancelFunc

	// registrationConfigs maps the addon name to a map of registrationConfigs whose key is the hash of
	// the registrationConfig
	addOnRegistrationConfigs map[string]map[string]registrationConfig
}

// NewAddOnRegistrationController returns an instance of addOnRegistrationController
func NewAddOnRegistrationControllerManager(
	clusterName string,
	agentName string,
	kubeconfigData []byte,
	managementKubeClient kubernetes.Interface,
	managedKubeClient kubernetes.Interface,
	hubAddOnClient addonclient.Interface,
	hubCSRInformer certificatesinformers.Interface,
	hubAddOnInformers addoninformerv1alpha1.ManagedClusterAddOnInformer,
	hubCSRClient kubernetes.Interface,
	recorder events.Recorder,
) AddOnRegistrationControllerManager {
	manager := &addOnRegistrationManager{
		clusterName:              clusterName,
		agentName:                agentName,
		kubeconfigData:           kubeconfigData,
		managementKubeClient:     managementKubeClient,
		spokeKubeClient:          managedKubeClient,
		hubAddOnClient:           hubAddOnClient,
		hubAddOnLister:           hubAddOnInformers.Lister(),
		hubCSRInformer:           hubCSRInformer,
		hubKubeClient:            hubCSRClient,
		recorder:                 recorder,
		addOnRegistrationConfigs: map[string]map[string]registrationConfig{},
	}

	manager.startRegistrationFunc = manager.startRegistration

	return manager
}

// RunControllers runs a client certificate controller for each registratin config item of the add-on. The controller will
// be restarted once the coressponding registratin config item changes.
func (c *addOnRegistrationManager) RunControllers(ctx context.Context, addOn *addonv1alpha1.ManagedClusterAddOn) error {
	cachedConfigs := c.addOnRegistrationConfigs[addOn.Name]
	configs, err := getRegistrationConfigs(addOn)
	if err != nil {
		return err
	}

	// stop registration for the stale registration configs
	errs := []error{}
	for hash, cachedConfig := range cachedConfigs {
		if _, ok := configs[hash]; ok {
			continue
		}

		if err := c.stopRegistration(ctx, cachedConfig); err != nil {
			errs = append(errs, err)
		}
	}
	if err := operatorhelpers.NewMultiLineAggregate(errs); err != nil {
		return err
	}

	syncedConfigs := map[string]registrationConfig{}
	for hash, config := range configs {
		// keep the unchanged configs
		if cachedConfig, ok := cachedConfigs[hash]; ok {
			syncedConfigs[hash] = cachedConfig
			continue
		}

		// start registration for the new added configs
		config.stopFunc = c.startRegistrationFunc(ctx, config)
		syncedConfigs[hash] = config
	}

	if len(syncedConfigs) == 0 {
		delete(c.addOnRegistrationConfigs, addOn.Name)
		return nil
	}
	c.addOnRegistrationConfigs[addOn.Name] = syncedConfigs
	return nil
}

// StopControllers cleans both the registration configs and client certificate controllers for the addon
func (c *addOnRegistrationManager) StopControllers(ctx context.Context, addOnName string) error {
	errs := []error{}
	for _, config := range c.addOnRegistrationConfigs[addOnName] {
		if err := c.stopRegistration(ctx, config); err != nil {
			errs = append(errs, err)
		}
	}

	if err := operatorhelpers.NewMultiLineAggregate(errs); err != nil {
		return err
	}

	delete(c.addOnRegistrationConfigs, addOnName)
	return nil
}

func (c *addOnRegistrationManager) GetKnownAddOnNames() []string {
	addOnNames := []string{}
	for addOnName := range c.addOnRegistrationConfigs {
		addOnNames = append(addOnNames, addOnName)
	}

	return addOnNames
}

// startRegistration starts a client certificate controller with the given config
func (c *addOnRegistrationManager) startRegistration(ctx context.Context, config registrationConfig) context.CancelFunc {
	ctx, stopFunc := context.WithCancel(ctx)

	// the kubeClient here will be used to generate the hub kubeconfig secret for addon agents, it generates the secret
	// on the managed cluster by default, but if the addon agent is not running on the managed cluster(in Hosted mode
	// the addon agent runs outside the managed cluster, for more details see the hosted mode design docs for addon:
	// https://github.com/open-cluster-management-io/enhancements/pull/65), it generate the secret on the
	// management(hosting) cluster
	var kubeClient kubernetes.Interface = c.spokeKubeClient
	if config.addOnAgentRunningOutsideManagedCluster {
		kubeClient = c.managementKubeClient
	}

	kubeInformerFactory := informers.NewSharedInformerFactoryWithOptions(
		kubeClient, 10*time.Minute, informers.WithNamespace(config.installationNamespace))

	additonalSecretData := map[string][]byte{}
	if config.registration.SignerName == certificatesv1.KubeAPIServerClientSignerName {
		additonalSecretData[clientcert.KubeconfigFile] = c.kubeconfigData
	}

	// build and start a client cert controller
	clientCertOption := clientcert.ClientCertOption{
		SecretNamespace:               config.installationNamespace,
		SecretName:                    config.secretName,
		AdditionalSecretData:          additonalSecretData,
		AdditionalSecretDataSensitive: true,
	}

	csrOption := clientcert.CSROption{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("addon-%s-%s-", c.clusterName, config.addOnName),
			Labels: map[string]string{
				// the labels are only hints. Anyone could set/modify them.
				clientcert.ClusterNameLabel: c.clusterName,
				clientcert.AddonNameLabel:   config.addOnName,
			},
		},
		Subject:         config.x509Subject(c.clusterName, c.agentName),
		DNSNames:        []string{fmt.Sprintf("%s.addon.open-cluster-management.io", config.addOnName)},
		SignerName:      config.registration.SignerName,
		EventFilterFunc: createCSREventFilterFunc(c.clusterName, config.addOnName, config.registration.SignerName),
	}

	controllerName := fmt.Sprintf("ClientCertController@addon:%s:signer:%s", config.addOnName, config.registration.SignerName)

	statusUpdater := c.generateStatusUpdate(c.clusterName, config.addOnName)

	clientCertController, err := clientcert.NewClientCertificateController(
		clientCertOption,
		csrOption,
		c.hubCSRInformer,
		c.hubKubeClient,
		kubeInformerFactory.Core().V1().Secrets(),
		kubeClient,
		statusUpdater,
		c.recorder,
		controllerName,
	)
	if err != nil {
		utilruntime.HandleError(err)
	}

	go kubeInformerFactory.Start(ctx.Done())
	go clientCertController.Run(ctx, 1)

	return stopFunc
}

func (c *addOnRegistrationManager) generateStatusUpdate(clusterName, addonName string) clientcert.StatusUpdateFunc {
	return func(ctx context.Context, cond metav1.Condition) error {
		_, _, updatedErr := helpers.UpdateManagedClusterAddOnStatus(
			ctx, c.hubAddOnClient, clusterName, addonName, helpers.UpdateManagedClusterAddOnStatusFn(cond),
		)

		return updatedErr
	}
}

// stopRegistration stops the client certificate controller for the given config
func (c *addOnRegistrationManager) stopRegistration(ctx context.Context, config registrationConfig) error {
	if config.stopFunc != nil {
		config.stopFunc()
	}

	var kubeClient kubernetes.Interface = c.spokeKubeClient
	if config.addOnAgentRunningOutsideManagedCluster {
		// delete the secret generated on the management cluster
		kubeClient = c.managementKubeClient
	}

	err := kubeClient.CoreV1().Secrets(config.installationNamespace).
		Delete(ctx, config.secretName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	return nil
}

func createCSREventFilterFunc(clusterName, addOnName, signerName string) factory.EventFilterFunc {
	return func(obj interface{}) bool {
		accessor, err := meta.Accessor(obj)
		if err != nil {
			return false
		}
		labels := accessor.GetLabels()
		// only enqueue csr from a specific managed cluster
		if labels[clientcert.ClusterNameLabel] != clusterName {
			return false
		}
		// only enqueue csr created for a specific addon
		if labels[clientcert.AddonNameLabel] != addOnName {
			return false
		}

		// only enqueue csr with a specific signer name
		csr, ok := obj.(*certificatesv1.CertificateSigningRequest)
		if !ok {
			return false
		}
		if len(csr.Spec.SignerName) == 0 {
			return false
		}
		if csr.Spec.SignerName != signerName {
			return false
		}
		return true
	}
}
