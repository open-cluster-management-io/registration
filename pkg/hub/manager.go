package hub

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	clusterv1client "github.com/open-cluster-management/api/client/cluster/clientset/versioned"
	clusterv1informers "github.com/open-cluster-management/api/client/cluster/informers/externalversions"
	"github.com/open-cluster-management/registration/pkg/hub/csr"
	"github.com/open-cluster-management/registration/pkg/hub/custommetrics"
	"github.com/open-cluster-management/registration/pkg/hub/lease"
	"github.com/open-cluster-management/registration/pkg/hub/managedcluster"

	kubeinformers "k8s.io/client-go/informers"
)

// RunControllerManager starts the controllers on hub to manage spoke cluster registration.
func RunControllerManager(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
	kubeClient, err := kubernetes.NewForConfig(controllerContext.KubeConfig)
	if err != nil {
		return err
	}

	clusterClient, err := clusterv1client.NewForConfig(controllerContext.KubeConfig)
	if err != nil {
		return err
	}

	clusterInformers := clusterv1informers.NewSharedInformerFactory(clusterClient, 10*time.Minute)
	kubeInfomers := kubeinformers.NewSharedInformerFactory(kubeClient, 10*time.Minute)

	managedClusterController := managedcluster.NewManagedClusterController(
		kubeClient,
		clusterClient,
		clusterInformers.Cluster().V1().ManagedClusters().Informer(),
		controllerContext.EventRecorder,
	)

	csrController := csr.NewCSRApprovingController(
		kubeClient,
		kubeInfomers.Certificates().V1beta1().CertificateSigningRequests().Informer(),
		controllerContext.EventRecorder,
	)

	leaseController := lease.NewClusterLeaseController(
		kubeClient,
		clusterClient,
		clusterInformers.Cluster().V1().ManagedClusters(),
		kubeInfomers.Coordination().V1().Leases(),
		5*time.Minute, //TODO: this interval time should be allowed to change from outside
		controllerContext.EventRecorder,
	)

	go clusterInformers.Start(ctx.Done())
	go kubeInfomers.Start(ctx.Done())

	go managedClusterController.Run(ctx, 1)
	go csrController.Run(ctx, 1)
	go leaseController.Run(ctx, 1)

	//Add Custom Metrics
	//make sure its a go func call else it will block
	enableMetric := false
	val, exists := os.LookupEnv("METRIC_ENABLE")
	if exists {
		enableMetric, err = strconv.ParseBool(val)
		if err != nil {
			klog.Warning("Error parsing env METRIC_ENABLE.  Expected a bool.  Original error: ", err)
			klog.Info("Falling back on default FALSE; Metric collection will be disabled")
		}
	}

	if enableMetric {
		go custommetrics.MetricStart(8890)
	}

	<-ctx.Done()
	return nil
}
