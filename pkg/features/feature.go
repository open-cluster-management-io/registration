package features

import (
	"k8s.io/component-base/featuregate"
	"k8s.io/klog/v2"
)

const (
	// Every feature gate should add method here following this template:
	//
	// // owner: @username
	// // alpha: v1.X
	// MyFeature featuregate.Feature = "MyFeature"

	// ClusterClaim will start a new controller in the spoke-agent to manage the cluster-claim
	// resources in the managed cluster.
	//
	// The cluster-claim controller is majorly for collecting claims and updating claims field
	// in managedcluster status. When it exceeds the limit specified by "--max-custom-cluster-claims",
	// the extra claims will be truncated.
	//
	// If it is disabled, the user will see empty claims field in managedcluster status. The
	// deployer who disable the feature may need to update claim field in managed cluster status
	// itself to avoid impact to users.
	ClusterClaim featuregate.Feature = "ClusterClaim"

	// AddonManagement will start new controllers in the spoke-agent to manage the managed cluster addons
	// registration and maintains the status of managed cluster addons through watching their leases.
	AddonManagement featuregate.Feature = "AddonManagement"

	// DefaultCluster will make registration hub controller to maintain a default cluster set. All clusters
	// without clusterset label will be automatically added into the default cluster set by adding a label
	// "cluster.open-cluster-management.io/clusterset=default" to the clusters.
	DefaultClusterSet featuregate.Feature = "DefaultClusterSet"

	// V1beta1CSRAPICompatibility will make the spoke registration agent to issue CSR requests
	// via V1beta1 api, so that registration agent can still manage the certificate rotation for the
	// ManagedCluster and  ManagedClusterAddon.
	// Note that kubernetes release [1.12, 1.18)'s beta CSR api doesn't have the "signerName" field which
	// means that all the approved CSR objects will be signed by the built-in CSR controller in
	// kube-controller-manager.
	V1beta1CSRAPICompatibility featuregate.Feature = "V1beta1CSRAPICompatibility"
)

var (
	// DefaultSpokeMutableFeatureGate is made up of multiple mutable feature-gates for registration agent.
	DefaultSpokeMutableFeatureGate featuregate.MutableFeatureGate = featuregate.NewFeatureGate()

	// DefaultHubMutableFeatureGate made up of multiple mutable feature-gates for registration hub controller.
	DefaultHubMutableFeatureGate featuregate.MutableFeatureGate = featuregate.NewFeatureGate()
)

func init() {
	if err := DefaultSpokeMutableFeatureGate.Add(defaultSpokeRegistrationFeatureGates); err != nil {
		klog.Fatalf("Unexpected error: %v", err)
	}
	if err := DefaultHubMutableFeatureGate.Add(defaultHubRegistrationFeatureGates); err != nil {
		klog.Fatalf("Unexpected error: %v", err)
	}
}

// defaultSpokeRegistrationFeatureGates consists of all known ocm-registration
// feature keys for registration agent.  To add a new feature, define a key for it above and
// add it here.
var defaultSpokeRegistrationFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	ClusterClaim:               {Default: true, PreRelease: featuregate.Beta},
	AddonManagement:            {Default: false, PreRelease: featuregate.Alpha},
	V1beta1CSRAPICompatibility: {Default: false, PreRelease: featuregate.Alpha},
}

// defaultHubRegistrationFeatureGates consists of all known ocm-registration
// feature keys for registration hub controller.  To add a new feature, define a key for it above and
// add it here.
var defaultHubRegistrationFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	DefaultClusterSet: {Default: false, PreRelease: featuregate.Alpha},
}
