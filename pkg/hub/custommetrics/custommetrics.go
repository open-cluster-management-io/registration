package custommetrics

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/http"
	"time"

	ocinfrav1 "github.com/openshift/api/config/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog"

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

	cvGVR = schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "clusterversions",
	}

	hubID = ""
)

//cluster_id = OCP ID of the Cluster (need to resolve for eks, etc)
//type = K8s Distribution, e.g. OCP, EKS, etc
//version = Distribution version
//cluster_infrastructure_provider = value "Type" from cluster_infrastructure_provider
//hub_id = cluster_id of hub server
//cluster_name =User Display Name of Cluster (defaults to Id if not provided)
var managedClusterMetric = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "a_managed_cluster",
	Help: "Managed Cluster being managed by ACM Hub.",
}, []string{"cluster_name", "cluster_id", "type", "version", "cluster_infrastructure_provider", "hub_id"})

func getDynClient() (dynamic.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	return dynamic.NewForConfig(config)
}

func getStaticData() {
	dynClient, errClient := getDynClient()
	if errClient != nil {
		klog.Fatalf("Error received creating client %v \n", errClient)
		panic(errClient.Error())
	}

	cvObj, errCv := dynClient.Resource(cvGVR).Get(context.TODO(), "version", metav1.GetOptions{})
	if errCv != nil {
		klog.Fatalf("Error getting cluster version %v \n", errClient)
		panic(errCv.Error())
	}
	cv := &ocinfrav1.ClusterVersion{}
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(cvObj.UnstructuredContent(), &cv)
	if err != nil {
		klog.Fatalf("Error unmarshal cluster version object%v \n", errClient)
		panic(errCv.Error())
	}
	hubID = string(cv.Spec.ClusterID)
	klog.Infof("hub id is " + hubID)

}

func fetchTestData() {

	//TODO: Test - will be removed
	managedClusterMetric.WithLabelValues("cluster_name", "cluster_id", "type", "version", "cluster_infrastructure_provider", "hub_id").Set(2.354)

	klog.Infof("Getting data for Managed Clusters")

	dynClient, errClient := getDynClient()
	if errClient != nil {
		klog.Fatalf("Error received creating client %v \n", errClient)
	}
	//TODO: rewrite the for-loop and sleep
	//Can we use WATCH
	//Need to see minimal role/permission needed for accessing API
	//it is set to cluster-reader now
	for {
		mcList, errCrd := dynClient.Resource(mcGVR).List(context.TODO(), metav1.ListOptions{})
		if errCrd != nil {
			klog.Infof("Error getting CRD %v \n", errCrd)
		} else {
			for _, mc := range mcList.Items {
				/* 		replicas, found, err := unstructured.NestedInt64(d.Object, "spec", "replicas")
				   		if err != nil || !found {
				   			fmt.Printf("Replicas not found for deployment %s: error=%s", d.GetName(), err)
				   			continue
				   		} */
				klog.Infof("Managed Cluster details %s \n", mc.GetName())

				cluster := &clusterv1.ManagedCluster{}
				err := runtime.DefaultUnstructuredConverter.FromUnstructured(mc.UnstructuredContent(), &cluster)
				if err != nil {
					klog.Fatalf("Error unmarshal managed cluster object%v \n", err)
				}

				//TODO:
				//get the actual values as mentioned here:
				//https://github.com/open-cluster-management/perf-analysis/blob/master/Big%20Picture.md#acm-20-telemetry-data
				managedClusterMetric.WithLabelValues(cluster.Name, "cluster_id", "type", "version", cluster.GetLabels()["cloud"], hubID).Set(1)
			}
		}
		time.Sleep(60 * time.Second)
	}

}

func MetricStart(port int32) {

	var customMetricsPort = port
	//registering the metrics to a custom registry
	r := prometheus.NewRegistry()
	r.MustRegister(managedClusterMetric)

	getStaticData()

	go fetchTestData()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello, %q, how are you ?\n", html.EscapeString(r.URL.Path))

	})

	http.Handle("/metrics", promhttp.HandlerFor(r, promhttp.HandlerOpts{}))

	klog.Infof("Custom Metric Service started on port: " + fmt.Sprint(customMetricsPort))
	log.Fatal(http.ListenAndServe(":"+fmt.Sprint(customMetricsPort), nil))
	//klog.Fatalf(http.ListenAndServe(":"+fmt.Sprint(customMetricsPort), nil))

}
