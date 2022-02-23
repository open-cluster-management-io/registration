package cluster

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mattbaird/jsonpatch"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	"open-cluster-management.io/registration/pkg/helpers"

	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

var nowFunc = time.Now

// ManagedClusterMutatingAdmissionHook will mutate the creating/updating managedcluster request.
type ManagedClusterMutatingAdmissionHook struct{}

// MutatingResource is called by generic-admission-server on startup to register the returned REST resource through which the
// webhook is accessed by the kube apiserver.
func (a *ManagedClusterMutatingAdmissionHook) MutatingResource() (schema.GroupVersionResource, string) {
	return schema.GroupVersionResource{
			Group:    "admission.cluster.open-cluster-management.io",
			Version:  "v1",
			Resource: "managedclustermutators",
		},
		"managedclustermutators"
}

// Admit is called by generic-admission-server when the registered REST resource above is called with an admission request.
func (a *ManagedClusterMutatingAdmissionHook) Admit(req *admissionv1beta1.AdmissionRequest) *admissionv1beta1.AdmissionResponse {
	klog.V(4).Infof("mutate %q operation for object %q", req.Operation, req.Object)
	status := &admissionv1beta1.AdmissionResponse{
		Allowed: true,
	}
	// only mutate create and update operation
	if req.Operation != admissionv1beta1.Create && req.Operation != admissionv1beta1.Update {
		return status
	}

	// only mutate the request for managedcluster
	if req.Resource.Group != "cluster.open-cluster-management.io" {
		return status
	}

	switch req.Resource.Resource {
	case "managedclusters":
		return a.processManagedCluster(req)
	case "managedclustersets":
		return a.processManagedClusterSet(req)
	}

	return status
}

// processManagedClusterSet handle ManagedClusterSet obj
func (a *ManagedClusterMutatingAdmissionHook) processManagedClusterSet(req *admissionv1beta1.AdmissionRequest) *admissionv1beta1.AdmissionResponse {
	status := &admissionv1beta1.AdmissionResponse{
		Allowed: true,
	}

	var jsonPatches []jsonpatch.JsonPatchOperation

	// set default value for managedClusterset.spec.clusterSelector
	clusterSetJsonPatches, status := a.procesManagedClusterSetSpec(req.Object)
	if !status.Allowed {
		return status
	}
	jsonPatches = append(jsonPatches, clusterSetJsonPatches...)

	if len(jsonPatches) == 0 {
		return status
	}

	patch, err := json.Marshal(jsonPatches)
	if err != nil {
		status.Allowed = false
		status.Result = &metav1.Status{
			Status: metav1.StatusFailure, Code: http.StatusInternalServerError, Reason: metav1.StatusReasonInternalError,
			Message: err.Error(),
		}
		return status
	}

	status.Patch = patch
	pt := admissionv1beta1.PatchTypeJSONPatch
	status.PatchType = &pt
	return status
}

func (a *ManagedClusterMutatingAdmissionHook) procesManagedClusterSetSpec(clusterSetObj runtime.RawExtension) ([]jsonpatch.JsonPatchOperation, *admissionv1beta1.AdmissionResponse) {
	status := &admissionv1beta1.AdmissionResponse{
		Allowed: true,
	}
	clusterSet := &clusterv1beta1.ManagedClusterSet{}
	if err := json.Unmarshal(clusterSetObj.Raw, clusterSet); err != nil {
		status.Allowed = false
		status.Result = &metav1.Status{
			Status: metav1.StatusFailure, Code: http.StatusBadRequest, Reason: metav1.StatusReasonBadRequest,
			Message: err.Error(),
		}
		return nil, status
	}

	newClusterSet := clusterSet.DeepCopy()

	if clusterSet.Spec.ClusterSelector.SelectorType == "" {
		newClusterSet.Spec.ClusterSelector.SelectorType = clusterv1beta1.ExclusiveLabel
	}

	if clusterSet.Spec.ClusterSelector.ExclusiveLabel != nil {
		// clusterset.Spec.ClusterSelector.ExclusiveLabel.Key can only be "" or "cluster.open-cluster-management.io/clusterset"
		if clusterSet.Spec.ClusterSelector.ExclusiveLabel.Key != "" && clusterSet.Spec.ClusterSelector.ExclusiveLabel.Key != clusterSetLabel {
			status.Allowed = false
			status.Result = &metav1.Status{
				Status: metav1.StatusFailure, Code: http.StatusInternalServerError, Reason: metav1.StatusReasonInternalError,
				Message: fmt.Sprintf("The spec.clusterSelector.exclusiveLabel.key must be %q.", clusterSetLabel),
			}
			return nil, status
		}
		// clusterset.Spec.ClusterSelector.ExclusiveLabel.Value can only be "" or clusterset name
		if clusterSet.Spec.ClusterSelector.ExclusiveLabel.Value != "" && clusterSet.Spec.ClusterSelector.ExclusiveLabel.Value != clusterSet.Name {
			status.Allowed = false
			status.Result = &metav1.Status{
				Status: metav1.StatusFailure, Code: http.StatusInternalServerError, Reason: metav1.StatusReasonInternalError,
				Message: fmt.Sprintf("The spec.clusterSelector.ExclusiveLabel.value must be %q.", clusterSet.Name),
			}
			return nil, status
		}
	}

	newClusterSet.Spec.ClusterSelector.ExclusiveLabel = &clusterv1beta1.ManagedClusterLabel{
		Key:   clusterSetLabel,
		Value: clusterSet.Name,
	}

	newClusterSetObj, err := json.Marshal(newClusterSet)
	if err != nil {
		status.Allowed = false
		status.Result = &metav1.Status{
			Status: metav1.StatusFailure, Code: http.StatusInternalServerError, Reason: metav1.StatusReasonInternalError,
			Message: err.Error(),
		}
		return nil, status
	}

	res, err := jsonpatch.CreatePatch(clusterSetObj.Raw, newClusterSetObj)
	if err != nil {
		status.Allowed = false
		status.Result = &metav1.Status{
			Status: metav1.StatusFailure, Code: http.StatusInternalServerError, Reason: metav1.StatusReasonInternalError,
			Message: fmt.Sprintf("Create patch error, Error:  %v", err.Error()),
		}
		return nil, status
	}
	return res, status
}

//processManagedCluster handle managedCluster obj
func (a *ManagedClusterMutatingAdmissionHook) processManagedCluster(req *admissionv1beta1.AdmissionRequest) *admissionv1beta1.AdmissionResponse {
	status := &admissionv1beta1.AdmissionResponse{
		Allowed: true,
	}
	managedCluster := &clusterv1.ManagedCluster{}
	if err := json.Unmarshal(req.Object.Raw, managedCluster); err != nil {
		status.Allowed = false
		status.Result = &metav1.Status{
			Status: metav1.StatusFailure, Code: http.StatusBadRequest, Reason: metav1.StatusReasonBadRequest,
			Message: err.Error(),
		}
		return status
	}

	var jsonPatches []jsonpatch.JsonPatchOperation

	// set timeAdded of taint if it is nil and reset it if it is modified
	taintJsonPatches, status := a.processTaints(managedCluster, req.OldObject.Raw)
	if !status.Allowed {
		return status
	}
	jsonPatches = append(jsonPatches, taintJsonPatches...)

	if len(jsonPatches) == 0 {
		return status
	}

	patch, err := json.Marshal(jsonPatches)
	if err != nil {
		status.Allowed = false
		status.Result = &metav1.Status{
			Status: metav1.StatusFailure, Code: http.StatusInternalServerError, Reason: metav1.StatusReasonInternalError,
			Message: err.Error(),
		}
		return status
	}

	status.Patch = patch
	pt := admissionv1beta1.PatchTypeJSONPatch
	status.PatchType = &pt
	return status
}

// processTaints generates json patched for cluster taints
func (a *ManagedClusterMutatingAdmissionHook) processTaints(managedCluster *clusterv1.ManagedCluster, oldManagedClusterRaw []byte) ([]jsonpatch.JsonPatchOperation, *admissionv1beta1.AdmissionResponse) {
	status := &admissionv1beta1.AdmissionResponse{
		Allowed: true,
	}

	if len(managedCluster.Spec.Taints) == 0 {
		return nil, status
	}

	var oldManagedCluster *clusterv1.ManagedCluster
	if len(oldManagedClusterRaw) > 0 {
		cluster := &clusterv1.ManagedCluster{}
		if err := json.Unmarshal(oldManagedClusterRaw, cluster); err != nil {
			status.Allowed = false
			status.Result = &metav1.Status{
				Status: metav1.StatusFailure, Code: http.StatusInternalServerError, Reason: metav1.StatusReasonInternalError,
				Message: err.Error(),
			}
			return nil, status
		}
		oldManagedCluster = cluster
	}

	var invalidTaints []string
	var jsonPatches []jsonpatch.JsonPatchOperation
	now := metav1.NewTime(nowFunc())
	for index, taint := range managedCluster.Spec.Taints {
		originalTaint := helpers.FindTaintByKey(oldManagedCluster, taint.Key)
		switch {
		case originalTaint == nil:
			// new taint
			if !taint.TimeAdded.IsZero() {
				invalidTaints = append(invalidTaints, taint.Key)
				continue
			}
			jsonPatches = append(jsonPatches, newTaintTimeAddedJsonPatch(index, now.Time))
		case originalTaint.Value == taint.Value && originalTaint.Effect == taint.Effect:
			// no change
			if !originalTaint.TimeAdded.Equal(&taint.TimeAdded) {
				invalidTaints = append(invalidTaints, taint.Key)
			}
		default:
			// taint's value/effect has changed
			if !taint.TimeAdded.IsZero() {
				invalidTaints = append(invalidTaints, taint.Key)
				continue
			}
			jsonPatches = append(jsonPatches, newTaintTimeAddedJsonPatch(index, now.Time))
		}
	}

	if len(invalidTaints) == 0 {
		return jsonPatches, status
	}

	status.Allowed = false
	status.Result = &metav1.Status{
		Status: metav1.StatusFailure, Code: http.StatusBadRequest, Reason: metav1.StatusReasonBadRequest,
		Message: fmt.Sprintf("It is not allowed to set TimeAdded of Taint %q.", strings.Join(invalidTaints, ",")),
	}
	return nil, status
}

// Initialize is called by generic-admission-server on startup to setup initialization that managedclusters webhook needs.
func (a *ManagedClusterMutatingAdmissionHook) Initialize(kubeClientConfig *rest.Config, stopCh <-chan struct{}) error {
	// do nothing
	return nil
}

func newTaintTimeAddedJsonPatch(index int, timeAdded time.Time) jsonpatch.JsonPatchOperation {
	return jsonpatch.JsonPatchOperation{
		Operation: "replace",
		Path:      fmt.Sprintf("/spec/taints/%d/timeAdded", index),
		Value:     timeAdded.UTC().Format(time.RFC3339),
	}
}
