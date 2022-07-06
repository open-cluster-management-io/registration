package addon

import (
	"context"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
)

type AddOnControllerManager interface {
	// RunControllers runs or restarts one or multiple controllers for the add-on
	RunControllers(ctx context.Context, addOn *addonv1alpha1.ManagedClusterAddOn) error
	// StopControllers stops all controllers started for the add-on
	StopControllers(ctx context.Context, addOnName string) error
}
