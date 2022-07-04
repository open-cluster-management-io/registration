package addon

import (
	"context"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	addoninformerv1alpha1 "open-cluster-management.io/api/client/addon/informers/externalversions/addon/v1alpha1"
	addonlisterv1alpha1 "open-cluster-management.io/api/client/addon/listers/addon/v1alpha1"
)

const (
	addonInstallNamespaceAnnotationKey = "addon.open-cluster-management.io/namespace"
)

type addonNamespaceController struct {
	managedClusterName string
	kubeClient         kubernetes.Interface
	addOnLister        addonlisterv1alpha1.ManagedClusterAddOnLister
	recorder           events.Recorder
}

func NewAddonNamespaceController(
	managedClusterName string,
	kubeClient kubernetes.Interface,
	addOnInformer addoninformerv1alpha1.ManagedClusterAddOnInformer,
	recorder events.Recorder,
) factory.Controller {
	c := &addonNamespaceController{
		kubeClient:  kubeClient,
		addOnLister: addOnInformer.Lister(),
		recorder:    recorder,
	}
	return factory.New().WithInformersQueueKeyFunc(func(o runtime.Object) string {
		accessor, _ := meta.Accessor(o)
		return accessor.GetName()
	}, addOnInformer.Informer()).WithSync(c.sync).ToController("AddonNamespaceController", recorder)
}

func (c *addonNamespaceController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	// Get managedclusteraddon
	addOnName := syncCtx.QueueKey()
	addOn, err := c.addOnLister.ManagedClusterAddOns(c.managedClusterName).Get(addOnName)
	if errors.IsNotFound(err) {
		// addon is not for this managed cluster, ignore
		return nil
	}
	if err != nil {
		return err
	}

	// Get installNamespace of managedClusterAddon
	installNamespace := addOn.Spec.InstallNamespace

	// Check installNamespace exist or not
	_, _, err = resourceapply.ApplyNamespace(ctx, c.kubeClient.CoreV1(), c.recorder, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: installNamespace,
			Annotations: map[string]string{
				addonInstallNamespaceAnnotationKey: "true",
			},
		},
	})
	if err != nil {
		return err
	}

	// When addon is deleted, check if there is anyother addon is using the same installNamespace, if not, delete the namespace
	if !addOn.DeletionTimestamp.IsZero() {
		addOnList, err := c.addOnLister.ManagedClusterAddOns(c.managedClusterName).List(labels.Everything())
		if err != nil {
			return err
		}
		for _, a := range addOnList {
			if a.Spec.InstallNamespace == addOn.Spec.InstallNamespace && a.DeletionTimestamp.IsZero() {
				return nil
			}
		}
		err = c.kubeClient.CoreV1().Namespaces().Delete(ctx, installNamespace, metav1.DeleteOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}
