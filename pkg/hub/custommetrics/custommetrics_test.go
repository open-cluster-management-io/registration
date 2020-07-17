package custommetrics

import (
	"testing"

	ocinfrav1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestGetHubClusterId(t *testing.T) {

	s := scheme.Scheme
	if err := ocinfrav1.AddToScheme(s); err != nil {
		t.Fatalf("Unable to add ocinfrav1 scheme: (%v)", err)
	}

	id := "1234567890"
	cv := &ocinfrav1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "version"},
		Spec: ocinfrav1.ClusterVersionSpec{
			ClusterID: ocinfrav1.ClusterID(id),
		},
	}

	c := fake.NewSimpleDynamicClient(s, cv)
	getHubClusterId(c)
	if hubID != id {
		t.Fatal("Failed to get cluster id")
	}
}
