package addon

import (
	"context"
	"time"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	errorhelpers "github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	addoninformerv1alpha1 "open-cluster-management.io/api/client/addon/informers/externalversions/addon/v1alpha1"
	addonlisterv1alpha1 "open-cluster-management.io/api/client/addon/listers/addon/v1alpha1"
)

type getKnownAddOnNamesFunc func() []string

// addOnRegistrationController monitors ManagedClusterAddOns on hub and starts addOn registration
// according to the registrationConfigs read from annotations of ManagedClusterAddOns. Echo addOn
// may have multiple registrationConfigs. A clientcert.NewClientCertificateController will be started
// for each of them.
type addOnManagementController struct {
	clusterName             string
	hubAddOnLister          addonlisterv1alpha1.ManagedClusterAddOnLister
	recorder                events.Recorder
	getKnownAddOnNames      getKnownAddOnNamesFunc
	addOnControllerManagers []AddOnControllerManager
}

// NewAddOnRegistrationController returns an instance of addOnRegistrationController
func NewAddOnManagementController(
	clusterName string,
	hubAddOnInformers addoninformerv1alpha1.ManagedClusterAddOnInformer,
	recorder events.Recorder,
	getKnownAddOnNames getKnownAddOnNamesFunc,
	addOnControllerManagers ...AddOnControllerManager,
) factory.Controller {
	c := &addOnManagementController{
		clusterName:             clusterName,
		hubAddOnLister:          hubAddOnInformers.Lister(),
		recorder:                recorder,
		getKnownAddOnNames:      getKnownAddOnNames,
		addOnControllerManagers: addOnControllerManagers,
	}

	return factory.New().
		WithInformersQueueKeyFunc(
			func(obj runtime.Object) string {
				accessor, _ := meta.Accessor(obj)
				return accessor.GetName()
			},
			hubAddOnInformers.Informer()).
		WithSync(c.sync).
		ResyncEvery(10*time.Minute).
		ToController("AddOnRegistrationController", recorder)
}

func (c *addOnManagementController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	queueKey := syncCtx.QueueKey()
	if queueKey != factory.DefaultQueueKey {
		// sync a particular addOn
		return c.syncAddOn(ctx, syncCtx, queueKey)
	}

	// handle resync
	for _, addOnName := range c.getKnownAddOnNames() {
		syncCtx.Queue().Add(addOnName)
	}

	return nil
}

func (c *addOnManagementController) syncAddOn(ctx context.Context, syncCtx factory.SyncContext, addOnName string) error {
	klog.V(4).Infof("Reconciling addOn %q", addOnName)

	addOn, err := c.hubAddOnLister.ManagedClusterAddOns(c.clusterName).Get(addOnName)
	if errors.IsNotFound(err) {
		// addon is deleted
		return c.stopAddOnControllers(ctx, addOnName)
	}
	if err != nil {
		return err
	}

	errs := []error{}
	for _, addOnControllerManager := range c.addOnControllerManagers {
		if err := addOnControllerManager.RunControllers(ctx, addOn); err != nil {
			errs = append(errs, err)
		}
	}

	return errorhelpers.NewMultiLineAggregate(errs)
}

func (c *addOnManagementController) stopAddOnControllers(ctx context.Context, addOnName string) error {
	errs := []error{}
	for _, addOnControllerManager := range c.addOnControllerManagers {
		if err := addOnControllerManager.StopControllers(ctx, addOnName); err != nil {
			errs = append(errs, err)
		}
	}

	return errorhelpers.NewMultiLineAggregate(errs)
}
