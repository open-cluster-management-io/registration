package webhook

import (
	"context"

	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/runtime"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	internalv1 "open-cluster-management.io/registration/pkg/webhook/v1"
	internalv1beta1 "open-cluster-management.io/registration/pkg/webhook/v1beta1"
	internalv1beta2 "open-cluster-management.io/registration/pkg/webhook/v1beta2"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(clusterv1.Install(scheme))
	utilruntime.Must(internalv1beta1.Install(scheme))
	utilruntime.Must(internalv1beta2.Install(scheme))
}

func (c *Options) RunWebhookServer() error {
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Port:                   c.Port,
		HealthProbeBindAddress: ":8000",
		CertDir:                c.CertDir,
		WebhookServer:          &webhook.Server{TLSMinVersion: "1.3"},
	})
	logger:= klog.LoggerWithName(klog.FromContext(context.Background()), "Webhook Server")

	if err != nil {
		logger.Error(err, "Unable to start manager")
		return err
	}

	// add healthz/readyz check handler
	if err := mgr.AddHealthzCheck("healthz-ping", healthz.Ping); err != nil {
		logger.Error(err, "Unable to add healthz check handler")
		return err
	}

	if err := mgr.AddReadyzCheck("readyz-ping", healthz.Ping); err != nil {
		logger.Error(err, "Unable to add readyz check handler")
		return err
	}

	if err = (&internalv1.ManagedClusterWebhook{}).Init(mgr); err != nil {
		logger.Error(err, "Unable to create ManagedCluster webhook")
		return err
	}
	if err = (&internalv1beta1.ManagedClusterSetBindingWebhook{}).Init(mgr); err != nil {
		logger.Error(err, "Unable to create ManagedClusterSetBinding webhook", "version", "v1beta1")
		return err
	}
	if err = (&internalv1beta2.ManagedClusterSetBindingWebhook{}).Init(mgr); err != nil {
		logger.Error(err, "Unable to create ManagedClusterSetBinding webhook", "version", "v1beta2")
		return err
	}
	if err = (&internalv1beta1.ManagedClusterSet{}).SetupWebhookWithManager(mgr); err != nil {
		logger.Error(err, "Unable to create ManagedClusterSet webhook", "version", "v1beta1")
		return err
	}
	if err = (&internalv1beta2.ManagedClusterSet{}).SetupWebhookWithManager(mgr); err != nil {
		logger.Error(err, "Unable to create ManagedClusterSet webhook", "version", "v1beta2")
		return err
	}

	logger.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "problem running manager")
		return err
	}
	return nil
}
