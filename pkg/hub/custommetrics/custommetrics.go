package custommetrics

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
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

var managedClusterMetric = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "a_managed_cluster",
	Help: "Managed Cluster being managed by ACM Hub.",
}, []string{"name", "route", "snapshot"})

func fetchTestData() {

	//TODO: Test - will be removed
	managedClusterMetric.WithLabelValues("GET", "/test", "2.0.0").Set(2.354)

	klog.Infof("Getting data for Managed Clusters")

	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	dynClient, errClient := dynamic.NewForConfig(config)
	if errClient != nil {
		klog.Fatalf("Error received creating client %v \n", errClient)
	}

	//TODO: rewrite the for-loop and sleep
	//Can we use WATCH
	//Need to see minimal role/permission needed for accessing API
	//it is set to cluster-admin now
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
				//TODO:
				//get the actual values as mentioned here:
				//https://github.com/open-cluster-management/perf-analysis/blob/master/Big%20Picture.md#acm-20-telemetry-data
				managedClusterMetric.WithLabelValues(mc.GetName(), "/test", "2.0.0").Set(1)
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

	go fetchTestData()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello, %q, how are you ?\n", html.EscapeString(r.URL.Path))

	})

	http.Handle("/metrics", promhttp.HandlerFor(r, promhttp.HandlerOpts{}))

	klog.Infof("Custom Metric Service started on port: " + fmt.Sprint(customMetricsPort))
	log.Fatal(http.ListenAndServe(":"+fmt.Sprint(customMetricsPort), nil))
	//klog.Fatalf(http.ListenAndServe(":"+fmt.Sprint(customMetricsPort), nil))

}
