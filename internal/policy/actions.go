package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"
)

const (
	deploymentRevisionAnnotation = "deployment.kubernetes.io/revision"
	fieldManager                 = "coordination-engine"
)

// ActionExecutor runs standard repair actions in the policy namespace.
type ActionExecutor struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	mapper        meta.RESTMapper
	exec          CommandExecutor
	log           *logrus.Logger
}

// NewActionExecutor returns an executor that refuses to leave the policy namespace.
func NewActionExecutor(
	clientset kubernetes.Interface,
	dynamicClient dynamic.Interface,
	mapper meta.RESTMapper,
	exec CommandExecutor,
	log *logrus.Logger,
) (*ActionExecutor, error) {
	if clientset == nil {
		return nil, errors.New("kubernetes client is required")
	}
	if dynamicClient == nil {
		return nil, errors.New("dynamic client is required")
	}
	if mapper == nil {
		return nil, errors.New("rest mapper is required")
	}
	if log == nil {
		log = logrus.New()
	}
	return &ActionExecutor{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		mapper:        mapper,
		exec:          exec,
		log:           log,
	}, nil
}

// DiscoveryRESTMapper builds a mapper from the apiserver's discovery document.
func DiscoveryRESTMapper(client discovery.DiscoveryInterface) (meta.RESTMapper, error) {
	groupResources, err := restmapper.GetAPIGroupResources(client)
	if err != nil {
		return nil, fmt.Errorf("discover api resources: %w", err)
	}
	return restmapper.NewDiscoveryRESTMapper(groupResources), nil
}

// Run executes one repair. namespace is the policy namespace and cannot be overridden.
func (e *ActionExecutor) Run(ctx context.Context, namespace string, repair Repair) error {
	if namespace == "" {
		return errors.New("policy namespace is required")
	}
	err := e.dispatch(ctx, namespace, repair)
	if err != nil {
		return fmt.Errorf("%s: %w", repair.Action, err)
	}
	return nil
}

func (e *ActionExecutor) dispatch(ctx context.Context, namespace string, repair Repair) error {
	switch repair.Action {
	case actionRestartPod:
		return e.restartPod(ctx, namespace, repair.Params)
	case actionRollbackDeployment:
		return e.rollbackDeployment(ctx, namespace, repair.Params)
	case actionScaleDeployment:
		return e.scaleDeployment(ctx, namespace, repair.Params)
	case actionDeleteResource:
		return e.deleteResource(ctx, namespace, repair.Params)
	case actionApplyResource:
		return e.applyResource(ctx, namespace, repair.Params)
	case actionExecCommand:
		return e.execCommand(ctx, namespace, repair.Params)
	case actionPatchResource:
		return e.patchResource(ctx, namespace, repair.Params)
	default:
		return fmt.Errorf("action %q is not implemented", repair.Action)
	}
}

func (e *ActionExecutor) restartPod(ctx context.Context, namespace string, params map[string]any) error {
	name, err := stringParam(params, "deployment")
	if err != nil {
		return err
	}
	deploy, err := e.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get deployment %s/%s: %w", namespace, name, err)
	}
	if deploy.Spec.Selector == nil {
		return fmt.Errorf("deployment %s/%s has no selector", namespace, name)
	}
	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		return fmt.Errorf("deployment selector: %w", err)
	}
	pods, err := e.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return fmt.Errorf("list pods for deployment %s: %w", name, err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no pods matched deployment %s", name)
	}
	for i := range pods.Items {
		podName := pods.Items[i].Name
		if err := e.clientset.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{}); err != nil {
			return fmt.Errorf("delete pod %s/%s: %w", namespace, podName, err)
		}
	}
	return nil
}

func (e *ActionExecutor) rollbackDeployment(ctx context.Context, namespace string, params map[string]any) error {
	name, err := stringParam(params, "deployment")
	if err != nil {
		return err
	}
	deploy, err := e.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get deployment %s/%s: %w", namespace, name, err)
	}
	current, err := currentRevision(deploy)
	if err != nil {
		return err
	}
	target, err := targetRevision(params, current)
	if err != nil {
		return err
	}
	previous, err := e.replicaSetRevision(ctx, deploy, target)
	if err != nil {
		return err
	}
	updated := deploy.DeepCopy()
	updated.Spec.Template = previous.Spec.Template
	if _, err := e.clientset.AppsV1().Deployments(namespace).Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update deployment %s/%s: %w", namespace, name, err)
	}
	return nil
}

func currentRevision(deploy *appsv1.Deployment) (int, error) {
	raw := deploy.Annotations[deploymentRevisionAnnotation]
	if raw == "" {
		return 0, fmt.Errorf("deployment %s has no revision annotation", deploy.Name)
	}
	revision, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("deployment %s revision %q is invalid", deploy.Name, raw)
	}
	return revision, nil
}

func targetRevision(params map[string]any, current int) (int, error) {
	if _, ok := params["revision"]; !ok {
		if current <= 1 {
			return 0, errors.New("deployment has no previous revision")
		}
		return current - 1, nil
	}
	revision, err := numberParam(params, "revision")
	if err != nil {
		return 0, err
	}
	if revision < 1 {
		return 0, errors.New("param \"revision\" must be >= 1")
	}
	if revision == current {
		return 0, fmt.Errorf("deployment is already at revision %d", revision)
	}
	return revision, nil
}

func (e *ActionExecutor) replicaSetRevision(ctx context.Context, deploy *appsv1.Deployment, revision int) (*appsv1.ReplicaSet, error) {
	if deploy.Spec.Selector == nil {
		return nil, fmt.Errorf("deployment %s has no selector", deploy.Name)
	}
	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("deployment selector: %w", err)
	}
	list, err := e.clientset.AppsV1().ReplicaSets(deploy.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("list replica sets: %w", err)
	}
	want := strconv.Itoa(revision)
	for i := range list.Items {
		rs := &list.Items[i]
		if rs.Annotations[deploymentRevisionAnnotation] != want {
			continue
		}
		if hasOwnerUID(rs.OwnerReferences, deploy.UID) {
			return rs, nil
		}
	}
	return nil, fmt.Errorf("replica set revision %d for deployment %s was not found", revision, deploy.Name)
}

func hasOwnerUID(refs []metav1.OwnerReference, uid types.UID) bool {
	for _, ref := range refs {
		if ref.Kind == "Deployment" && ref.UID == uid {
			return true
		}
	}
	return false
}

func (e *ActionExecutor) scaleDeployment(ctx context.Context, namespace string, params map[string]any) error {
	name, err := stringParam(params, "deployment")
	if err != nil {
		return err
	}
	replicas, err := numberParam(params, "replicas")
	if err != nil {
		return err
	}
	if replicas < 0 {
		return errors.New("param \"replicas\" must be >= 0")
	}
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{"replicas": replicas},
	})
	if err != nil {
		return fmt.Errorf("encode scale patch: %w", err)
	}
	_, err = e.clientset.AppsV1().Deployments(namespace).Patch(
		ctx, name, types.MergePatchType, patch, metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("scale deployment %s/%s: %w", namespace, name, err)
	}
	return nil
}

func (e *ActionExecutor) deleteResource(ctx context.Context, namespace string, params map[string]any) error {
	apiVersion, kind, name, err := objectRef(params)
	if err != nil {
		return err
	}
	e.noteIgnoredNamespace(params, namespace, kind, name)
	gvr, err := e.namespacedResource(apiVersion, kind)
	if err != nil {
		return err
	}
	err = e.dynamicClient.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("delete %s %s/%s: %w", kind, namespace, name, err)
	}
	return nil
}

func (e *ActionExecutor) applyResource(ctx context.Context, namespace string, params map[string]any) error {
	raw, err := manifestParam(params)
	if err != nil {
		return err
	}
	obj := &unstructured.Unstructured{Object: runtime.DeepCopyJSON(raw)}
	if obj.GetNamespace() != "" && obj.GetNamespace() != namespace {
		e.log.WithFields(logrus.Fields{
			"manifest_namespace": obj.GetNamespace(),
			"policy_namespace":   namespace,
			"name":               obj.GetName(),
		}).Info("apply_resource namespace overridden by policy namespace")
	}
	obj.SetNamespace(namespace)
	gvr, err := e.namespacedResource(obj.GetAPIVersion(), obj.GetKind())
	if err != nil {
		return err
	}
	_, err = e.dynamicClient.Resource(gvr).Namespace(namespace).Apply(ctx, obj.GetName(), obj, metav1.ApplyOptions{
		FieldManager: fieldManager,
		Force:        true,
	})
	if err != nil {
		return fmt.Errorf("apply %s %s/%s: %w", obj.GetKind(), namespace, obj.GetName(), err)
	}
	return nil
}

func (e *ActionExecutor) patchResource(ctx context.Context, namespace string, params map[string]any) error {
	apiVersion, kind, name, err := objectRef(params)
	if err != nil {
		return err
	}
	patchObj, err := objectParam(params, "patch")
	if err != nil {
		return err
	}
	data, err := json.Marshal(patchObj)
	if err != nil {
		return fmt.Errorf("encode patch: %w", err)
	}
	gvr, err := e.namespacedResource(apiVersion, kind)
	if err != nil {
		return err
	}
	_, err = e.dynamicClient.Resource(gvr).Namespace(namespace).Patch(
		ctx, name, types.StrategicMergePatchType, data, metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patch %s %s/%s: %w", kind, namespace, name, err)
	}
	return nil
}

func objectRef(params map[string]any) (string, string, string, error) {
	apiVersion, err := stringParam(params, "apiVersion")
	if err != nil {
		return "", "", "", err
	}
	kind, err := stringParam(params, "kind")
	if err != nil {
		return "", "", "", err
	}
	name, err := stringParam(params, "name")
	if err != nil {
		return "", "", "", err
	}
	return apiVersion, kind, name, nil
}

func (e *ActionExecutor) noteIgnoredNamespace(params map[string]any, namespace, kind, name string) {
	hinted, ok := params["namespace"].(string)
	if !ok || hinted == "" || hinted == namespace {
		return
	}
	e.log.WithFields(logrus.Fields{
		"param_namespace":  hinted,
		"policy_namespace": namespace,
		"kind":             kind,
		"name":             name,
	}).Info("ignoring params.namespace; actions stay in the policy namespace")
}

func (e *ActionExecutor) namespacedResource(apiVersion, kind string) (schema.GroupVersionResource, error) {
	group, version, err := splitAPIVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	mapping, err := e.mapper.RESTMapping(schema.GroupKind{Group: group, Kind: kind}, version)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("map %s %s: %w", apiVersion, kind, err)
	}
	if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
		return schema.GroupVersionResource{}, fmt.Errorf("%s %s is not namespaced", apiVersion, kind)
	}
	return mapping.Resource, nil
}
