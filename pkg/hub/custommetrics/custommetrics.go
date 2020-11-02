package custommetrics

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
)

var (
	//https://..../apis/cluster.open-cluster-management.io/v1/managedclusters
	//correspond to
	//.../apis/G/V/R
	mcGVR = schema.GroupVersionResource{
		Group:    "cluster.open-cluster-management.io",
		Version:  "v1",
		Resource: "managedclusters",
	}
)

//cluster_id = OCP ID of the Cluster (need to resolve for eks, etc)
//type = K8s Distribution, e.g. OCP, EKS, etc
//version = Distribution version
//cluster_infrastructure_provider = value "Type" from cluster_infrastructure_provider
//hub_id = cluster_id of hub server
//cluster_name =User Display Name of Cluster (defaults to Id if not provided)
var managedClusterMetric = k8smetrics.NewGaugeVec(&k8smetrics.GaugeOpts{
	Name: "a_managed_cluster",
	Help: "Managed Cluster being managed by ACM Hub.",
}, []string{"cluster_name", "type", "version", "cluster_infrastructure_provider"})

func getDynClient(controllerContext *controllercmd.ControllerContext) (dynamic.Interface, error) {

	return dynamic.NewForConfig(controllerContext.KubeConfig)
}

func addCluster(obj interface{}) {

	j, err := json.Marshal(obj.(*unstructured.Unstructured))
	if err != nil {
		klog.Warning("Error on ManagedCluster marshal.")
	}
	managedCluster := clusterv1.ManagedCluster{}
	err = json.Unmarshal(j, &managedCluster)
	if err != nil {
		klog.Warning("Error on ManagedCluster unmarshal.")

	}

	klog.Infof("Managed Cluster name being added: %s \n", managedCluster.GetName())

	//TODO:
	//get the actual values as mentioned here:
	//https://github.com/open-cluster-management/perf-analysis/blob/master/Big%20Picture.md#acm-20-telemetry-data
	managedClusterMetric.WithLabelValues(managedCluster.GetName(), "type", "version", managedCluster.GetLabels()["cloud"]).Set(1)
}

func delCluster(obj interface{}) {

	j, err := json.Marshal(obj.(*unstructured.Unstructured))
	if err != nil {
		klog.Warning("Error on ManagedCluster marshal.")
	}
	managedCluster := clusterv1.ManagedCluster{}
	err = json.Unmarshal(j, &managedCluster)
	if err != nil {
		klog.Warning("Error on ManagedCluster unmarshal.")
	}

	klog.Infof("Managed Cluster name being removed: %s \n", managedCluster.GetName())

	//TODO:
	//get the actual values as mentioned here:
	//https://github.com/open-cluster-management/perf-analysis/blob/master/Big%20Picture.md#acm-20-telemetry-data
	managedClusterMetric.WithLabelValues(managedCluster.GetName(), "type", "version", managedCluster.GetLabels()["cloud"]).Set(0)

}

func fetchManagedClusterData(c dynamic.Interface, wg *sync.WaitGroup) {

	defer wg.Done()

	//TODO: Test - will be removed
	managedClusterMetric.WithLabelValues("cluster_name", "type", "version", "cluster_infrastructure_provider").Set(2.354)

	klog.Infof("Getting data for Managed Clusters")

	var stopper chan struct{}
	informerRunning := false

	dynamicFactory := dynamicinformer.NewDynamicSharedInformerFactory(c, 60*time.Second)
	clusterInformer := dynamicFactory.ForResource(mcGVR).Informer()
	clusterInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			addCluster(obj)
		},
		UpdateFunc: func(prev interface{}, next interface{}) {
			klog.Info("Updating Managed Clusters ####################")
		},
		DeleteFunc: func(obj interface{}) {
			delCluster(obj)
		},
	})

	//Starting the informer
	for {
		if !informerRunning {
			klog.Info("Starting cluster informer routine for cluster watch")
			stopper = make(chan struct{})
			informerRunning = true
			go clusterInformer.Run(stopper)
		}
		//TODO: Check this setting
		time.Sleep(60 * time.Second)
	}

}

func MetricStart(controllerContext *controllercmd.ControllerContext) {

	var wg sync.WaitGroup

	// var customMetricsPort = port
	//registering the metrics to a custom registry
	//r := prometheus.NewRegistry()
	legacyregistry.MustRegister(managedClusterMetric)

	dynClient, errClient := getDynClient(controllerContext)
	if errClient != nil {
		klog.Fatalf("Error received creating client %v \n", errClient)
		panic(errClient.Error())
	}

	wg.Add(1)
	go fetchManagedClusterData(dynClient, &wg)

	wg.Wait()

}
