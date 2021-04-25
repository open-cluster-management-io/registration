package cluster

import (
	"encoding/json"
	"reflect"
	"testing"

	testinghelpers "github.com/open-cluster-management/registration/pkg/helpers/testing"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestManagedClusterMutate(t *testing.T) {
	pt := admissionv1.PatchTypeJSONPatch
	cases := []struct {
		name                   string
		request                *admissionv1.AdmissionRequest
		expectedResponse       *admissionv1.AdmissionResponse
		allowUpdateAcceptField bool
	}{
		{
			name: "mutate non-managedclusters request",
			request: &admissionv1.AdmissionRequest{
				Resource: metav1.GroupVersionResource{
					Group:    "test.open-cluster-management.io",
					Version:  "v1",
					Resource: "tests",
				},
			},
			expectedResponse: &admissionv1.AdmissionResponse{
				Allowed: true,
			},
		},
		{
			name: "mutate deleting operation",
			request: &admissionv1.AdmissionRequest{
				Resource:  managedclustersSchema,
				Operation: admissionv1.Delete,
			},
			expectedResponse: &admissionv1.AdmissionResponse{
				Allowed: true,
			},
		},
		{
			name: "mutate a ManagedCluster without LeaseDurationSeconds setting",
			request: &admissionv1.AdmissionRequest{
				Resource:  managedclustersSchema,
				Operation: admissionv1.Create,
				Object:    newManagedClusterObj(),
			},
			expectedResponse: &admissionv1.AdmissionResponse{
				Allowed:   true,
				Patch:     []byte(`[{"op": "replace", "path": "/spec/leaseDurationSeconds", "value": 60}]`),
				PatchType: &pt,
			},
		},
		{
			name: "mutate a ManagedCluster with LeaseDurationSeconds setting",
			request: &admissionv1.AdmissionRequest{
				Resource:  managedclustersSchema,
				Operation: admissionv1.Create,
				Object:    newManagedClusterObjWithLeaseDurationSeconds(60),
			},
			expectedResponse: &admissionv1.AdmissionResponse{
				Allowed: true,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			admissionHook := &ManagedClusterMutatingAdmissionHook{}

			actualResponse := admissionHook.Admit(c.request)

			if !reflect.DeepEqual(actualResponse, c.expectedResponse) {
				t.Errorf("expected %#v but got: %#v", c.expectedResponse.Result, actualResponse.Result)
			}
		})
	}
}

func newManagedClusterObjWithLeaseDurationSeconds(leaseDurationSeconds int32) runtime.RawExtension {
	managedCluster := testinghelpers.NewManagedCluster()
	managedCluster.Spec.LeaseDurationSeconds = leaseDurationSeconds
	clusterObj, _ := json.Marshal(managedCluster)
	return runtime.RawExtension{
		Raw: clusterObj,
	}
}
