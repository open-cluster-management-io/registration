package addon

import (
	"context"
	"testing"
	"time"

	"github.com/openshift/library-go/pkg/operator/events/eventstesting"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonfake "open-cluster-management.io/api/client/addon/clientset/versioned/fake"
	addoninformers "open-cluster-management.io/api/client/addon/informers/externalversions"
	testinghelpers "open-cluster-management.io/registration/pkg/helpers/testing"
)

func TestNamespaceController(t *testing.T) {
	testcases := []struct {
		name               string
		managedClusterName string
		queueKey           string
		objects            []runtime.Object
		addons             []runtime.Object
		verify             func(t *testing.T, client *kubefake.Clientset)
	}{
		{
			name:               "The addon is not found in the managed cluster",
			managedClusterName: "cluster1",
			queueKey:           "addon1",
			addons: []runtime.Object{
				&addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "cluster2",
						Name:      "addon1",
					},
				},
			},
			verify: func(t *testing.T, client *kubefake.Clientset) {
				if len(client.Actions()) != 0 {
					t.Errorf("expected no action from client, got %v", client.Actions())
				}
			},
		},
		{
			name:               "The addon is found in the managed cluster, but installnamespace not found",
			managedClusterName: "cluster1",
			queueKey:           "addon1",
			addons: []runtime.Object{
				&addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "cluster1",
						Name:      "addon1",
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{
						InstallNamespace: "test",
					},
				},
			},
			verify: func(t *testing.T, client *kubefake.Clientset) {
				ns, err := client.CoreV1().Namespaces().Get(context.TODO(), "test", metav1.GetOptions{})
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if ns.Annotations[addonInstallNamespace] != "true" {
					t.Errorf("expected namespace to be annotated with managed cluster name, got %v", ns.Annotations)
				}
			},
		},
		{
			name:               "The addon is found in the managed cluster, and installnamespace is also found but without annotation existing",
			managedClusterName: "cluster1",
			queueKey:           "addon1",
			objects: []runtime.Object{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
				},
			},
			addons: []runtime.Object{
				&addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "cluster1",
						Name:      "addon1",
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{
						InstallNamespace: "test",
					},
				},
			},
			verify: func(t *testing.T, client *kubefake.Clientset) {
				ns, err := client.CoreV1().Namespaces().Get(context.TODO(), "test", metav1.GetOptions{})
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if ns.Annotations[addonInstallNamespace] != "true" {
					t.Errorf("expected namespace to be annotated with managed cluster name, got %v", ns.Annotations)
				}
			},
		},
		{
			name:               "The addon is found in the managed cluster, and installnamespace is also found but without annotation equals to true",
			managedClusterName: "cluster1",
			queueKey:           "addon1",
			objects: []runtime.Object{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
						Annotations: map[string]string{
							addonInstallNamespace: "false",
						},
					},
				},
			},
			addons: []runtime.Object{
				&addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "cluster1",
						Name:      "addon1",
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{
						InstallNamespace: "test",
					},
				},
			},
			verify: func(t *testing.T, client *kubefake.Clientset) {
				ns, err := client.CoreV1().Namespaces().Get(context.TODO(), "test", metav1.GetOptions{})
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if ns.Annotations[addonInstallNamespace] != "true" {
					t.Errorf("expected namespace to be annotated with managed cluster name, got %v", ns.Annotations)
				}
			},
		},
		{
			name:               "The addon is found in the managed cluster, and installnamespace is also found with annotation equals to true",
			managedClusterName: "cluster1",
			queueKey:           "addon1",
			objects: []runtime.Object{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
						Annotations: map[string]string{
							addonInstallNamespace: "true",
						},
					},
				},
			},
			addons: []runtime.Object{
				&addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "cluster1",
						Name:      "addon1",
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{
						InstallNamespace: "test",
					},
				},
			},
			verify: func(t *testing.T, client *kubefake.Clientset) {
				if len(client.Actions()) > 1 {
					t.Errorf("expected only 'get' from client, got %v", client.Actions())
				}
			},
		},
		{
			name:               "The addon is deleted, but another addon is also using the namespace",
			managedClusterName: "cluster1",
			queueKey:           "addon1",
			objects: []runtime.Object{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
						Annotations: map[string]string{
							addonInstallNamespace: "true",
						},
					},
				},
			},
			addons: []runtime.Object{
				&addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "cluster1",
						Name:      "addon1",
						DeletionTimestamp: &metav1.Time{
							Time: time.Now(),
						},
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{
						InstallNamespace: "test",
					},
				},
				&addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "cluster1",
						Name:      "addon2",
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{
						InstallNamespace: "test",
					},
				},
			},
			verify: func(t *testing.T, client *kubefake.Clientset) {
				for _, action := range client.Actions() {
					if action.GetVerb() == "delete" {
						t.Errorf("unexpected delete action: %v", action)
					}
				}
			},
		},
		{
			name:               "The addon is deleted, and no other addon is using the namespace",
			managedClusterName: "cluster1",
			queueKey:           "addon1",
			objects: []runtime.Object{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
						Annotations: map[string]string{
							addonInstallNamespace: "true",
						},
					},
				},
			},
			addons: []runtime.Object{
				&addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "cluster1",
						Name:      "addon1",
						DeletionTimestamp: &metav1.Time{
							Time: time.Now(),
						},
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{
						InstallNamespace: "test",
					},
				},
				&addonv1alpha1.ManagedClusterAddOn{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "cluster1",
						Name:      "addon2",
					},
					Spec: addonv1alpha1.ManagedClusterAddOnSpec{
						InstallNamespace: "test1",
					},
				},
			},
			verify: func(t *testing.T, client *kubefake.Clientset) {
				for _, action := range client.Actions() {
					if action.GetVerb() == "delete" {
						return
					}
				}
				t.Errorf("expected a delete action, got %v", client.Actions())
			},
		},
	}
	for _, c := range testcases {
		recorder := eventstesting.NewTestingEventRecorder(t)
		kubeClient := kubefake.NewSimpleClientset(c.objects...)
		addonClient := addonfake.NewSimpleClientset(c.addons...)
		addonInformerFactory := addoninformers.NewSharedInformerFactory(addonClient, time.Minute*10)
		addonStore := addonInformerFactory.Addon().V1alpha1().ManagedClusterAddOns().Informer().GetStore()
		for _, addon := range c.addons {
			addonStore.Add(addon)
		}

		controller := &addonNamespaceController{
			managedClusterName: c.managedClusterName,
			recorder:           recorder,
			kubeClient:         kubeClient,
			addOnLister:        addonInformerFactory.Addon().V1alpha1().ManagedClusterAddOns().Lister(),
		}

		err := controller.sync(context.TODO(), testinghelpers.NewFakeSyncContext(t, c.queueKey))
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
		}

		c.verify(t, kubeClient)
	}
}
