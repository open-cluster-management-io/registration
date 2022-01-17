package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonfake "open-cluster-management.io/api/client/addon/clientset/versioned/fake"
	clusterfake "open-cluster-management.io/api/client/cluster/clientset/versioned/fake"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	testinghelpers "open-cluster-management.io/registration/pkg/helpers/testing"

	"github.com/openshift/library-go/pkg/operator/events/eventstesting"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	fakekube "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestUpdateStatusCondition(t *testing.T) {
	nowish := metav1.Now()
	beforeish := metav1.Time{Time: nowish.Add(-10 * time.Second)}
	afterish := metav1.Time{Time: nowish.Add(10 * time.Second)}

	cases := []struct {
		name               string
		startingConditions []metav1.Condition
		newCondition       metav1.Condition
		expextedUpdated    bool
		expectedConditions []metav1.Condition
	}{
		{
			name:               "add to empty",
			startingConditions: []metav1.Condition{},
			newCondition:       testinghelpers.NewManagedClusterCondition("test", "True", "my-reason", "my-message", nil),
			expextedUpdated:    true,
			expectedConditions: []metav1.Condition{testinghelpers.NewManagedClusterCondition("test", "True", "my-reason", "my-message", nil)},
		},
		{
			name: "add to non-conflicting",
			startingConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
			},
			newCondition:    testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", nil),
			expextedUpdated: true,
			expectedConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
				testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", nil),
			},
		},
		{
			name: "change existing status",
			startingConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
				testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", nil),
			},
			newCondition:    testinghelpers.NewManagedClusterCondition("one", "False", "my-different-reason", "my-othermessage", nil),
			expextedUpdated: true,
			expectedConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
				testinghelpers.NewManagedClusterCondition("one", "False", "my-different-reason", "my-othermessage", nil),
			},
		},
		{
			name: "leave existing transition time",
			startingConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
				testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", &beforeish),
			},
			newCondition:    testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", &afterish),
			expextedUpdated: false,
			expectedConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
				testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", &beforeish),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeClusterClient := clusterfake.NewSimpleClientset(&clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "testspokecluster"},
				Status: clusterv1.ManagedClusterStatus{
					Conditions: c.startingConditions,
				},
			})

			status, updated, err := UpdateManagedClusterStatus(
				context.TODO(),
				fakeClusterClient,
				"testspokecluster",
				UpdateManagedClusterConditionFn(c.newCondition),
			)
			if err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if updated != c.expextedUpdated {
				t.Errorf("expected %t, but %t", c.expextedUpdated, updated)
			}
			for i := range c.expectedConditions {
				expected := c.expectedConditions[i]
				actual := status.Conditions[i]
				if expected.LastTransitionTime == (metav1.Time{}) {
					actual.LastTransitionTime = metav1.Time{}
				}
				if !equality.Semantic.DeepEqual(expected, actual) {
					t.Errorf(diff.ObjectDiff(expected, actual))
				}
			}
		})
	}
}

func TestUpdateManagedClusterAddOnStatus(t *testing.T) {
	nowish := metav1.Now()
	beforeish := metav1.Time{Time: nowish.Add(-10 * time.Second)}
	afterish := metav1.Time{Time: nowish.Add(10 * time.Second)}

	cases := []struct {
		name               string
		startingConditions []metav1.Condition
		newCondition       metav1.Condition
		expextedUpdated    bool
		expectedConditions []metav1.Condition
	}{
		{
			name:               "add to empty",
			startingConditions: []metav1.Condition{},
			newCondition:       testinghelpers.NewManagedClusterCondition("test", "True", "my-reason", "my-message", nil),
			expextedUpdated:    true,
			expectedConditions: []metav1.Condition{testinghelpers.NewManagedClusterCondition("test", "True", "my-reason", "my-message", nil)},
		},
		{
			name: "add to non-conflicting",
			startingConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
			},
			newCondition:    testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", nil),
			expextedUpdated: true,
			expectedConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
				testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", nil),
			},
		},
		{
			name: "change existing status",
			startingConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
				testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", nil),
			},
			newCondition:    testinghelpers.NewManagedClusterCondition("one", "False", "my-different-reason", "my-othermessage", nil),
			expextedUpdated: true,
			expectedConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
				testinghelpers.NewManagedClusterCondition("one", "False", "my-different-reason", "my-othermessage", nil),
			},
		},
		{
			name: "leave existing transition time",
			startingConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
				testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", &beforeish),
			},
			newCondition:    testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", &afterish),
			expextedUpdated: false,
			expectedConditions: []metav1.Condition{
				testinghelpers.NewManagedClusterCondition("two", "True", "my-reason", "my-message", nil),
				testinghelpers.NewManagedClusterCondition("one", "True", "my-reason", "my-message", &beforeish),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeAddOnClient := addonfake.NewSimpleClientset(&addonv1alpha1.ManagedClusterAddOn{
				ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "test"},
				Status: addonv1alpha1.ManagedClusterAddOnStatus{
					Conditions: c.startingConditions,
				},
			})

			status, updated, err := UpdateManagedClusterAddOnStatus(
				context.TODO(),
				fakeAddOnClient,
				"test", "test",
				UpdateManagedClusterAddOnStatusFn(c.newCondition),
			)
			if err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if updated != c.expextedUpdated {
				t.Errorf("expected %t, but %t", c.expextedUpdated, updated)
			}
			for i := range c.expectedConditions {
				expected := c.expectedConditions[i]
				actual := status.Conditions[i]
				if expected.LastTransitionTime == (metav1.Time{}) {
					actual.LastTransitionTime = metav1.Time{}
				}
				if !equality.Semantic.DeepEqual(expected, actual) {
					t.Errorf(diff.ObjectDiff(expected, actual))
				}
			}
		})
	}
}

func TestIsValidHTTPSURL(t *testing.T) {
	cases := []struct {
		name      string
		serverURL string
		isValid   bool
	}{
		{
			name:      "an empty url",
			serverURL: "",
			isValid:   false,
		},
		{
			name:      "an invalid url",
			serverURL: "/path/path/path",
			isValid:   false,
		},
		{
			name:      "a http url",
			serverURL: "http://127.0.0.1:8080",
			isValid:   false,
		},
		{
			name:      "a https url",
			serverURL: "https://127.0.0.1:6443",
			isValid:   true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isValid := IsValidHTTPSURL(c.serverURL)
			if isValid != c.isValid {
				t.Errorf("expected %t, but %t", c.isValid, isValid)
			}
		})
	}
}

func TestCleanUpManagedClusterManifests(t *testing.T) {
	applyFiles := map[string]runtime.Object{
		"namespace":          testinghelpers.NewUnstructuredObj("v1", "Namespace", "", "n1"),
		"clusterrole":        testinghelpers.NewUnstructuredObj("rbac.authorization.k8s.io/v1", "ClusterRole", "", "cr1"),
		"clusterrolebinding": testinghelpers.NewUnstructuredObj("rbac.authorization.k8s.io/v1", "ClusterRoleBinding", "", "crb1"),
		"role":               testinghelpers.NewUnstructuredObj("rbac.authorization.k8s.io/v1", "Role", "n1", "r1"),
		"rolebinding":        testinghelpers.NewUnstructuredObj("rbac.authorization.k8s.io/v1", "RoleBinding", "n1", "rb1"),
	}
	expectedActions := []string{}
	for i := 0; i < len(applyFiles); i++ {
		expectedActions = append(expectedActions, "delete")
	}
	cases := []struct {
		name            string
		applyObject     []runtime.Object
		applyFiles      map[string]runtime.Object
		validateActions func(t *testing.T, actions []clienttesting.Action)
		expectedErr     string
	}{
		{
			name: "delete applied objects",
			applyObject: []runtime.Object{
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
				&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "cr1"}},
				&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "crb1"}},
				&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "r1", Namespace: "n1"}},
				&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "rb1", Namespace: "n1"}},
			},
			applyFiles: applyFiles,
			validateActions: func(t *testing.T, actions []clienttesting.Action) {
				testinghelpers.AssertActions(t, actions, expectedActions...)
			},
		},
		{
			name:        "there are no applied objects",
			applyObject: []runtime.Object{},
			applyFiles:  applyFiles,
			validateActions: func(t *testing.T, actions []clienttesting.Action) {
				testinghelpers.AssertActions(t, actions, expectedActions...)
			},
		},
		{
			name:            "unhandled types",
			applyObject:     []runtime.Object{},
			applyFiles:      map[string]runtime.Object{"secret": testinghelpers.NewUnstructuredObj("v1", "Secret", "n1", "s1")},
			expectedErr:     "unhandled type *v1.Secret",
			validateActions: testinghelpers.AssertNoActions,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kubeClient := fakekube.NewSimpleClientset(c.applyObject...)
			cleanUpErr := CleanUpManagedClusterManifests(
				context.TODO(),
				kubeClient,
				eventstesting.NewTestingEventRecorder(t),
				func(name string) ([]byte, error) {
					if c.applyFiles[name] == nil {
						return nil, fmt.Errorf("Failed to find file")
					}
					return json.Marshal(c.applyFiles[name])
				},
				getApplyFileNames(c.applyFiles)...,
			)
			testinghelpers.AssertError(t, cleanUpErr, c.expectedErr)
			c.validateActions(t, kubeClient.Actions())
		})
	}
}

func TestFindTaintByKey(t *testing.T) {
	cases := []struct {
		name     string
		cluster  *clusterv1.ManagedCluster
		key      string
		expected *clusterv1.Taint
	}{
		{
			name: "nil of managed cluster",
			key:  "taint1",
		},
		{
			name: "taint found",
			cluster: &clusterv1.ManagedCluster{
				Spec: clusterv1.ManagedClusterSpec{
					Taints: []clusterv1.Taint{
						{
							Key:   "taint1",
							Value: "value1",
						},
					},
				},
			},
			key: "taint1",
			expected: &clusterv1.Taint{
				Key:   "taint1",
				Value: "value1",
			},
		},
		{
			name: "taint not found",
			cluster: &clusterv1.ManagedCluster{
				Spec: clusterv1.ManagedClusterSpec{
					Taints: []clusterv1.Taint{
						{
							Key:   "taint1",
							Value: "value1",
						},
					},
				},
			},
			key: "taint2",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			actual := FindTaintByKey(c.cluster, c.key)
			if !reflect.DeepEqual(actual, c.expected) {
				t.Errorf("expected %v but got %v", c.expected, actual)
			}
		})
	}
}

func getApplyFileNames(applyFiles map[string]runtime.Object) []string {
	keys := []string{}
	for key := range applyFiles {
		keys = append(keys, key)
	}
	return keys
}
