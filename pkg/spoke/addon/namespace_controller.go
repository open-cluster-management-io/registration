package addon

import (
	"context"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
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
	ns, err := c.kubeClient.CoreV1().Namespaces().Get(ctx, installNamespace, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		// Create installNamespace
		_, err = c.kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: installNamespace,
				Annotations: map[string]string{
					addonInstallNamespaceAnnotationKey: "true",
				},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		return nil
	} else if err != nil {
		return err
	} else {
		// Update ns if annotation not set
		if ns.Annotations == nil {
			ns.Annotations = make(map[string]string)
			ns.Annotations[addonInstallNamespaceAnnotationKey] = "true"
			_, err = c.kubeClient.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
		} else {
			if ns.Annotations[addonInstallNamespaceAnnotationKey] != "true" {
				ns.Annotations[addonInstallNamespaceAnnotationKey] = "true"
				_, err = c.kubeClient.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{})
				if err != nil {
					return err
				}
			}
		}
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
