package criticalresourceadmissionwebhook

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/labels"

	"k8s.io/apimachinery/pkg/api/errors"

	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	apiserverv1 "github.com/openshift/api/apiserver/v1"
	apiserverclient "github.com/openshift/client-go/apiserver/clientset/versioned"
	apiserverinformers "github.com/openshift/client-go/apiserver/informers/externalversions"
	apiserverlisters "github.com/openshift/client-go/apiserver/listers/apiserver/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

// ManagedClusterAdmissionHook will validate the creating/updating managedcluster request.
type CriticalResourceAdmissionWebhook struct {
	kubeClient                kubernetes.Interface
	dynamicClient             dynamic.Interface
	criticalResourceClient    apiserverclient.Interface
	criticalResourceLister    apiserverlisters.CriticalResourceLister
	criticalResourceHasSynced cache.InformerSynced
}

// ValidatingResource is called by generic-admission-server on startup to register the returned REST resource through which the
// webhook is accessed by the kube apiserver.
func (a *CriticalResourceAdmissionWebhook) ValidatingResource() (plural schema.GroupVersionResource, singular string) {
	return schema.GroupVersionResource{
			Group:    "admission.apiserver.openshift.io",
			Version:  "v1",
			Resource: "criticalresourcesadmissionvalidators",
		},
		"criticalresourcesadmissionvalidator"
}

// Validate is called by generic-admission-server when the registered REST resource above is called with an admission request.
func (a *CriticalResourceAdmissionWebhook) Validate(admissionSpec *admissionv1beta1.AdmissionRequest) *admissionv1beta1.AdmissionResponse {
	klog.V(4).Infof("validate %q operation for object %q", admissionSpec.Operation, admissionSpec.Object)

	status := &admissionv1beta1.AdmissionResponse{}

	switch admissionSpec.Operation {
	case admissionv1beta1.Delete:
	default:
		status.Allowed = true
		return status
	}

	return a.validateDelete(context.TODO(), admissionSpec)
}

// Initialize is called by generic-admission-server on startup to setup initialization that managedclusters webhook needs.
func (a *CriticalResourceAdmissionWebhook) Initialize(kubeClientConfig *rest.Config, stopCh <-chan struct{}) error {
	var err error
	a.kubeClient, err = kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return err
	}
	a.dynamicClient, err = dynamic.NewForConfig(kubeClientConfig)
	if err != nil {
		return err
	}

	apiserverClient, err := apiserverclient.NewForConfig(kubeClientConfig)
	if err != nil {
		return err
	}
	apiserverInfomers := apiserverinformers.NewSharedInformerFactory(apiserverClient, 12*time.Hour)
	a.criticalResourceHasSynced = apiserverInfomers.Apiserver().V1().CriticalResources().Informer().HasSynced
	a.criticalResourceLister = apiserverInfomers.Apiserver().V1().CriticalResources().Lister()
	apiserverInfomers.Start(stopCh)

	return err
}

// validateCreateRequest validates create managed cluster operation
func (a *CriticalResourceAdmissionWebhook) validateDelete(ctx context.Context, request *admissionv1beta1.AdmissionRequest) *admissionv1beta1.AdmissionResponse {
	gr := schema.GroupResource{
		Group:    request.Resource.Group,
		Resource: request.Resource.Resource,
	}
	switch gr {
	case apiserverv1.Resource("criticalresources"):
		return a.validateCriticalResourceDelete(ctx, request)
	case corev1.Resource("namespace"):
		return a.validateNamespaceDelete(ctx, request)
	case appsv1.Resource("deployments"):
		return a.validateProviderDelete(ctx, gr, request)
	default:
		return &admissionv1beta1.AdmissionResponse{Allowed: true}
	}
}

func (a *CriticalResourceAdmissionWebhook) validateProviderDelete(ctx context.Context, groupResource schema.GroupResource, request *admissionv1beta1.AdmissionRequest) *admissionv1beta1.AdmissionResponse {
	criticalResources, err := a.criticalResourceLister.CriticalResources(request.Namespace).List(labels.Everything())
	if err != nil {
		return &admissionv1beta1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	messages := []string{}
	for _, criticalResource := range criticalResources {
		providerGR := schema.GroupResource{
			Group:    criticalResource.Spec.Provider.GroupResource.Group,
			Resource: criticalResource.Spec.Provider.GroupResource.Resource,
		}
		if providerGR != groupResource {
			continue
		}
		if request.Name != criticalResource.Spec.Provider.Name {
			continue
		}

		// we are looking at the correct resource, now we need to check the criteria
		messages = append(messages, a.validateAllCriteriaMet(ctx, criticalResource)...)
	}
	if len(messages) > 0 {
		return &admissionv1beta1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: strings.Join(messages, ";"),
			},
		}
	}

	// if all criteria are satisfied, the provider can be removed.
	return &admissionv1beta1.AdmissionResponse{Allowed: true}
}

func (a *CriticalResourceAdmissionWebhook) validateNamespaceDelete(ctx context.Context, request *admissionv1beta1.AdmissionRequest) *admissionv1beta1.AdmissionResponse {
	criticalResources, err := a.criticalResourceLister.CriticalResources(request.Namespace).List(labels.Everything())
	if err != nil {
		return &admissionv1beta1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	messages := []string{}
	for _, criticalResource := range criticalResources {
		if err := a.validateProviderRemoved(ctx, criticalResource); err != nil {
			messages = append(messages, err.Error())
		}
	}
	if len(messages) > 0 {
		return &admissionv1beta1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: strings.Join(messages, ";"),
			},
		}
	}

	// if all providers are removed, the namespace can be removed.
	return &admissionv1beta1.AdmissionResponse{Allowed: true}
}

func (a *CriticalResourceAdmissionWebhook) validateCriticalResourceDelete(ctx context.Context, request *admissionv1beta1.AdmissionRequest) *admissionv1beta1.AdmissionResponse {
	criticalResource, err := a.criticalResourceLister.CriticalResources(request.Namespace).Get(request.Name)
	if err != nil {
		return &admissionv1beta1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	if err := a.validateProviderRemoved(ctx, criticalResource); err != nil {
		return &admissionv1beta1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	return &admissionv1beta1.AdmissionResponse{Allowed: true}
}

func (a *CriticalResourceAdmissionWebhook) validateProviderRemoved(ctx context.Context, criticalResource *apiserverv1.CriticalResource) error {
	// TODO fix API to include version
	providerGVR := schema.GroupVersionResource{
		Group:    criticalResource.Spec.Provider.GroupResource.Group,
		Version:  "v1",
		Resource: criticalResource.Spec.Provider.GroupResource.Resource,
	}
	_, err := a.dynamicClient.Resource(providerGVR).Namespace(criticalResource.Namespace).Get(ctx, criticalResource.Spec.Provider.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// TODO we can decide about finalized or not later
	//if providerResource.GetDeletionTimestamp() != nil {
	//	return nil
	//}

	return fmt.Errorf("%v named %q in namespace/%v still exists", providerGVR, criticalResource.Spec.Provider.Name, criticalResource.Namespace)
}

func (a *CriticalResourceAdmissionWebhook) validateAllCriteriaMet(ctx context.Context, criticalResource *apiserverv1.CriticalResource) []string {
	messages := []string{}
	for _, criteria := range criticalResource.Spec.Criteria {
		if err := a.validateCriteriaMet(ctx, criticalResource.Namespace, criteria); err != nil {
			messages = append(messages, err.Error())
		}
	}

	return messages
}

func (a *CriticalResourceAdmissionWebhook) validateCriteriaMet(ctx context.Context, namespace string, criteria apiserverv1.CriticalResourceCriteria) error {
	switch criteria.Type {
	case apiserverv1.FinalizerType:
		return a.validateFinalizerCriteriaMet(ctx, criteria)
	case apiserverv1.SpecificResourceType:
		return a.validateSpecificResourceCriteriaMet(ctx, namespace, criteria)
	default:
		return fmt.Errorf("%q is not a known type", criteria.Type)
	}
}

func (a *CriticalResourceAdmissionWebhook) validateFinalizerCriteriaMet(ctx context.Context, criteria apiserverv1.CriticalResourceCriteria) error {
	// TODO fix API to include version
	gvr := schema.GroupVersionResource{
		Group:    criteria.Finalizer.Group,
		Version:  "v1",
		Resource: criteria.Finalizer.Resource,
	}
	instanceList, err := a.dynamicClient.Resource(gvr).Namespace("" /*get instances in every namespace*/).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("unable to list %v: %w", gvr, err)
	}

	numWithFinalizer := 0
	for _, instance := range instanceList.Items {
		for _, finalizer := range instance.GetFinalizers() {
			if finalizer == criteria.Finalizer.FinalizerName {
				numWithFinalizer++
				break
			}
		}
	}
	if numWithFinalizer == 0 {
		return nil
	}
	return fmt.Errorf("%d instances of %v contain finalizer", numWithFinalizer, gvr)
}

func (a *CriticalResourceAdmissionWebhook) validateSpecificResourceCriteriaMet(ctx context.Context, namespace string, criteria apiserverv1.CriticalResourceCriteria) error {
	// TODO fix API to include version
	gvr := schema.GroupVersionResource{
		Group:    criteria.SpecificResource.Group,
		Version:  "v1",
		Resource: criteria.SpecificResource.Resource,
	}
	_, err := a.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, criteria.SpecificResource.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// TODO we can decide about finalized or not later
	//if instance.GetDeletionTimestamp() != nil {
	//	return nil
	//}

	return fmt.Errorf("%v named %q in namespace/%v still exists", gvr, criteria.SpecificResource.Name, namespace)
}
