package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestActionExecutor_RestartDeletesOnlyPolicyNamespace(t *testing.T) {
	client := k8sfake.NewSimpleClientset(
		deployment("parasol-insurance", "ic-app", "2", "ic-app:v2"),
		pod("parasol-insurance", "ic-app-aaa", "ic-app"),
		pod("parasol-insurance", "other-app", "other"),
		pod("other-ns", "ic-app-bbb", "ic-app"),
	)
	executor := testExecutor(t, client, nil)

	err := executor.Run(context.Background(), "parasol-insurance", Repair{
		Action: actionRestartPod,
		Params: map[string]any{"deployment": "ic-app"},
	})
	require.NoError(t, err)

	_, err = client.CoreV1().Pods("parasol-insurance").Get(context.Background(), "ic-app-aaa", metav1.GetOptions{})
	require.Error(t, err)
	_, err = client.CoreV1().Pods("parasol-insurance").Get(context.Background(), "other-app", metav1.GetOptions{})
	require.NoError(t, err)
	_, err = client.CoreV1().Pods("other-ns").Get(context.Background(), "ic-app-bbb", metav1.GetOptions{})
	require.NoError(t, err)
}

func TestActionExecutor_RollbackToPreviousRevision(t *testing.T) {
	deploy := deployment("checkout", "checkout-api", "2", "checkout:v2")
	client := k8sfake.NewSimpleClientset(
		deploy,
		replicaSet("checkout-api-v1", "1", deploy.UID, "checkout:v1"),
		replicaSet("checkout-api-v2", "2", deploy.UID, "checkout:v2"),
	)
	executor := testExecutor(t, client, nil)

	err := executor.Run(context.Background(), "checkout", Repair{
		Action: actionRollbackDeployment,
		Params: map[string]any{"deployment": "checkout-api"},
	})
	require.NoError(t, err)

	updated, err := client.AppsV1().Deployments("checkout").Get(context.Background(), "checkout-api", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "checkout:v1", updated.Spec.Template.Spec.Containers[0].Image)
}

func TestActionExecutor_RollbackToSpecifiedRevision(t *testing.T) {
	deploy := deployment("checkout", "checkout-api", "3", "checkout:v3")
	client := k8sfake.NewSimpleClientset(
		deploy,
		replicaSet("checkout-api-v1", "1", deploy.UID, "checkout:v1"),
		replicaSet("checkout-api-v2", "2", deploy.UID, "checkout:v2"),
	)
	executor := testExecutor(t, client, nil)

	err := executor.Run(context.Background(), "checkout", Repair{
		Action: actionRollbackDeployment,
		Params: map[string]any{"deployment": "checkout-api", "revision": float64(1)},
	})
	require.NoError(t, err)

	updated, err := client.AppsV1().Deployments("checkout").Get(context.Background(), "checkout-api", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "checkout:v1", updated.Spec.Template.Spec.Containers[0].Image)
}

func TestActionExecutor_ScaleStaysInPolicyNamespace(t *testing.T) {
	client := k8sfake.NewSimpleClientset(
		deployment("checkout", "checkout-api", "1", "checkout:v1"),
		deployment("other-ns", "checkout-api", "1", "checkout:v1"),
	)
	executor := testExecutor(t, client, nil)

	err := executor.Run(context.Background(), "checkout", Repair{
		Action: actionScaleDeployment,
		Params: map[string]any{"deployment": "checkout-api", "replicas": float64(3)},
	})
	require.NoError(t, err)

	scaled, err := client.AppsV1().Deployments("checkout").Get(context.Background(), "checkout-api", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, int32(3), *scaled.Spec.Replicas)

	untouched, err := client.AppsV1().Deployments("other-ns").Get(context.Background(), "checkout-api", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, int32(1), *untouched.Spec.Replicas)
}

func TestActionExecutor_DeleteIgnoresParamNamespace(t *testing.T) {
	inPolicy := configMap("checkout", "chaos-block-db-access")
	other := configMap("other-ns", "chaos-block-db-access")
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), inPolicy, other)
	executor := testExecutorWithDynamic(t, k8sfake.NewSimpleClientset(), dyn, nil)

	err := executor.Run(context.Background(), "checkout", Repair{
		Action: actionDeleteResource,
		Params: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"name":       "chaos-block-db-access",
			"namespace":  "other-ns",
		},
	})
	require.NoError(t, err)

	_, err = dyn.Resource(configMapGVR).Namespace("checkout").Get(context.Background(), "chaos-block-db-access", metav1.GetOptions{})
	require.Error(t, err)
	_, err = dyn.Resource(configMapGVR).Namespace("other-ns").Get(context.Background(), "chaos-block-db-access", metav1.GetOptions{})
	require.NoError(t, err)
}

func TestActionExecutor_ApplyForcesPolicyNamespace(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	// The fake client's server-side apply cannot create unstructured objects.
	// Record the apply call and store the object the executor submitted.
	dyn.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		patch, ok := action.(clienttesting.PatchAction)
		if !ok || patch.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(patch.GetPatch()); err != nil {
			return true, nil, fmt.Errorf("decode apply patch: %w", err)
		}
		obj.SetNamespace(patch.GetNamespace())
		if err := dyn.Tracker().Add(obj); err != nil {
			return true, nil, fmt.Errorf("store applied object: %w", err)
		}
		return true, obj, nil
	})
	executor := testExecutorWithDynamic(t, k8sfake.NewSimpleClientset(), dyn, nil)

	err := executor.Run(context.Background(), "checkout", Repair{
		Action: actionApplyResource,
		Params: map[string]any{
			"manifest": map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":      "app-config",
					"namespace": "other-ns",
				},
				"data": map[string]any{"mode": "safe"},
			},
		},
	})
	require.NoError(t, err)

	got, err := dyn.Resource(configMapGVR).Namespace("checkout").Get(context.Background(), "app-config", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "checkout", got.GetNamespace())
	_, err = dyn.Resource(configMapGVR).Namespace("other-ns").Get(context.Background(), "app-config", metav1.GetOptions{})
	require.Error(t, err)
}

func TestActionExecutor_PatchUsesStrategicMergeInPolicyNamespace(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), configMap("checkout", "app-config"))
	var patched clienttesting.PatchAction
	dyn.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		patch, ok := action.(clienttesting.PatchAction)
		require.True(t, ok)
		patched = patch
		return true, configMap("checkout", "app-config"), nil
	})
	executor := testExecutorWithDynamic(t, k8sfake.NewSimpleClientset(), dyn, nil)

	err := executor.Run(context.Background(), "checkout", Repair{
		Action: actionPatchResource,
		Params: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"name":       "app-config",
			"patch":      map[string]any{"data": map[string]any{"mode": "safe"}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "checkout", patched.GetNamespace())
	require.Equal(t, "app-config", patched.GetName())
	require.Equal(t, "application/strategic-merge-patch+json", string(patched.GetPatchType()))
	require.JSONEq(t, `{"metadata":{"resourceVersion":""},"data":{"mode":"safe"}}`, string(patched.GetPatch()))
}

func TestActionExecutor_ExecUsesArgvInPolicyNamespace(t *testing.T) {
	client := k8sfake.NewSimpleClientset(
		podWithContainer("checkout", "pg-b", "postgresql", "postgres"),
		podWithContainer("checkout", "pg-a", "postgresql", "postgres"),
		podWithContainer("other-ns", "pg-a", "postgresql", "postgres"),
	)
	recorder := &recordingExec{}
	executor := testExecutor(t, client, recorder)

	err := executor.Run(context.Background(), "checkout", Repair{
		Action: actionExecCommand,
		Params: map[string]any{
			"podSelector": "app=postgresql",
			"command":     []any{"rm", "/var/lib/pgsql/data/chaos-fill"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "checkout", recorder.namespace)
	require.Equal(t, "pg-a", recorder.pod)
	require.Equal(t, "postgres", recorder.container)
	require.Equal(t, []string{"rm", "/var/lib/pgsql/data/chaos-fill"}, recorder.command)
}

func TestActionExecutor_UnknownActionDoesNotCallKubernetes(t *testing.T) {
	client := k8sfake.NewSimpleClientset(pod("checkout", "keep", "checkout-api"))
	executor := testExecutor(t, client, nil)

	err := executor.Run(context.Background(), "checkout", Repair{
		Action: "drain_node",
		Params: map[string]any{},
	})
	require.Error(t, err)
	_, err = client.CoreV1().Pods("checkout").Get(context.Background(), "keep", metav1.GetOptions{})
	require.NoError(t, err)
}

type recordingExec struct {
	namespace string
	pod       string
	container string
	command   []string
}

func (r *recordingExec) Exec(_ context.Context, namespace, pod, container string, command []string) error {
	r.namespace = namespace
	r.pod = pod
	r.container = container
	r.command = append([]string(nil), command...)
	return nil
}

func testExecutor(t *testing.T, client *k8sfake.Clientset, exec CommandExecutor) *ActionExecutor {
	t.Helper()
	return testExecutorWithDynamic(t, client, dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), exec)
}

func testExecutorWithDynamic(t *testing.T, client *k8sfake.Clientset, dyn *dynamicfake.FakeDynamicClient, exec CommandExecutor) *ActionExecutor {
	t.Helper()
	installRevisionReactor(client, client.Tracker())
	installRevisionReactor(dyn, dyn.Tracker())
	executor, err := NewActionExecutor(client, dyn, testRESTMapper(), exec, discardLogger())
	require.NoError(t, err)
	return executor
}

func TestActionExecutor_StaleRevisionAbortsBeforeWrite(t *testing.T) {
	t.Run("restart_pod", func(t *testing.T) {
		client := k8sfake.NewSimpleClientset(
			deployment("checkout", "checkout-api", "1", "checkout:v1"),
			pod("checkout", "checkout-api-aaa", "checkout-api"),
		)
		executor := testExecutor(t, client, nil)
		plan := mustPlan(t, executor, "checkout", Repair{
			Action: actionRestartPod,
			Params: map[string]any{"deployment": "checkout-api"},
		})
		bumpRevision(t, client.Tracker(), podGVR, "checkout", "checkout-api-aaa")
		err := executor.Execute(context.Background(), &plan)
		requireAborted(t, err)
		require.Zero(t, writeActions(client.Actions()))
		_, err = client.CoreV1().Pods("checkout").Get(context.Background(), "checkout-api-aaa", metav1.GetOptions{})
		require.NoError(t, err)
	})

	t.Run("rollback_deployment", func(t *testing.T) {
		deploy := deployment("checkout", "checkout-api", "2", "checkout:v2")
		client := k8sfake.NewSimpleClientset(
			deploy,
			replicaSet("checkout-api-v1", "1", deploy.UID, "checkout:v1"),
			replicaSet("checkout-api-v2", "2", deploy.UID, "checkout:v2"),
		)
		executor := testExecutor(t, client, nil)
		plan := mustPlan(t, executor, "checkout", Repair{
			Action: actionRollbackDeployment,
			Params: map[string]any{"deployment": "checkout-api"},
		})
		bumpRevision(t, client.Tracker(), deploymentGVR, "checkout", "checkout-api")
		err := executor.Execute(context.Background(), &plan)
		requireAborted(t, err)
		require.Zero(t, writeActions(client.Actions()))
		updated, err := client.AppsV1().Deployments("checkout").Get(context.Background(), "checkout-api", metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, "checkout:v2", updated.Spec.Template.Spec.Containers[0].Image)
	})

	t.Run("scale_deployment", func(t *testing.T) {
		client := k8sfake.NewSimpleClientset(deployment("checkout", "checkout-api", "1", "checkout:v1"))
		executor := testExecutor(t, client, nil)
		plan := mustPlan(t, executor, "checkout", Repair{
			Action: actionScaleDeployment,
			Params: map[string]any{"deployment": "checkout-api", "replicas": float64(3)},
		})
		bumpRevision(t, client.Tracker(), deploymentGVR, "checkout", "checkout-api")
		err := executor.Execute(context.Background(), &plan)
		requireAborted(t, err)
		require.Zero(t, writeActions(client.Actions()))
		scaled, err := client.AppsV1().Deployments("checkout").Get(context.Background(), "checkout-api", metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, int32(1), *scaled.Spec.Replicas)
	})

	t.Run("delete_resource", func(t *testing.T) {
		dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), configMap("checkout", "app-config"))
		executor := testExecutorWithDynamic(t, k8sfake.NewSimpleClientset(), dyn, nil)
		plan := mustPlan(t, executor, "checkout", Repair{
			Action: actionDeleteResource,
			Params: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "name": "app-config"},
		})
		bumpRevision(t, dyn.Tracker(), configMapGVR, "checkout", "app-config")
		err := executor.Execute(context.Background(), &plan)
		requireAborted(t, err)
		require.Zero(t, writeActions(dyn.Actions()))
		_, err = dyn.Resource(configMapGVR).Namespace("checkout").Get(context.Background(), "app-config", metav1.GetOptions{})
		require.NoError(t, err)
	})

	t.Run("apply_resource", func(t *testing.T) {
		existing := configMap("checkout", "app-config")
		existing.Object["data"] = map[string]any{"mode": "old"}
		dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), existing)
		executor := testExecutorWithDynamic(t, k8sfake.NewSimpleClientset(), dyn, nil)
		plan := mustPlan(t, executor, "checkout", Repair{
			Action: actionApplyResource,
			Params: map[string]any{"manifest": map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]any{"name": "app-config"},
				"data":       map[string]any{"mode": "new"},
			}},
		})
		bumpRevision(t, dyn.Tracker(), configMapGVR, "checkout", "app-config")
		err := executor.Execute(context.Background(), &plan)
		requireAborted(t, err)
		require.Zero(t, writeActions(dyn.Actions()))
		got, err := dyn.Resource(configMapGVR).Namespace("checkout").Get(context.Background(), "app-config", metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, "old", got.Object["data"].(map[string]any)["mode"])
	})

	t.Run("patch_resource", func(t *testing.T) {
		existing := configMap("checkout", "app-config")
		existing.Object["data"] = map[string]any{"mode": "old"}
		dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), existing)
		executor := testExecutorWithDynamic(t, k8sfake.NewSimpleClientset(), dyn, nil)
		plan := mustPlan(t, executor, "checkout", Repair{
			Action: actionPatchResource,
			Params: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"name":       "app-config",
				"patch":      map[string]any{"data": map[string]any{"mode": "new"}},
			},
		})
		bumpRevision(t, dyn.Tracker(), configMapGVR, "checkout", "app-config")
		err := executor.Execute(context.Background(), &plan)
		requireAborted(t, err)
		require.Zero(t, writeActions(dyn.Actions()))
		got, err := dyn.Resource(configMapGVR).Namespace("checkout").Get(context.Background(), "app-config", metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, "old", got.Object["data"].(map[string]any)["mode"])
	})
}

func TestActionExecutor_AdmissionRejectionStopsBeforeWrite(t *testing.T) {
	client := k8sfake.NewSimpleClientset(deployment("checkout", "checkout-api", "1", "checkout:v1"))
	executor := testExecutor(t, client, nil)
	client.PrependReactor("*", "deployments", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if !isWriteAction(action) {
			return false, nil, nil
		}
		if actionDryRun(action) {
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Group: "apps", Resource: "deployments"},
				"checkout-api",
				errors.New("admission denied"),
			)
		}
		return true, nil, errors.New("real write after admission rejection")
	})

	err := executor.Run(context.Background(), "checkout", Repair{
		Action: actionScaleDeployment,
		Params: map[string]any{"deployment": "checkout-api", "replicas": float64(4)},
	})
	require.Error(t, err)
	require.False(t, errors.Is(err, errAborted))
	require.Contains(t, err.Error(), "admission denied")
	scaled, err := client.AppsV1().Deployments("checkout").Get(context.Background(), "checkout-api", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, int32(1), *scaled.Spec.Replicas)
}

func TestActionExecutor_ExecIgnoresPodRevision(t *testing.T) {
	client := k8sfake.NewSimpleClientset(podWithContainer("checkout", "pg-a", "postgresql", "postgres"))
	recorder := &recordingExec{}
	executor := testExecutor(t, client, recorder)
	plan := mustPlan(t, executor, "checkout", Repair{
		Action: actionExecCommand,
		Params: map[string]any{
			"podSelector": "app=postgresql",
			"command":     []any{"rm", "/var/lib/pgsql/data/chaos-fill"},
		},
	})
	bumpRevision(t, client.Tracker(), podGVR, "checkout", "pg-a")

	err := executor.Execute(context.Background(), &plan)
	require.NoError(t, err)
	require.Equal(t, "pg-a", recorder.pod)
	require.Equal(t, []string{"rm", "/var/lib/pgsql/data/chaos-fill"}, recorder.command)
	require.Zero(t, writeActions(client.Actions()))
}

var configMapGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

func testRESTMapper() meta.RESTMapper {
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}})
	mapper.AddSpecific(
		schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
		configMapGVR,
		schema.GroupVersionResource{Version: "v1", Resource: "configmap"},
		meta.RESTScopeNamespace,
	)
	return mapper
}

func deployment(namespace, name, revision, image string) *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			UID:         types.UID("uid-" + namespace + "-" + name),
			Annotations: map[string]string{deploymentRevisionAnnotation: revision},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: image}},
				},
			},
		},
	}
}

func replicaSet(name, revision string, owner types.UID, image string) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "checkout",
			Labels:      map[string]string{"app": "checkout-api"},
			Annotations: map[string]string{deploymentRevisionAnnotation: revision},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "checkout-api",
				UID:        owner,
			}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: image}},
				},
			},
		},
	}
}

func pod(namespace, name, app string) *corev1.Pod {
	return podWithContainer(namespace, name, app, "app")
}

func podWithContainer(namespace, name, app, container string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": app},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: container, Image: "app:v1"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func configMap(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
	}}
}

var (
	podGVR        = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	deploymentGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
)

// revisionGate stands in for API-server optimistic concurrency. The fake client
// stores dry-run writes and ignores preconditions. Typed writes expose DryRun;
// the dynamic fake drops options, so its first write is the dry-run.
type revisionGate struct {
	tracker clienttesting.ObjectTracker
	dynamic map[string]int
}

func installRevisionReactor(fake interface {
	PrependReactor(verb, resource string, reaction clienttesting.ReactionFunc)
}, tracker clienttesting.ObjectTracker) {
	gate := &revisionGate{tracker: tracker, dynamic: map[string]int{}}
	fake.PrependReactor("*", "*", gate.react)
}

func (g *revisionGate) react(action clienttesting.Action) (bool, runtime.Object, error) {
	if !isWriteAction(action) {
		return false, nil, nil
	}
	if typedWrite(action) {
		return g.reactTyped(action)
	}
	return g.reactDynamic(action)
}

func (g *revisionGate) reactTyped(action clienttesting.Action) (bool, runtime.Object, error) {
	if actionDryRun(action) {
		return true, nil, nil
	}
	if err := g.conflict(action); err != nil {
		return true, nil, err
	}
	return false, nil, nil
}

func (g *revisionGate) reactDynamic(action clienttesting.Action) (bool, runtime.Object, error) {
	key := action.GetVerb() + " " + action.GetResource().Resource + " " + action.GetNamespace() + "/" + actionName(action)
	g.dynamic[key]++
	if g.dynamic[key] == 1 {
		return true, nil, nil
	}
	if err := g.conflict(action); err != nil {
		return true, nil, err
	}
	return false, nil, nil
}

func (g *revisionGate) conflict(action clienttesting.Action) error {
	uid, resourceVersion, ok := requestedRevision(action)
	if !ok {
		return nil
	}
	name := actionName(action)
	live, err := g.tracker.Get(action.GetResource(), action.GetNamespace(), name)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	accessor, err := meta.Accessor(live)
	if err != nil {
		return fmt.Errorf("read %s metadata: %w", name, err)
	}
	uidChanged := uid != "" && accessor.GetUID() != uid
	if uidChanged || accessor.GetResourceVersion() != resourceVersion {
		return fmt.Errorf("stale %s: %w", name, apierrors.NewConflict(
			schema.GroupResource{Resource: action.GetResource().Resource},
			name,
			errors.New("stale uid or resourceVersion"),
		))
	}
	return nil
}

func typedWrite(action clienttesting.Action) bool {
	switch action.GetResource().Resource {
	case "pods", "deployments":
		return true
	default:
		return false
	}
}

func isWriteAction(action clienttesting.Action) bool {
	switch action.GetVerb() {
	case "update", "patch", "delete":
		return true
	default:
		return false
	}
}

func actionDryRun(action clienttesting.Action) bool {
	switch typed := action.(type) {
	case clienttesting.UpdateActionImpl:
		return len(typed.GetUpdateOptions().DryRun) > 0
	case clienttesting.PatchActionImpl:
		return len(typed.GetPatchOptions().DryRun) > 0
	case clienttesting.DeleteActionImpl:
		return len(typed.GetDeleteOptions().DryRun) > 0
	default:
		return false
	}
}

func requestedRevision(action clienttesting.Action) (types.UID, string, bool) {
	switch typed := action.(type) {
	case clienttesting.UpdateActionImpl:
		accessor, err := meta.Accessor(typed.GetObject())
		if err != nil {
			return "", "", false
		}
		return accessor.GetUID(), accessor.GetResourceVersion(), true
	case clienttesting.DeleteActionImpl:
		pre := typed.GetDeleteOptions().Preconditions
		if pre == nil || (pre.UID == nil && pre.ResourceVersion == nil) {
			return "", "", false
		}
		var uid types.UID
		var resourceVersion string
		if pre.UID != nil {
			uid = *pre.UID
		}
		if pre.ResourceVersion != nil {
			resourceVersion = *pre.ResourceVersion
		}
		return uid, resourceVersion, true
	case clienttesting.PatchActionImpl:
		var body map[string]any
		if err := json.Unmarshal(typed.GetPatch(), &body); err != nil {
			return "", "", false
		}
		objectMeta, ok := body["metadata"].(map[string]any)
		if !ok {
			return "", "", false
		}
		resourceVersion, hasVersion := objectMeta["resourceVersion"].(string)
		rawUID, hasUID := objectMeta["uid"].(string)
		if !hasVersion && !hasUID {
			return "", "", false
		}
		return types.UID(rawUID), resourceVersion, true
	default:
		return "", "", false
	}
}

func actionName(action clienttesting.Action) string {
	switch typed := action.(type) {
	case clienttesting.PatchAction:
		return typed.GetName()
	case clienttesting.DeleteAction:
		return typed.GetName()
	case clienttesting.UpdateAction:
		accessor, err := meta.Accessor(typed.GetObject())
		if err != nil {
			return ""
		}
		return accessor.GetName()
	default:
		return ""
	}
}

func writeActions(actions []clienttesting.Action) int {
	count := 0
	for _, action := range actions {
		if isWriteAction(action) {
			count++
		}
	}
	return count
}

func mustPlan(t *testing.T, executor *ActionExecutor, namespace string, repair Repair) ActionPlan { //nolint:unparam // namespace kept for reuse across tests
	t.Helper()
	plan, err := executor.Plan(context.Background(), namespace, repair)
	require.NoError(t, err)
	return plan
}

func requireAborted(t *testing.T, err error) {
	t.Helper()
	require.ErrorIs(t, err, errAborted)
	require.True(t, apierrors.IsConflict(err))
}

func bumpRevision(t *testing.T, tracker clienttesting.ObjectTracker, gvr schema.GroupVersionResource, namespace, name string) { //nolint:unparam // namespace kept for reuse across tests
	t.Helper()
	obj, err := tracker.Get(gvr, namespace, name)
	require.NoError(t, err)
	cloned := obj.DeepCopyObject()
	accessor, err := meta.Accessor(cloned)
	require.NoError(t, err)
	accessor.SetResourceVersion("2")
	require.NoError(t, tracker.Update(gvr, cloned, namespace))
}
