package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// errAborted marks a repair that was not applied because its target changed.
var errAborted = errors.New("aborted")

// ObjectRevision is the UID and resourceVersion of one write target.
type ObjectRevision struct {
	APIVersion      string
	Kind            string
	Namespace       string
	Name            string
	UID             types.UID
	ResourceVersion string
}

// ActionPlan is a repair plus the target versions it may write.
type ActionPlan struct {
	Namespace string
	Repair    Repair
	Targets   []ObjectRevision
}

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

// Run plans a repair and executes it. namespace is the policy namespace and cannot be overridden.
func (e *ActionExecutor) Run(ctx context.Context, namespace string, repair Repair) error {
	plan, err := e.Plan(ctx, namespace, repair)
	if err != nil {
		return fmt.Errorf("%s: %w", repair.Action, err)
	}
	if err := e.Execute(ctx, &plan); err != nil {
		return fmt.Errorf("%s: %w", repair.Action, err)
	}
	return nil
}

// Plan reads each write target and records its UID and resourceVersion.
// exec_command has no write target because pods/exec has no dry-run or preconditions.
func (e *ActionExecutor) Plan(ctx context.Context, namespace string, repair Repair) (ActionPlan, error) {
	if namespace == "" {
		return ActionPlan{}, errors.New("policy namespace is required")
	}
	targets, err := e.planTargets(ctx, namespace, repair)
	if err != nil {
		return ActionPlan{}, err
	}
	return ActionPlan{Namespace: namespace, Repair: repair, Targets: targets}, nil
}

// Execute re-checks the planned versions, dry-runs the write, then writes.
// A changed UID or resourceVersion aborts before the dry-run and the real write.
func (e *ActionExecutor) Execute(ctx context.Context, plan *ActionPlan) error {
	if plan == nil {
		return errors.New("action plan is required")
	}
	if plan.Repair.Action == actionExecCommand {
		return e.execCommand(ctx, plan.Namespace, plan.Repair.Params)
	}
	if err := e.matchRevisions(ctx, plan.Targets); err != nil {
		return err
	}
	if err := e.applyRepair(ctx, plan, true); err != nil {
		return classifyConflict(err)
	}
	if err := e.applyRepair(ctx, plan, false); err != nil {
		return classifyConflict(err)
	}
	return nil
}

func (e *ActionExecutor) planTargets(ctx context.Context, namespace string, repair Repair) ([]ObjectRevision, error) {
	switch repair.Action {
	case actionRestartPod:
		return e.planRestartPod(ctx, namespace, repair.Params)
	case actionRollbackDeployment, actionScaleDeployment:
		return e.planDeployment(ctx, namespace, repair)
	case actionDeleteResource, actionPatchResource:
		return e.planNamedObject(ctx, namespace, repair.Params)
	case actionApplyResource:
		return e.planApply(ctx, namespace, repair.Params)
	case actionExecCommand:
		return nil, nil
	default:
		return nil, fmt.Errorf("action %q is not implemented", repair.Action)
	}
}

func (e *ActionExecutor) applyRepair(ctx context.Context, plan *ActionPlan, dryRun bool) error {
	switch plan.Repair.Action {
	case actionRestartPod:
		return e.restartPod(ctx, plan.Namespace, plan.Targets, dryRun)
	case actionRollbackDeployment:
		return e.rollbackDeployment(ctx, plan.Namespace, plan.Repair.Params, plan.Targets, dryRun)
	case actionScaleDeployment:
		return e.scaleDeployment(ctx, plan.Namespace, plan.Repair.Params, plan.Targets, dryRun)
	case actionDeleteResource:
		return e.deleteResource(ctx, plan.Namespace, plan.Repair.Params, plan.Targets, dryRun)
	case actionApplyResource:
		return e.applyResource(ctx, plan.Namespace, plan.Repair.Params, plan.Targets, dryRun)
	case actionPatchResource:
		return e.patchResource(ctx, plan.Namespace, plan.Repair.Params, plan.Targets, dryRun)
	default:
		return fmt.Errorf("action %q is not implemented", plan.Repair.Action)
	}
}

func (e *ActionExecutor) planRestartPod(ctx context.Context, namespace string, params map[string]any) ([]ObjectRevision, error) {
	name, err := stringParam(params, "deployment")
	if err != nil {
		return nil, err
	}
	deploy, err := e.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get deployment %s/%s: %w", namespace, name, err)
	}
	if deploy.Spec.Selector == nil {
		return nil, fmt.Errorf("deployment %s/%s has no selector", namespace, name)
	}
	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("deployment selector: %w", err)
	}
	pods, err := e.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("list pods for deployment %s: %w", name, err)
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no pods matched deployment %s", name)
	}
	revisions := make([]ObjectRevision, 0, len(pods.Items))
	for i := range pods.Items {
		revisions = append(revisions, revisionOf("v1", "Pod", &pods.Items[i]))
	}
	return revisions, nil
}

func (e *ActionExecutor) restartPod(ctx context.Context, namespace string, targets []ObjectRevision, dryRun bool) error {
	for _, target := range targets {
		err := e.clientset.CoreV1().Pods(namespace).Delete(ctx, target.Name, deleteOptions(&target, dryRun))
		if err != nil {
			return fmt.Errorf("delete pod %s/%s: %w", namespace, target.Name, err)
		}
	}
	return nil
}

func (e *ActionExecutor) planDeployment(ctx context.Context, namespace string, repair Repair) ([]ObjectRevision, error) {
	name, err := stringParam(repair.Params, "deployment")
	if err != nil {
		return nil, err
	}
	deploy, err := e.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get deployment %s/%s: %w", namespace, name, err)
	}
	if repair.Action == actionRollbackDeployment {
		if err := e.ensureRollbackTarget(ctx, deploy, repair.Params); err != nil {
			return nil, err
		}
	}
	return []ObjectRevision{revisionOf("apps/v1", "Deployment", deploy)}, nil
}

func (e *ActionExecutor) ensureRollbackTarget(ctx context.Context, deploy *appsv1.Deployment, params map[string]any) error {
	current, err := currentRevision(deploy)
	if err != nil {
		return err
	}
	target, err := targetRevision(params, current)
	if err != nil {
		return err
	}
	_, err = e.replicaSetRevision(ctx, deploy, target)
	return err
}

func (e *ActionExecutor) rollbackDeployment(ctx context.Context, namespace string, params map[string]any, targets []ObjectRevision, dryRun bool) error {
	target, err := oneTarget(actionRollbackDeployment, targets)
	if err != nil {
		return err
	}
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
	revision, err := targetRevision(params, current)
	if err != nil {
		return err
	}
	previous, err := e.replicaSetRevision(ctx, deploy, revision)
	if err != nil {
		return err
	}
	updated := deploy.DeepCopy()
	updated.Spec.Template = previous.Spec.Template
	updated.SetResourceVersion(target.ResourceVersion)
	_, err = e.clientset.AppsV1().Deployments(namespace).Update(ctx, updated, updateOptions(dryRun))
	if err != nil {
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

func (e *ActionExecutor) scaleDeployment(ctx context.Context, namespace string, params map[string]any, targets []ObjectRevision, dryRun bool) error {
	target, err := oneTarget(actionScaleDeployment, targets)
	if err != nil {
		return err
	}
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
		"metadata": map[string]any{"resourceVersion": target.ResourceVersion},
		"spec":     map[string]any{"replicas": replicas},
	})
	if err != nil {
		return fmt.Errorf("encode scale patch: %w", err)
	}
	_, err = e.clientset.AppsV1().Deployments(namespace).Patch(
		ctx, name, types.MergePatchType, patch, patchOptions(dryRun),
	)
	if err != nil {
		return fmt.Errorf("scale deployment %s/%s: %w", namespace, name, err)
	}
	return nil
}

func (e *ActionExecutor) planNamedObject(ctx context.Context, namespace string, params map[string]any) ([]ObjectRevision, error) {
	apiVersion, kind, name, err := objectRef(params)
	if err != nil {
		return nil, err
	}
	obj, err := e.getUnstructured(ctx, namespace, apiVersion, kind, name)
	if err != nil {
		return nil, err
	}
	return []ObjectRevision{revisionOf(obj.GetAPIVersion(), obj.GetKind(), obj)}, nil
}

func (e *ActionExecutor) deleteResource(ctx context.Context, namespace string, params map[string]any, targets []ObjectRevision, dryRun bool) error {
	target, err := oneTarget(actionDeleteResource, targets)
	if err != nil {
		return err
	}
	apiVersion, kind, name, err := objectRef(params)
	if err != nil {
		return err
	}
	e.noteIgnoredNamespace(params, namespace, kind, name)
	gvr, err := e.namespacedResource(apiVersion, kind)
	if err != nil {
		return err
	}
	err = e.dynamicClient.Resource(gvr).Namespace(namespace).Delete(ctx, name, deleteOptions(&target, dryRun))
	if err != nil {
		return fmt.Errorf("delete %s %s/%s: %w", kind, namespace, name, err)
	}
	return nil
}

func (e *ActionExecutor) planApply(ctx context.Context, namespace string, params map[string]any) ([]ObjectRevision, error) {
	raw, err := manifestParam(params)
	if err != nil {
		return nil, err
	}
	obj := &unstructured.Unstructured{Object: runtime.DeepCopyJSON(raw)}
	if obj.GetName() == "" || obj.GetAPIVersion() == "" || obj.GetKind() == "" {
		return nil, errors.New("apply_resource manifest requires apiVersion, kind, and metadata.name")
	}
	live, err := e.getUnstructured(ctx, namespace, obj.GetAPIVersion(), obj.GetKind(), obj.GetName())
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []ObjectRevision{revisionOf(live.GetAPIVersion(), live.GetKind(), live)}, nil
}

func (e *ActionExecutor) applyResource(ctx context.Context, namespace string, params map[string]any, targets []ObjectRevision, dryRun bool) error {
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
	if len(targets) == 1 {
		obj.SetResourceVersion(targets[0].ResourceVersion)
	}
	gvr, err := e.namespacedResource(obj.GetAPIVersion(), obj.GetKind())
	if err != nil {
		return err
	}
	_, err = e.dynamicClient.Resource(gvr).Namespace(namespace).Apply(ctx, obj.GetName(), obj, applyOptions(dryRun))
	if err != nil {
		return fmt.Errorf("apply %s %s/%s: %w", obj.GetKind(), namespace, obj.GetName(), err)
	}
	return nil
}

func (e *ActionExecutor) patchResource(ctx context.Context, namespace string, params map[string]any, targets []ObjectRevision, dryRun bool) error {
	target, err := oneTarget(actionPatchResource, targets)
	if err != nil {
		return err
	}
	apiVersion, kind, name, err := objectRef(params)
	if err != nil {
		return err
	}
	patchObj, err := objectParam(params, "patch")
	if err != nil {
		return err
	}
	data, err := json.Marshal(withResourceVersion(patchObj, target.ResourceVersion))
	if err != nil {
		return fmt.Errorf("encode patch: %w", err)
	}
	gvr, err := e.namespacedResource(apiVersion, kind)
	if err != nil {
		return err
	}
	_, err = e.dynamicClient.Resource(gvr).Namespace(namespace).Patch(
		ctx, name, types.StrategicMergePatchType, data, patchOptions(dryRun),
	)
	if err != nil {
		return fmt.Errorf("patch %s %s/%s: %w", kind, namespace, name, err)
	}
	return nil
}

func (e *ActionExecutor) matchRevisions(ctx context.Context, targets []ObjectRevision) error {
	for _, target := range targets {
		live, err := e.readRevision(ctx, &target)
		if apierrors.IsNotFound(err) {
			return abortRevision(&target)
		}
		if err != nil {
			return err
		}
		if live.UID != target.UID || live.ResourceVersion != target.ResourceVersion {
			return abortRevision(&target)
		}
	}
	return nil
}

func (e *ActionExecutor) readRevision(ctx context.Context, target *ObjectRevision) (ObjectRevision, error) {
	switch target.Kind {
	case "Pod":
		pod, err := e.clientset.CoreV1().Pods(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return ObjectRevision{}, fmt.Errorf("get pod %s/%s: %w", target.Namespace, target.Name, err)
		}
		return revisionOf("v1", "Pod", pod), nil
	case "Deployment":
		deploy, err := e.clientset.AppsV1().Deployments(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return ObjectRevision{}, fmt.Errorf("get deployment %s/%s: %w", target.Namespace, target.Name, err)
		}
		return revisionOf("apps/v1", "Deployment", deploy), nil
	default:
		obj, err := e.getUnstructured(ctx, target.Namespace, target.APIVersion, target.Kind, target.Name)
		if err != nil {
			return ObjectRevision{}, err
		}
		return revisionOf(obj.GetAPIVersion(), obj.GetKind(), obj), nil
	}
}

func (e *ActionExecutor) getUnstructured(ctx context.Context, namespace, apiVersion, kind, name string) (*unstructured.Unstructured, error) {
	gvr, err := e.namespacedResource(apiVersion, kind)
	if err != nil {
		return nil, err
	}
	obj, err := e.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get %s %s/%s: %w", kind, namespace, name, err)
	}
	return obj, nil
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

func revisionOf(apiVersion, kind string, obj metav1.Object) ObjectRevision {
	return ObjectRevision{
		APIVersion:      apiVersion,
		Kind:            kind,
		Namespace:       obj.GetNamespace(),
		Name:            obj.GetName(),
		UID:             obj.GetUID(),
		ResourceVersion: obj.GetResourceVersion(),
	}
}

func oneTarget(action string, targets []ObjectRevision) (ObjectRevision, error) {
	if len(targets) != 1 {
		return ObjectRevision{}, fmt.Errorf("%s requires one target", action)
	}
	return targets[0], nil
}

func abortRevision(target *ObjectRevision) error {
	return fmt.Errorf("%w: %w", errAborted, apierrors.NewConflict(
		schema.GroupResource{Resource: target.Kind},
		target.Name,
		fmt.Errorf("uid or resourceVersion changed"),
	))
}

func classifyConflict(err error) error {
	if err == nil || errors.Is(err, errAborted) || !apierrors.IsConflict(err) {
		return err
	}
	return fmt.Errorf("%w: %w", errAborted, err)
}

func deleteOptions(target *ObjectRevision, dryRun bool) metav1.DeleteOptions {
	uid := target.UID
	resourceVersion := target.ResourceVersion
	opts := metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{
			UID:             &uid,
			ResourceVersion: &resourceVersion,
		},
	}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	return opts
}

func updateOptions(dryRun bool) metav1.UpdateOptions {
	if !dryRun {
		return metav1.UpdateOptions{}
	}
	return metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}}
}

func patchOptions(dryRun bool) metav1.PatchOptions {
	if !dryRun {
		return metav1.PatchOptions{}
	}
	return metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}}
}

func applyOptions(dryRun bool) metav1.ApplyOptions {
	opts := metav1.ApplyOptions{FieldManager: fieldManager, Force: true}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	return opts
}

func withResourceVersion(patch map[string]any, resourceVersion string) map[string]any {
	cloned := runtime.DeepCopyJSON(patch)
	objectMeta := map[string]any{}
	if existing, ok := cloned["metadata"].(map[string]any); ok {
		objectMeta = existing
	}
	cloned["metadata"] = objectMeta
	objectMeta["resourceVersion"] = resourceVersion
	return cloned
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
