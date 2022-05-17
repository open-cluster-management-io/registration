package managedcluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/klog/v2"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clientset "open-cluster-management.io/api/client/cluster/clientset/versioned"
	informerv1 "open-cluster-management.io/api/client/cluster/informers/externalversions/cluster/v1"
	listerv1 "open-cluster-management.io/api/client/cluster/listers/cluster/v1"
	v1 "open-cluster-management.io/api/cluster/v1"
	workapiv1 "open-cluster-management.io/api/work/v1"
	"open-cluster-management.io/registration/pkg/helpers"
)

const (
	estimate                   = 3 * time.Second
	ConditionTypeDeleteSuccess = "ContentDeleteSuccess"
	ResourceRemainReason       = "ResourceRemaining"
	FinalizerRemainReason      = "FinalizerRemaining"

	// DeletionByOtherLabelKey is the key on resource, the resource will not be delete by registration
	// with this key
	DeletionByOtherLabelKey = "cluster.open-cluster-management.io/delete-by-other"
)

// managedClusterController reconciles instances of ManagedCluster on the hub.
type managedClusterDeletionController struct {
	kubeClient     kubernetes.Interface
	clusterClient  clientset.Interface
	metadataClient metadata.Interface
	clusterLister  listerv1.ManagedClusterLister
	eventRecorder  events.Recorder

	preDeleteMonitorResources []schema.GroupVersionResource
}

// NewManagedClusterController creates a new managed cluster controller
func NewManagedClusterDeletionController(
	kubeClient kubernetes.Interface,
	metadataClient metadata.Interface,
	clusterClient clientset.Interface,
	clusterInformer informerv1.ManagedClusterInformer,
	preDeleteMonitorResources []schema.GroupVersionResource,
	recorder events.Recorder) factory.Controller {
	c := &managedClusterDeletionController{
		kubeClient:                kubeClient,
		metadataClient:            metadataClient,
		clusterClient:             clusterClient,
		clusterLister:             clusterInformer.Lister(),
		preDeleteMonitorResources: preDeleteMonitorResources,
		eventRecorder:             recorder.WithComponentSuffix("managed-cluster-deletion-controller"),
	}
	return factory.New().
		WithInformersQueueKeyFunc(func(obj runtime.Object) string {
			accessor, _ := meta.Accessor(obj)
			return accessor.GetName()
		}, clusterInformer.Informer()).
		WithSync(c.sync).
		ToController("ManagedClusterDeletionController", recorder)
}

func (c *managedClusterDeletionController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	managedClusterName := syncCtx.QueueKey()
	klog.V(4).Infof("Reconciling ManagedCluster %s", managedClusterName)
	managedCluster, err := c.clusterLister.Get(managedClusterName)
	if errors.IsNotFound(err) {
		// Spoke cluster not found, could have been deleted, do nothing.
		return nil
	}
	if err != nil {
		return err
	}

	managedCluster = managedCluster.DeepCopy()
	if managedCluster.DeletionTimestamp.IsZero() {
		hasFinalizer := false
		for i := range managedCluster.Finalizers {
			if managedCluster.Finalizers[i] == managedClusterFinalizer {
				hasFinalizer = true
				break
			}
		}
		if !hasFinalizer {
			return c.patchFinalizer(ctx, managedCluster.Name, append(managedCluster.Finalizers, managedClusterFinalizer))
		}
	}

	// Only process when managedCluster is deleting.
	if managedCluster.DeletionTimestamp.IsZero() {
		return nil
	}

	remaining, err := c.cleanup(ctx, managedClusterName)
	if err != nil {
		return err
	}

	if len(remaining.numRemainingFinalizers) > 0 {
		remainingByFinalizer := []string{}
		for finalizer, numRemaining := range remaining.numRemainingFinalizers {
			if numRemaining == 0 {
				continue
			}
			remainingByFinalizer = append(remainingByFinalizer, fmt.Sprintf("%s in %d resource instances", finalizer, numRemaining))
		}
		// sort for stable updates
		sort.Strings(remainingByFinalizer)

		_, _, updatedErr := helpers.UpdateManagedClusterStatus(
			ctx,
			c.clusterClient,
			managedClusterName,
			helpers.UpdateManagedClusterConditionFn(metav1.Condition{
				Type:    ConditionTypeDeleteSuccess,
				Status:  metav1.ConditionFalse,
				Reason:  FinalizerRemainReason,
				Message: fmt.Sprintf("resource %s for cluster %s has finalizers remaining: %s", remaining.resource, managedCluster.Name, strings.Join(remainingByFinalizer, ", ")),
			}),
		)

		if updatedErr != nil {
			return updatedErr
		}

		syncCtx.Queue().AddAfter(managedCluster.Name, time.Duration(estimate))
		return nil
	}

	if remaining.numRemainingResource > 0 {
		_, _, updatedErr := helpers.UpdateManagedClusterStatus(
			ctx,
			c.clusterClient,
			managedClusterName,
			helpers.UpdateManagedClusterConditionFn(metav1.Condition{
				Type:    ConditionTypeDeleteSuccess,
				Status:  metav1.ConditionFalse,
				Reason:  ResourceRemainReason,
				Message: fmt.Sprintf("resource %s for cluster %s has %d resource remaining", remaining.resource.String(), managedCluster.Name, remaining.numRemainingResource),
			}),
		)

		if updatedErr != nil {
			return updatedErr
		}

		syncCtx.Queue().AddAfter(managedCluster.Name, time.Duration(estimate))
		return nil
	}

	return c.removeManagedClusterFinalizer(ctx, managedCluster)
}

type totalRemainingResource struct {
	resource               schema.GroupVersionResource
	numRemainingResource   int
	numRemainingFinalizers map[string]int
}

func (c *managedClusterDeletionController) cleanup(ctx context.Context, managedClusterName string) (totalRemainingResource, error) {
	// monitor predefined resource at first, do not delete anything until all resource defined here has been cleaned by other controller.
	for _, gvr := range c.preDeleteMonitorResources {
		remaining, err := c.monitorGVR(ctx, managedClusterName, gvr, metav1.ListOptions{})
		if err != nil || remaining.numRemainingResource > 0 {
			return remaining, err
		}
	}

	// delete all managedcluster addons.
	remainingAddon, err := c.cleanupGVR(ctx, managedClusterName, addonv1alpha1.GroupVersion.WithResource("managedclusteraddons"), metav1.ListOptions{})
	if err != nil || remainingAddon.numRemainingResource > 0 {
		return remainingAddon, err
	}

	// delete all manifestworks
	remainingWorks, err := c.cleanupGVR(ctx, managedClusterName, workapiv1.GroupVersion.WithResource("manifestworks"), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("!%s", DeletionByOtherLabelKey),
	})

	if err != nil || remainingWorks.numRemainingResource > 0 {
		return remainingAddon, err
	}

	// monitor all manifestworks again, this is to ensure all works have been deleted.
	remainingWorks, err = c.monitorGVR(ctx, managedClusterName, workapiv1.GroupVersion.WithResource("manifestworks"), metav1.ListOptions{})
	if err != nil || remainingWorks.numRemainingResource > 0 {
		return remainingAddon, err
	}

	// for namespace deletion, we only delete ns with certain name and no deleteByOther label.
	remainingNS, err := c.cleanupGVR(ctx, metav1.NamespaceAll, corev1.SchemeGroupVersion.WithResource("namespaces"), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("!%s", DeletionByOtherLabelKey),
		FieldSelector: fmt.Sprintf("metadata.name=%s", managedClusterName),
	})
	if err != nil || remainingNS.numRemainingResource > 0 {
		return remainingNS, err
	}

	return totalRemainingResource{numRemainingResource: 0}, removeManagedClusterResources(ctx, c.kubeClient, c.eventRecorder, managedClusterName)
}

func (c *managedClusterDeletionController) monitorGVR(
	ctx context.Context, namespace string, gvr schema.GroupVersionResource, listOpts metav1.ListOptions) (totalRemainingResource, error) {
	partialList, err := c.metadataClient.Resource(gvr).Namespace(namespace).List(ctx, listOpts)
	if err != nil {
		return totalRemainingResource{}, err
	}

	if len(partialList.Items) == 0 {
		return totalRemainingResource{numRemainingResource: 0}, nil
	}

	finalizersToNumRemaining := map[string]int{}
	for _, item := range partialList.Items {
		for _, finalizer := range item.GetFinalizers() {
			finalizersToNumRemaining[finalizer] = finalizersToNumRemaining[finalizer] + 1
		}
	}

	klog.V(4).Infof("cluster deletion controller - deleteCR - cluster: %s, gvr: %v, finalizers: %v", namespace, gvr, finalizersToNumRemaining)
	return totalRemainingResource{
		resource:               gvr,
		numRemainingResource:   len(partialList.Items),
		numRemainingFinalizers: finalizersToNumRemaining,
	}, nil

}

func (c *managedClusterDeletionController) cleanupGVR(
	ctx context.Context, managedClusterName string, gvr schema.GroupVersionResource, listOpts metav1.ListOptions) (totalRemainingResource, error) {
	remaining, err := c.monitorGVR(ctx, managedClusterName, gvr, listOpts)
	if err != nil {
		return remaining, err
	}

	if remaining.numRemainingResource == 0 {
		return remaining, nil
	}

	foreground := metav1.DeletePropagationForeground
	opts := metav1.DeleteOptions{PropagationPolicy: &foreground}

	err = c.metadataClient.Resource(gvr).Namespace(managedClusterName).DeleteCollection(ctx, opts, listOpts)
	if err != nil {
		return remaining, err
	}

	return remaining, nil
}

func (c *managedClusterDeletionController) patchFinalizer(ctx context.Context, name string, finalizers []string) error {
	finalizerData := &metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Finalizers: finalizers,
		},
	}

	patch, err := json.Marshal(finalizerData)
	if err != nil {
		return err
	}

	// remove finalizers field if there is no remaining finalizers,
	if len(finalizers) == 0 {
		patch = []byte("{\"metadata\": {\"finalizers\": []}}")
	}

	_, err = c.clusterClient.ClusterV1().ManagedClusters().Patch(
		ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

func (c *managedClusterDeletionController) removeManagedClusterFinalizer(ctx context.Context, managedCluster *v1.ManagedCluster) error {
	copiedFinalizers := []string{}
	for i := range managedCluster.Finalizers {
		if managedCluster.Finalizers[i] == managedClusterFinalizer {
			continue
		}
		copiedFinalizers = append(copiedFinalizers, managedCluster.Finalizers[i])
	}

	if len(managedCluster.Finalizers) != len(copiedFinalizers) {
		return c.patchFinalizer(ctx, managedCluster.Name, copiedFinalizers)
	}

	return nil
}
