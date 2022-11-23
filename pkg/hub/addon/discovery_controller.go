package addon

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addoninformerv1alpha1 "open-cluster-management.io/api/client/addon/informers/externalversions/addon/v1alpha1"
	addonlisterv1alpha1 "open-cluster-management.io/api/client/addon/listers/addon/v1alpha1"
	clientset "open-cluster-management.io/api/client/cluster/clientset/versioned"
	clusterv1informer "open-cluster-management.io/api/client/cluster/informers/externalversions/cluster/v1"
	clusterv1listers "open-cluster-management.io/api/client/cluster/listers/cluster/v1"
)

const (
	addOnFeaturePrefix     = "feature.open-cluster-management.io/addon-"
	addOnStatusAvailable   = "available"
	addOnStatusUnhealthy   = "unhealthy"
	addOnStatusUnreachable = "unreachable"
)

// addOnFeatureDiscoveryController monitors ManagedCluster and its ManagedClusterAddOns on hub and
// create/update/delete labels of the ManagedCluster to reflect the status of addons.
type addOnFeatureDiscoveryController struct {
	clusterClient clientset.Interface
	clusterLister clusterv1listers.ManagedClusterLister
	addOnLister   addonlisterv1alpha1.ManagedClusterAddOnLister
	recorder      events.Recorder
}

// NewAddOnFeatureDiscoveryController returns an instance of addOnFeatureDiscoveryController
func NewAddOnFeatureDiscoveryController(
	clusterClient clientset.Interface,
	clusterInformer clusterv1informer.ManagedClusterInformer,
	addOnInformers addoninformerv1alpha1.ManagedClusterAddOnInformer,
	recorder events.Recorder,
) factory.Controller {
	c := &addOnFeatureDiscoveryController{
		clusterClient: clusterClient,
		clusterLister: clusterInformer.Lister(),
		addOnLister:   addOnInformers.Lister(),
		recorder:      recorder,
	}

	return factory.New().
		WithInformersQueueKeyFunc(
			func(obj runtime.Object) string {
				accessor, _ := meta.Accessor(obj)
				return accessor.GetName()
			},
			clusterInformer.Informer()).
		WithInformersQueueKeyFunc(
			func(obj runtime.Object) string {
				key, _ := cache.MetaNamespaceKeyFunc(obj)
				return key
			},
			addOnInformers.Informer()).
		WithSync(c.sync).
		ResyncEvery(10*time.Minute).
		ToController("AddOnFeatureDiscoveryController", recorder)
}

func (c *addOnFeatureDiscoveryController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	// The value of queueKey might be
	// 1) equal to the default queuekey. It is triggered by resync every 10 minutes;
	// 2) in format: namespace/name. It indicates the event source is a ManagedClusterAddOn;
	// 3) in format: name. It indicates the event source is a ManagedCluster;
	queueKey := syncCtx.QueueKey()
	namespace, name, err := cache.SplitMetaNamespaceKey(queueKey)
	if err != nil {
		utilruntime.HandleError(err)
		return nil
	}

	switch {
	case queueKey == factory.DefaultQueueKey:
		// handle resync
		clusters, err := c.clusterLister.List(labels.Everything())
		if err != nil {
			return err
		}

		for _, cluster := range clusters {
			syncCtx.Queue().Add(cluster.Name)
		}
		return nil
	case len(namespace) > 0:
		// sync a particular addon
		return c.syncAddOn(ctx, namespace, name)
	default:
		// sync the cluster
		return c.syncCluster(ctx, name)
	}
}

func (c *addOnFeatureDiscoveryController) syncAddOn(ctx context.Context, clusterName, addOnName string) error {
	klog.V(4).Infof("Reconciling addOn %q", addOnName)

	cluster, err := c.clusterLister.Get(clusterName)
	if errors.IsNotFound(err) {
		// no cluster, it could be deleted
		return nil
	}
	if err != nil {
		return fmt.Errorf("unable to find cluster with name %q: %w", clusterName, err)
	}
	// no work if cluster is deleting
	if !cluster.DeletionTimestamp.IsZero() {
		return nil
	}

	labels := cluster.Labels
	if len(labels) == 0 {
		labels = map[string]string{}
	}

	// make a copy to check if the cluster labels are changed
	labelsCopy := map[string]string{}
	for key, value := range labels {
		labelsCopy[key] = value
	}

	addOn, err := c.addOnLister.ManagedClusterAddOns(clusterName).Get(addOnName)
	key := fmt.Sprintf("%s%s", addOnFeaturePrefix, addOnName)
	switch {
	case errors.IsNotFound(err):
		// addon is deleted
		delete(labels, key)
	case err != nil:
		return err
	case !addOn.DeletionTimestamp.IsZero():
		delete(labels, key)
	default:
		labels[key] = getAddOnLabelValue(addOn)
	}

	// no work if the labels are not changed
	if reflect.DeepEqual(labelsCopy, labels) {
		return nil
	}

	// if labels is empty, put it to nil, otherwise patch operation will not take effect
	if len(labels) == 0 {
		labels = nil
	}
	// build cluster labels patch
	patchBytes, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels":          labels,
			"uid":             cluster.UID,
			"resourceVersion": cluster.ResourceVersion,
		}, // to ensure they appear in the patch as preconditions
	})
	if err != nil {
		return fmt.Errorf("failed to create patch for cluster %s: %w", cluster.Name, err)
	}

	// patch the cluster labels
	_, err = c.clusterClient.ClusterV1().ManagedClusters().Patch(ctx, cluster.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{})

	return err
}

func (c *addOnFeatureDiscoveryController) syncCluster(ctx context.Context, clusterName string) error {
	// sync all addon labels on the managed cluster
	cluster, err := c.clusterLister.Get(clusterName)
	if errors.IsNotFound(err) {
		// cluster is deleted
		return nil
	}
	if err != nil {
		return err
	}

	// Do not update addon label if cluster is deleting
	if !cluster.DeletionTimestamp.IsZero() {
		return nil
	}

	// build labels for existing addons
	addOnLabels := cluster.Labels
	if len(addOnLabels) == 0 {
		addOnLabels = map[string]string{}
	}

	// make a copy to check if the cluster labels are changed
	addOnLabelsCopy := map[string]string{}
	for key, value := range addOnLabels {
		addOnLabelsCopy[key] = value
	}

	newAddonLabels := map[string]string{}
	addOns, err := c.addOnLister.ManagedClusterAddOns(clusterName).List(labels.Everything())
	if err != nil {
		return fmt.Errorf("unable to list addOns of cluster %q: %w", clusterName, err)
	}

	for _, addOn := range addOns {
		key := fmt.Sprintf("%s%s", addOnFeaturePrefix, addOn.Name)

		// addon is deleting
		if !addOn.DeletionTimestamp.IsZero() {
			delete(addOnLabels, key)
			continue
		}

		addOnLabels[key] = getAddOnLabelValue(addOn)
		newAddonLabels[key] = getAddOnLabelValue(addOn)
	}

	// remove addon lable if its corresponding addon no longer exists
	for key := range addOnLabels {
		if !strings.HasPrefix(key, addOnFeaturePrefix) {
			continue
		}

		if _, ok := newAddonLabels[key]; !ok {
			delete(addOnLabels, key)
		}
	}

	// no work if the labels are not changed
	if reflect.DeepEqual(addOnLabelsCopy, addOnLabels) {
		return nil
	}

	// for empty addOnLabels, assign it to nil, otherwise patch operation will take no effect
	if len(addOnLabels) == 0 {
		addOnLabels = nil
	}
	// build cluster labels patch
	patchBytes, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels":          addOnLabels,
			"uid":             cluster.UID,
			"resourceVersion": cluster.ResourceVersion,
		}, // to ensure they appear in the patch as preconditions
	})
	if err != nil {
		return fmt.Errorf("failed to create patch for cluster %s: %w", cluster.Name, err)
	}

	// patch the cluster labels
	_, err = c.clusterClient.ClusterV1().ManagedClusters().Patch(ctx, cluster.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{})

	return err
}

func getAddOnLabelValue(addOn *addonv1alpha1.ManagedClusterAddOn) string {
	availableCondition := meta.FindStatusCondition(addOn.Status.Conditions, addonv1alpha1.ManagedClusterAddOnConditionAvailable)
	if availableCondition == nil {
		return addOnStatusUnreachable
	}

	switch availableCondition.Status {
	case metav1.ConditionTrue:
		return addOnStatusAvailable
	case metav1.ConditionFalse:
		return addOnStatusUnhealthy
	default:
		return addOnStatusUnreachable
	}
}
