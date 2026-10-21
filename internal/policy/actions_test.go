package policy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
		pod("parasol-insurance", "ic-app-aaa", "ic-app", corev1.PodRunning),
		pod("parasol-insurance", "other-app", "other", corev1.PodRunning),
		pod("other-ns", "ic-app-bbb", "ic-app", corev1.PodRunning),
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
		replicaSet("checkout", "checkout-api-v1", "checkout-api", "1", deploy.UID, "checkout:v1"),
		replicaSet("checkout", "checkout-api-v2", "checkout-api", "2", deploy.UID, "checkout:v2"),
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
		replicaSet("checkout", "checkout-api-v1", "checkout-api", "1", deploy.UID, "checkout:v1"),
		replicaSet("checkout", "checkout-api-v2", "checkout-api", "2", deploy.UID, "checkout:v2"),
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
			return true, nil, err
		}
		obj.SetNamespace(patch.GetNamespace())
		if err := dyn.Tracker().Add(obj); err != nil {
			return true, nil, err
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
	require.JSONEq(t, `{"data":{"mode":"safe"}}`, string(patched.GetPatch()))
}

func TestActionExecutor_ExecUsesArgvInPolicyNamespace(t *testing.T) {
	client := k8sfake.NewSimpleClientset(
		podWithContainer("checkout", "pg-b", "postgresql", "postgres", corev1.PodRunning),
		podWithContainer("checkout", "pg-a", "postgresql", "postgres", corev1.PodRunning),
		podWithContainer("other-ns", "pg-a", "postgresql", "postgres", corev1.PodRunning),
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
	client := k8sfake.NewSimpleClientset(pod("checkout", "keep", "checkout-api", corev1.PodRunning))
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
	executor, err := NewActionExecutor(client, dyn, testRESTMapper(), exec, discardLogger())
	require.NoError(t, err)
	return executor
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

func replicaSet(namespace, name, app, revision string, owner types.UID, image string) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      map[string]string{"app": app},
			Annotations: map[string]string{deploymentRevisionAnnotation: revision},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       app,
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

func pod(namespace, name, app string, phase corev1.PodPhase) *corev1.Pod {
	return podWithContainer(namespace, name, app, "app", phase)
}

func podWithContainer(namespace, name, app, container string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": app},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: container, Image: "app:v1"}},
		},
		Status: corev1.PodStatus{Phase: phase},
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
