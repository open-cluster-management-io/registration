package addon

import (
	"context"
	"fmt"
	"time"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonclient "open-cluster-management.io/api/client/addon/clientset/versioned"
	addonlisterv1alpha1 "open-cluster-management.io/api/client/addon/listers/addon/v1alpha1"
	"open-cluster-management.io/registration/pkg/helpers"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	coordv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
)

const leaseDurationTimes = 5

// AddOnLeaseControllerLeaseDurationSeconds is exposed so that integration tests can crank up the lease update speed.
// TODO: we may add this to ManagedClusterAddOn API to allow addon to adjust its own lease duration seconds
var AddOnLeaseControllerLeaseDurationSeconds = 60

// managedClusterAddOnLeaseController udpates managed cluster addons status on the hub cluster through watching the managed
// cluster status on the managed cluster.
type addOnLeaseController struct {
	clusterName    string
	addOnName      string
	clock          clock.Clock
	addOnClient    addonclient.Interface
	addOnLister    addonlisterv1alpha1.ManagedClusterAddOnLister
	hubLeaseClient coordv1client.CoordinationV1Interface
	leaseClient    coordv1client.CoordinationV1Interface
}

// NewManagedClusterAddOnLeaseController returns an instance of managedClusterAddOnLeaseController
func NewAddOnLeaseController(clusterName string,
	addOnName string,
	addOnClient addonclient.Interface,
	addOnLister addonlisterv1alpha1.ManagedClusterAddOnLister,
	hubLeaseClient coordv1client.CoordinationV1Interface,
	leaseClient coordv1client.CoordinationV1Interface,
	resyncInterval time.Duration,
	recorder events.Recorder) factory.Controller {
	c := &addOnLeaseController{
		clusterName:    clusterName,
		addOnName:      addOnName,
		clock:          clock.RealClock{},
		addOnClient:    addOnClient,
		addOnLister:    addOnLister,
		hubLeaseClient: hubLeaseClient,
		leaseClient:    leaseClient,
	}

	// TODO We do not add leaser informer to support kubernetes version lower than 1.17. Lease v1 api
	// is introduced in v1.17, hence adding lease informer in this controller will cause the hang of
	// informer cache sync and result in fatal exit of this controller. The code will be factored
	// when we no longer support kubernetes version lower than 1.17.
	return factory.New().
		WithSync(c.sync).
		ResyncEvery(resyncInterval).
		ToController("ManagedClusterAddOnLeaseController", recorder)
}

func (c *addOnLeaseController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	addOn, err := c.addOnLister.ManagedClusterAddOns(c.clusterName).Get(c.addOnName)
	if errors.IsNotFound(err) {
		// addon is not found, could be deleted, ignore it.
		return nil
	}
	if err != nil {
		return err
	}

	// "Customized" mode health check is supposed to delegate the health checking
	// to the addon manager.
	if addOn.Status.HealthCheck.Mode == addonv1alpha1.HealthCheckModeCustomized {
		return nil
	}

	return c.syncAddOn(ctx, getAddOnInstallationNamespace(addOn), addOn, syncCtx.Recorder())
}

func (c *addOnLeaseController) syncAddOn(ctx context.Context,
	leaseNamespace string,
	addOn *addonv1alpha1.ManagedClusterAddOn,
	recorder events.Recorder) error {
	now := c.clock.Now()
	gracePeriod := time.Duration(leaseDurationTimes*AddOnLeaseControllerLeaseDurationSeconds) * time.Second
	// addon lease name should be same with the addon name.
	observedLease, err := c.leaseClient.Leases(leaseNamespace).Get(ctx, addOn.Name, metav1.GetOptions{})

	var condition metav1.Condition
	switch {
	case errors.IsNotFound(err):
		// for backward compatible, before release-2.3, addons update their leases on hub cluster,
		// so if we cannot find addon lease on managed cluster, we will try to use addon hub lease.
		// TODO: after release-2.3, we will remove these code
		observedLease, err = c.hubLeaseClient.Leases(addOn.Namespace).Get(ctx, addOn.Name, metav1.GetOptions{})
		if err == nil {
			if now.Before(observedLease.Spec.RenewTime.Add(gracePeriod)) {
				// the lease is constantly updated, update its addon status to available
				condition = metav1.Condition{
					Type:    addonv1alpha1.ManagedClusterAddOnConditionAvailable,
					Status:  metav1.ConditionTrue,
					Reason:  "ManagedClusterAddOnLeaseUpdated",
					Message: fmt.Sprintf("%s add-on is available.", addOn.Name),
				}
				break
			}

			// the lease is not constantly updated, update its addon status to unavailable
			condition = metav1.Condition{
				Type:    addonv1alpha1.ManagedClusterAddOnConditionAvailable,
				Status:  metav1.ConditionFalse,
				Reason:  "ManagedClusterAddOnLeaseUpdateStopped",
				Message: fmt.Sprintf("%s add-on is not available.", addOn.Name),
			}
			break
		}
		condition = metav1.Condition{
			Type:    addonv1alpha1.ManagedClusterAddOnConditionAvailable,
			Status:  metav1.ConditionUnknown,
			Reason:  "ManagedClusterAddOnLeaseNotFound",
			Message: fmt.Sprintf("The status of %s add-on is unknown.", addOn.Name),
		}
	case err != nil:
		return err
	case err == nil:
		if now.Before(observedLease.Spec.RenewTime.Add(gracePeriod)) {
			// the lease is constantly updated, update its addon status to available
			condition = metav1.Condition{
				Type:    addonv1alpha1.ManagedClusterAddOnConditionAvailable,
				Status:  metav1.ConditionTrue,
				Reason:  "ManagedClusterAddOnLeaseUpdated",
				Message: fmt.Sprintf("%s add-on is available.", addOn.Name),
			}
			break
		}

		// the lease is not constantly updated, update its addon status to unavailable
		condition = metav1.Condition{
			Type:    addonv1alpha1.ManagedClusterAddOnConditionAvailable,
			Status:  metav1.ConditionFalse,
			Reason:  "ManagedClusterAddOnLeaseUpdateStopped",
			Message: fmt.Sprintf("%s add-on is not available.", addOn.Name),
		}
	}

	if meta.IsStatusConditionPresentAndEqual(addOn.Status.Conditions, condition.Type, condition.Status) {
		// addon status is not changed, do nothing
		return nil
	}

	_, updated, err := helpers.UpdateManagedClusterAddOnStatus(
		ctx,
		c.addOnClient,
		c.clusterName,
		addOn.Name,
		helpers.UpdateManagedClusterAddOnStatusFn(condition),
	)
	if err != nil {
		return err
	}
	if updated {
		recorder.Eventf("ManagedClusterAddOnStatusUpdated",
			"update managed cluster addon %q available condition to %q with its lease %q/%q status",
			addOn.Name, condition.Status, leaseNamespace, addOn.Name)
	}

	return nil
}
