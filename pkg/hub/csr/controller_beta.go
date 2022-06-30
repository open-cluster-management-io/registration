package csr

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"open-cluster-management.io/registration/pkg/helpers"
	"open-cluster-management.io/registration/pkg/hub/user"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	authorizationv1 "k8s.io/api/authorization/v1"
	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
	corev1 "k8s.io/api/core/v1"
	certificatesv1beta1informers "k8s.io/client-go/informers/certificates/v1beta1"
	certificatesv1beta1lister "k8s.io/client-go/listers/certificates/v1beta1"
)

// v1beta1CSRApprovingController auto approve the renewal CertificateSigningRequests for an accepted spoke cluster on the hub.
type v1beta1CSRApprovingController struct {
	kubeClient    kubernetes.Interface
	csrLister     certificatesv1beta1lister.CertificateSigningRequestLister
	eventRecorder events.Recorder
}

func NewV1beta1CSRApprovingController(
	kubeClient kubernetes.Interface,
	v1beta1CSRInformer certificatesv1beta1informers.CertificateSigningRequestInformer,
	recorder events.Recorder) factory.Controller {

	c := &v1beta1CSRApprovingController{
		kubeClient:    kubeClient,
		csrLister:     v1beta1CSRInformer.Lister(),
		eventRecorder: recorder.WithComponentSuffix("csr-approving-controller"),
	}

	return factory.New().WithInformersQueueKeyFunc(func(obj runtime.Object) string {
		accessor, _ := meta.Accessor(obj)
		return accessor.GetName()
	}, v1beta1CSRInformer.Informer()).
		WithSync(c.sync).
		ToController("CSRApprovingController", recorder)
}

func (c *v1beta1CSRApprovingController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	csrName := syncCtx.QueueKey()
	klog.V(4).Infof("Reconciling CertificateSigningRequests %q", csrName)
	csr, err := c.csrLister.Get(csrName)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	csr = csr.DeepCopy()
	// Current csr is in terminal state, do nothing.
	if helpers.Isv1beta1CSRInTerminalState(&csr.Status) {
		return nil
	}

	// Check whether current csr is a renewal spoke cluster csr.
	isRenewal := isV1beta1SpokeClusterClientCertRenewal(csr)
	if !isRenewal {
		klog.V(4).Infof("CSR %q was not recognized", csr.Name)
		return nil
	}

	allowed, err := c.authorize(ctx, csr)
	if err != nil {
		return err
	}
	if !allowed {
		//TODO find a way to avoid looking at this CSR again.
		klog.V(4).Infof("Managed cluster csr %q cannont be auto approved due to subject access review was not approved", csr.Name)
		return nil
	}

	// Auto approve the spoke cluster csr
	csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1beta1.CertificateSigningRequestCondition{
		Type:    certificatesv1beta1.CertificateApproved,
		Status:  corev1.ConditionTrue,
		Reason:  "AutoApprovedByHubCSRApprovingController",
		Message: "Auto approving Managed cluster agent certificate after SubjectAccessReview.",
	})
	_, err = c.kubeClient.CertificatesV1beta1().CertificateSigningRequests().UpdateApproval(ctx, csr, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	c.eventRecorder.Eventf("ManagedClusterCSRAutoApproved", "spoke cluster csr %q is auto approved by hub csr controller", csr.Name)
	return nil
}

// To check a renewal managed cluster csr, we check
// 1. if the signer name in csr request is valid.
// 2. if organization field and commonName field in csr request is valid.
// 3. if user name in csr is the same as commonName field in csr request.
func (c *v1beta1CSRApprovingController) authorize(ctx context.Context, csr *certificatesv1beta1.CertificateSigningRequest) (bool, error) {
	extra := make(map[string]authorizationv1.ExtraValue)
	for k, v := range csr.Spec.Extra {
		extra[k] = authorizationv1.ExtraValue(v)
	}

	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   csr.Spec.Username,
			UID:    csr.Spec.UID,
			Groups: csr.Spec.Groups,
			Extra:  extra,
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Group:       "register.open-cluster-management.io",
				Resource:    "managedclusters",
				Verb:        "renew",
				Subresource: "clientcertificates",
			},
		},
	}
	sar, err := c.kubeClient.AuthorizationV1().SubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
	if err != nil {
		return false, err
	}
	return sar.Status.Allowed, nil
}

func isV1beta1SpokeClusterClientCertRenewal(csr *certificatesv1beta1.CertificateSigningRequest) bool {
	spokeClusterName, existed := csr.Labels[spokeClusterNameLabel]
	if !existed {
		return false
	}

	if *csr.Spec.SignerName != certificatesv1beta1.KubeAPIServerClientSignerName {
		return false
	}

	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		klog.V(4).Infof("csr %q was not recognized: PEM block type is not CERTIFICATE REQUEST", csr.Name)
		return false
	}

	x509cr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		klog.V(4).Infof("csr %q was not recognized: %v", csr.Name, err)
		return false
	}

	requestingOrgs := sets.NewString(x509cr.Subject.Organization...)
	if requestingOrgs.Has(user.ManagedClustersGroup) { // optional common group for backward-compatibility
		requestingOrgs.Delete(user.ManagedClustersGroup)
	}
	if requestingOrgs.Len() != 1 {
		return false
	}

	expectedPerClusterOrg := fmt.Sprintf("%s%s", user.SubjectPrefix, spokeClusterName)
	if !requestingOrgs.Has(expectedPerClusterOrg) {
		return false
	}

	if !strings.HasPrefix(x509cr.Subject.CommonName, expectedPerClusterOrg) {
		return false
	}

	return csr.Spec.Username == x509cr.Subject.CommonName
}
