package addon

import (
	"context"
	"time"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonclient "open-cluster-management.io/api/client/addon/clientset/versioned"
	addonlisterv1alpha1 "open-cluster-management.io/api/client/addon/listers/addon/v1alpha1"

	"github.com/openshift/library-go/pkg/operator/events"

	"k8s.io/apimachinery/pkg/util/clock"
	coordv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
)

// AddOnLeaseControllerSyncInterval is exposed so that integration tests can crank up the constroller sync speed.
// TODO if we register the lease informer to the lease controller, we need to increase this time
var AddOnLeaseControllerSyncInterval = 30 * time.Second

const (
	leaseLocationManagementCluster = "ManagementCluster"
	leaseLocationManagedCluster    = "ManagedCluster"
)

type leaseConfig struct {
	location string
	stopFunc context.CancelFunc
}

// managedClusterAddOnLeaseController udpates managed cluster addons status on the hub cluster through watching the managed
// cluster status on the managed cluster.
type addOnLeaseControllerManager struct {
	clusterName           string
	clock                 clock.Clock
	addOnClient           addonclient.Interface
	addOnLister           addonlisterv1alpha1.ManagedClusterAddOnLister
	hubLeaseClient        coordv1client.CoordinationV1Interface
	managementLeaseClient coordv1client.CoordinationV1Interface
	managedLeaseClient    coordv1client.CoordinationV1Interface
	recorder              events.Recorder

	addOnLeaseConfigs map[string]leaseConfig
}

// NewManagedClusterAddOnLeaseController returns an instance of managedClusterAddOnLeaseController
func NewAddOnLeaseControllerManager(clusterName string,
	addOnClient addonclient.Interface,
	addOnLister addonlisterv1alpha1.ManagedClusterAddOnLister,
	hubLeaseClient coordv1client.CoordinationV1Interface,
	managementLeaseClient coordv1client.CoordinationV1Interface,
	managedLeaseClient coordv1client.CoordinationV1Interface,
	recorder events.Recorder) AddOnControllerManager {
	return &addOnLeaseControllerManager{
		clusterName:           clusterName,
		clock:                 clock.RealClock{},
		addOnClient:           addOnClient,
		addOnLister:           addOnLister,
		hubLeaseClient:        hubLeaseClient,
		managementLeaseClient: managementLeaseClient,
		managedLeaseClient:    managedLeaseClient,
		recorder:              recorder,
		addOnLeaseConfigs:     map[string]leaseConfig{},
	}
}

func (c *addOnLeaseControllerManager) RunControllers(ctx context.Context, addOn *addonv1alpha1.ManagedClusterAddOn) error {
	cachedConfig, config := c.addOnLeaseConfigs[addOn.Name], getAddOnLeaseConfig(addOn)

	// no work if the lease config exists and has no change
	if cachedConfig.location == config.location {
		return nil
	}

	// otherwise, stop the current lease controller if the lease config exists and changes
	if err := c.StopControllers(ctx, addOn.Name); err != nil {
		return err
	}

	// start a lease controller with new/updated lease config
	config.stopFunc = c.startAddOnLeaseController(ctx, addOn.Name, config)
	c.addOnLeaseConfigs[addOn.Name] = config

	return nil
}

func (c *addOnLeaseControllerManager) StopControllers(ctx context.Context, addOnName string) error {
	config, ok := c.addOnLeaseConfigs[addOnName]
	if !ok {
		return nil
	}

	if config.stopFunc != nil {
		config.stopFunc()
	}

	delete(c.addOnLeaseConfigs, addOnName)
	return nil
}

func (c *addOnLeaseControllerManager) startAddOnLeaseController(ctx context.Context, addOnName string, config leaseConfig) context.CancelFunc {
	leaseClient := c.managedLeaseClient
	if config.location == leaseLocationManagementCluster {
		leaseClient = c.managementLeaseClient
	}
	addOnleaseController := NewAddOnLeaseController(c.clusterName,
		addOnName,
		c.addOnClient,
		c.addOnLister,
		c.hubLeaseClient,
		leaseClient,
		AddOnLeaseControllerSyncInterval,
		c.recorder,
	)

	ctx, stopFunc := context.WithCancel(ctx)
	go addOnleaseController.Run(ctx, 1)

	return stopFunc
}

func getAddOnLeaseConfig(addOn *addonv1alpha1.ManagedClusterAddOn) leaseConfig {
	location := leaseLocationManagedCluster
	if isAddonRunningOutsideManagedCluster(addOn) {
		location = leaseLocationManagementCluster
	}
	return leaseConfig{
		location: location,
	}
}
