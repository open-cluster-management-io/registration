package cluster

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/mattbaird/jsonpatch"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"

	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	testinghelpers "open-cluster-management.io/registration/pkg/helpers/testing"
)

var managedclustersetsSchema = metav1.GroupVersionResource{
	Group:    "cluster.open-cluster-management.io",
	Version:  "v1",
	Resource: "managedclustersets",
}

func TestManagedClusterMutate(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name                   string
		request                *admissionv1beta1.AdmissionRequest
		expectedResponse       *admissionv1beta1.AdmissionResponse
		allowUpdateAcceptField bool
	}{
		{
			name: "mutate non-managedclusters request",
			request: &admissionv1beta1.AdmissionRequest{
				Resource: metav1.GroupVersionResource{
					Group:    "test.open-cluster-management.io",
					Version:  "v1",
					Resource: "tests",
				},
			},
			expectedResponse: newAdmissionResponse(true).build(),
		},
		{
			name: "mutate deleting operation",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersSchema,
				Operation: admissionv1beta1.Delete,
			},
			expectedResponse: newAdmissionResponse(true).build(),
		},
		{
			name: "new taints",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersSchema,
				Operation: admissionv1beta1.Create,
				Object: newManagedCluster().
					withLeaseDurationSeconds(60).
					addTaint(newTaint("a", "b", clusterv1.TaintEffectNoSelect, nil)).
					addTaint(newTaint("c", "d", clusterv1.TaintEffectPreferNoSelect, nil)).
					build(),
			},
			expectedResponse: newAdmissionResponse(true).
				addJsonPatch(newTaintTimeAddedJsonPatch(0, now)).
				addJsonPatch(newTaintTimeAddedJsonPatch(1, now)).
				build(),
		},
		{
			name: "new taint request denied",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersSchema,
				Operation: admissionv1beta1.Create,
				Object: newManagedCluster().
					withLeaseDurationSeconds(60).
					addTaint(newTaint("a", "b", clusterv1.TaintEffectNoSelect, newTime(now, 0))).
					addTaint(newTaint("c", "d", clusterv1.TaintEffectPreferNoSelect, newTime(now, 0))).
					build(),
			},
			expectedResponse: newAdmissionResponse(false).
				withResult(metav1.StatusFailure, http.StatusBadRequest, metav1.StatusReasonBadRequest, "It is not allowed to set TimeAdded of Taint \"a,c\".").
				build(),
		},
		{
			name: "update taint",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersSchema,
				Operation: admissionv1beta1.Create,
				OldObject: newManagedCluster().
					withLeaseDurationSeconds(60).
					addTaint(newTaint("a", "b", clusterv1.TaintEffectNoSelect, newTime(now, -10*time.Second))).
					addTaint(newTaint("c", "d", clusterv1.TaintEffectNoSelect, newTime(now, -10*time.Second))).
					build(),
				Object: newManagedCluster().
					withLeaseDurationSeconds(60).
					addTaint(newTaint("a", "b", clusterv1.TaintEffectNoSelect, newTime(now, -10*time.Second))). // no change
					addTaint(newTaint("c", "d", clusterv1.TaintEffectNoSelectIfNew, nil)).                      // effect modified
					build(),
			},
			expectedResponse: newAdmissionResponse(true).
				addJsonPatch(newTaintTimeAddedJsonPatch(1, now)).
				build(),
		},
		{
			name: "taint update request denied",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersSchema,
				Operation: admissionv1beta1.Create,
				OldObject: newManagedCluster().
					withLeaseDurationSeconds(60).
					addTaint(newTaint("a", "b", clusterv1.TaintEffectNoSelect, newTime(now, -10*time.Second))).
					addTaint(newTaint("c", "d", clusterv1.TaintEffectNoSelect, newTime(now, -10*time.Second))).
					build(),
				Object: newManagedCluster().
					withLeaseDurationSeconds(60).
					addTaint(newTaint("a", "b", clusterv1.TaintEffectNoSelect, newTime(now, -20*time.Second))).      // timeAdded modified
					addTaint(newTaint("c", "d", clusterv1.TaintEffectNoSelectIfNew, newTime(now, -10*time.Second))). // effect modified with timeAdded
					build(),
			},
			expectedResponse: newAdmissionResponse(false).
				withResult(metav1.StatusFailure, http.StatusBadRequest, metav1.StatusReasonBadRequest, "It is not allowed to set TimeAdded of Taint \"a,c\".").
				build(),
		},
		{
			name: "delete taint",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersSchema,
				Operation: admissionv1beta1.Create,
				OldObject: newManagedCluster().
					withLeaseDurationSeconds(60).
					addTaint(newTaint("a", "b", clusterv1.TaintEffectNoSelect, newTime(now, -10*time.Second))).
					addTaint(newTaint("c", "d", clusterv1.TaintEffectNoSelect, newTime(now, -10*time.Second))).
					build(),
				Object: newManagedCluster().
					withLeaseDurationSeconds(60).
					addTaint(newTaint("a", "b", clusterv1.TaintEffectNoSelect, newTime(now, -10*time.Second))).
					build(),
			},
			expectedResponse: newAdmissionResponse(true).build(),
		},
		{
			name: "mutate clusterset deleting operation",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersetsSchema,
				Operation: admissionv1beta1.Delete,
			},
			expectedResponse: newAdmissionResponse(true).build(),
		},
	}

	nowFunc = func() time.Time {
		return now
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			admissionHook := &ManagedClusterMutatingAdmissionHook{}
			actualResponse := admissionHook.Admit(c.request)

			if !reflect.DeepEqual(actualResponse, c.expectedResponse) {
				t.Errorf("expected\n%#v got : \n%#v", c.expectedResponse, actualResponse)
			}
		})
	}
}

func TestManagedClusterSetMutate(t *testing.T) {
	cases := []struct {
		name    string
		request *admissionv1beta1.AdmissionRequest
		allow   bool
	}{
		{
			name: "new empty spec clusterset",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersetsSchema,
				Operation: admissionv1beta1.Create,
				Object:    newManagedClusterSet("mcs1", false, "", "", "").build(),
			},
			allow: true,
		},
		{
			name: "new clusterset with other label key",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersetsSchema,
				Operation: admissionv1beta1.Create,
				Object:    newManagedClusterSet("mcs1", false, "", "deny-label-key", "").build(),
			},
			allow: false,
		},
		{
			name: "new clusterset with other label value",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersetsSchema,
				Operation: admissionv1beta1.Create,
				Object:    newManagedClusterSet("mcs1", false, "", "", "v1").build(),
			},
			allow: false,
		},
		{
			name: "new clusterset with right label key and nil value",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersetsSchema,
				Operation: admissionv1beta1.Create,
				Object:    newManagedClusterSet("mcs1", false, "", clusterSetLabel, "").build(),
			},
			allow: true,
		},
		{
			name: "new clusterset with right label key and right value",
			request: &admissionv1beta1.AdmissionRequest{
				Resource:  managedclustersetsSchema,
				Operation: admissionv1beta1.Create,
				Object:    newManagedClusterSet("mcs1", false, "", clusterSetLabel, "mcs1").build(),
			},
			allow: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			admissionHook := &ManagedClusterMutatingAdmissionHook{}
			actualResponse := admissionHook.Admit(c.request)

			if !reflect.DeepEqual(actualResponse.Allowed, c.allow) {
				t.Errorf("expected %#v got : %#v", c.allow, actualResponse.Allowed)
			}
		})
	}
}

type admissionResponseBuilder struct {
	jsonPatchOperations []jsonpatch.JsonPatchOperation
	response            admissionv1beta1.AdmissionResponse
}

func newAdmissionResponse(allowed bool) *admissionResponseBuilder {
	return &admissionResponseBuilder{
		response: admissionv1beta1.AdmissionResponse{
			Allowed: allowed,
		},
	}
}

func newclusterSelectorJsonPatch(path string, value interface{}) jsonpatch.JsonPatchOperation {
	return jsonpatch.JsonPatchOperation{
		Operation: "replace",
		Path:      path,
		Value:     value,
	}
}

func (b *admissionResponseBuilder) addJsonPatch(jsonPatch jsonpatch.JsonPatchOperation) *admissionResponseBuilder {
	b.jsonPatchOperations = append(b.jsonPatchOperations, jsonPatch)
	pt := admissionv1beta1.PatchTypeJSONPatch
	b.response.PatchType = &pt
	return b
}

func (b *admissionResponseBuilder) withResult(status string, code int32, reason metav1.StatusReason, message string) *admissionResponseBuilder {
	b.response.Result = &metav1.Status{
		Status:  status,
		Code:    code,
		Reason:  reason,
		Message: message,
	}
	return b
}

func (b *admissionResponseBuilder) build() *admissionv1beta1.AdmissionResponse {
	if len(b.jsonPatchOperations) > 0 {
		patch, _ := json.Marshal(b.jsonPatchOperations)
		b.response.Patch = patch
	}
	return &b.response
}

type managedClusterBuilder struct {
	cluster clusterv1.ManagedCluster
}

type managedClusterSetBuilder struct {
	clusterset clusterv1beta1.ManagedClusterSet
}

func (b *managedClusterSetBuilder) build() runtime.RawExtension {
	clusterObj, _ := json.Marshal(b.clusterset)
	return runtime.RawExtension{
		Raw: clusterObj,
	}
}

func newManagedClusterSet(name string, terminating bool, selectorType clusterv1beta1.SelectorType, key, value string) *managedClusterSetBuilder {
	return &managedClusterSetBuilder{
		clusterset: *testinghelpers.NewManagedClusterSet(name, terminating, selectorType, key, value),
	}
}

func newManagedCluster() *managedClusterBuilder {
	return &managedClusterBuilder{
		cluster: *testinghelpers.NewManagedCluster(),
	}
}

func (b *managedClusterBuilder) withLeaseDurationSeconds(leaseDurationSeconds int32) *managedClusterBuilder {
	b.cluster.Spec.LeaseDurationSeconds = leaseDurationSeconds
	return b
}

func (b *managedClusterBuilder) addTaint(taint clusterv1.Taint) *managedClusterBuilder {
	b.cluster.Spec.Taints = append(b.cluster.Spec.Taints, taint)
	return b
}

func (b *managedClusterBuilder) build() runtime.RawExtension {
	clusterObj, _ := json.Marshal(b.cluster)
	return runtime.RawExtension{
		Raw: clusterObj,
	}
}

func newTaint(key, value string, effect clusterv1.TaintEffect, timeAdded *metav1.Time) clusterv1.Taint {
	taint := clusterv1.Taint{
		Key:    key,
		Value:  value,
		Effect: effect,
	}

	if timeAdded != nil {
		taint.TimeAdded = *timeAdded
	}

	return taint
}

func newTime(time time.Time, offset time.Duration) *metav1.Time {
	mt := metav1.NewTime(time.Add(offset))
	return &mt
}
