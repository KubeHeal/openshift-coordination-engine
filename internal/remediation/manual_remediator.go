package remediation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/KubeHeal/openshift-coordination-engine/pkg/models"
)

// ManualRemediator handles manually-deployed application remediation
type ManualRemediator struct {
	clientset     kubernetes.Interface
	log           *logrus.Logger
	oomMultiplier float64           // memory limit multiplier for OOMKill remediation
	oomMaxLimit   resource.Quantity // ceiling for memory limit increases
}

// NewManualRemediator creates a new manual remediator with default OOM settings.
func NewManualRemediator(clientset kubernetes.Interface, log *logrus.Logger) *ManualRemediator {
	return &ManualRemediator{
		clientset:     clientset,
		log:           log,
		oomMultiplier: 2.5,
		oomMaxLimit:   resource.MustParse("2Gi"),
	}
}

// SetOOMConfig overrides the default OOM remediation parameters.
func (mr *ManualRemediator) SetOOMConfig(multiplier float64, maxLimit string) {
	if multiplier > 0 {
		mr.oomMultiplier = multiplier
	}
	if maxLimit != "" {
		mr.oomMaxLimit = resource.MustParse(maxLimit)
	}
}

// Remediate performs direct Kubernetes API remediation
func (mr *ManualRemediator) Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo, issue *models.Issue) error {
	mr.log.WithFields(logrus.Fields{
		"namespace":     issue.Namespace,
		"resource":      issue.ResourceName,
		"resource_type": issue.ResourceType,
		"issue_type":    issue.Type,
	}).Info("Starting manual remediation")

	// Route to appropriate remediation based on issue type
	switch issue.Type {
	case "CrashLoopBackOff", "crashloopbackoff":
		return mr.remediateCrashLoop(ctx, issue)
	case "ImagePullBackOff", "imagepullbackoff":
		return mr.remediateImagePull(ctx, issue)
	case "OOMKilled", "oomkilled":
		return mr.remediateOOM(ctx, issue)
	case "pod_crash_loop":
		return mr.remediateCrashLoop(ctx, issue)
	default:
		return mr.remediateGeneric(ctx, issue)
	}
}

// CanRemediate returns true for manual deployments or unknown methods
func (mr *ManualRemediator) CanRemediate(deploymentInfo *models.DeploymentInfo) bool {
	return deploymentInfo.Method == models.DeploymentMethodManual ||
		deploymentInfo.Method == models.DeploymentMethodUnknown ||
		deploymentInfo.IsManuallyDeployed()
}

// Name returns the remediator name
func (mr *ManualRemediator) Name() string {
	return "manual"
}

// remediateCrashLoop handles CrashLoopBackOff by deleting pod
func (mr *ManualRemediator) remediateCrashLoop(ctx context.Context, issue *models.Issue) error {
	mr.log.WithFields(logrus.Fields{
		"namespace": issue.Namespace,
		"pod":       issue.ResourceName,
	}).Info("Remediating CrashLoopBackOff: deleting pod")

	// If resource type is deployment, try to rollback
	if issue.ResourceType == "deployment" || issue.ResourceType == "Deployment" {
		return mr.rollbackDeployment(ctx, issue)
	}

	// For pods, delete to trigger recreation
	err := mr.clientset.CoreV1().Pods(issue.Namespace).Delete(ctx, issue.ResourceName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	mr.log.Info("Pod deleted, deployment will recreate it")
	return nil
}

// remediateImagePull handles ImagePullBackOff
func (mr *ManualRemediator) remediateImagePull(ctx context.Context, issue *models.Issue) error {
	mr.log.WithFields(logrus.Fields{
		"namespace": issue.Namespace,
		"pod":       issue.ResourceName,
	}).Warn("ImagePullBackOff detected: checking image and credentials")

	// Get pod to check image
	pod, err := mr.clientset.CoreV1().Pods(issue.Namespace).Get(ctx, issue.ResourceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get pod: %w", err)
	}

	// Log image information
	for i := range pod.Spec.Containers {
		mr.log.WithFields(logrus.Fields{
			"container": pod.Spec.Containers[i].Name,
			"image":     pod.Spec.Containers[i].Image,
		}).Info("Container image details")
	}

	mr.log.Warn("ImagePullBackOff requires manual intervention: check image availability and credentials")
	return fmt.Errorf("ImagePullBackOff requires manual intervention: verify image exists and pull secrets are configured")
}

// remediateOOM handles OOMKilled pods by patching the owning Deployment's
// memory limits.  If no Deployment can be resolved, it falls back to
// deleting the pod (the previous behaviour).
func (mr *ManualRemediator) remediateOOM(ctx context.Context, issue *models.Issue) error {
	mr.log.WithFields(logrus.Fields{
		"namespace": issue.Namespace,
		"pod":       issue.ResourceName,
	}).Warn("OOMKilled detected: attempting to increase memory limits on owning Deployment")

	// 1. Get the pod.
	pod, err := mr.clientset.CoreV1().Pods(issue.Namespace).Get(ctx, issue.ResourceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get pod: %w", err)
	}

	// 2. Resolve the owning Deployment.
	deployment, resolveErr := mr.resolveOwnerDeployment(ctx, issue.Namespace, pod)
	if resolveErr != nil || deployment == nil {
		mr.log.WithField("reason", resolveErr).Warn(
			"Could not resolve owning Deployment, falling back to pod delete")
		delErr := mr.clientset.CoreV1().Pods(issue.Namespace).Delete(ctx, issue.ResourceName, metav1.DeleteOptions{})
		if delErr != nil {
			return fmt.Errorf("failed to delete pod: %w", delErr)
		}
		mr.log.Warn("Pod deleted, but OOM may recur without memory limit increase")
		return nil
	}

	// 3. Find the target container (first OOMKilled, or first container).
	containerIdx := mr.findOOMKilledContainer(pod)

	container := deployment.Spec.Template.Spec.Containers[containerIdx]
	currentLimit := container.Resources.Limits.Memory()

	if currentLimit == nil || currentLimit.IsZero() {
		mr.log.Warn("Container has no memory limit set, falling back to pod delete")
		if delErr := mr.clientset.CoreV1().Pods(issue.Namespace).Delete(ctx, issue.ResourceName, metav1.DeleteOptions{}); delErr != nil {
			return fmt.Errorf("failed to delete pod (no memory limit set): %w", delErr)
		}
		return nil
	}

	// 4. Calculate new limits.
	oldLimitBytes := currentLimit.Value()
	newLimitBytes := int64(float64(oldLimitBytes) * mr.oomMultiplier)
	maxBytes := mr.oomMaxLimit.Value()
	if newLimitBytes > maxBytes {
		newLimitBytes = maxBytes
	}
	newLimit := resource.NewQuantity(newLimitBytes, resource.BinarySI)
	newRequestBytes := int64(float64(newLimitBytes) * 0.8)
	newRequest := resource.NewQuantity(newRequestBytes, resource.BinarySI)

	mr.log.WithFields(logrus.Fields{
		"deployment":  deployment.Name,
		"container":   container.Name,
		"old_limit":   currentLimit.String(),
		"new_limit":   newLimit.String(),
		"new_request": newRequest.String(),
	}).Info("Patching Deployment memory limits")

	// 5. Build and apply StrategicMergePatch.
	if patchErr := mr.patchDeploymentMemory(
		ctx, deployment, containerIdx,
		newLimit, newRequest, currentLimit.String(), newLimit.String(),
	); patchErr != nil {
		return fmt.Errorf("failed to patch deployment memory limits: %w", patchErr)
	}

	mr.log.WithFields(logrus.Fields{
		"deployment": deployment.Name,
		"old_limit":  currentLimit.String(),
		"new_limit":  newLimit.String(),
	}).Info("Deployment memory limits patched — Deployment controller will roll out new pods")

	return nil
}

// resolveOwnerDeployment walks Pod -> OwnerRef(ReplicaSet) -> OwnerRef(Deployment).
func (mr *ManualRemediator) resolveOwnerDeployment(
	ctx context.Context, namespace string, pod *corev1.Pod,
) (*appsv1.Deployment, error) {
	// Find a ReplicaSet owner.
	rsName := ""
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "ReplicaSet" {
			rsName = ref.Name
			break
		}
	}
	if rsName == "" {
		return nil, fmt.Errorf("pod %s has no ReplicaSet owner", pod.Name)
	}

	rs, err := mr.clientset.AppsV1().ReplicaSets(namespace).Get(ctx, rsName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get ReplicaSet %s: %w", rsName, err)
	}

	// Find a Deployment owner on the ReplicaSet.
	deployName := ""
	for _, ref := range rs.OwnerReferences {
		if ref.Kind == "Deployment" {
			deployName = ref.Name
			break
		}
	}
	if deployName == "" {
		return nil, fmt.Errorf("ReplicaSet %s has no Deployment owner", rsName)
	}

	deploy, err := mr.clientset.AppsV1().Deployments(namespace).Get(ctx, deployName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Deployment %s: %w", deployName, err)
	}

	return deploy, nil
}

// findOOMKilledContainer returns the index of the first OOMKilled container in
// the pod's status, or 0 if none can be identified.
func (mr *ManualRemediator) findOOMKilledContainer(pod *corev1.Pod) int {
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
			return i
		}
		if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
			return i
		}
	}
	return 0 // default to first container
}

// deploymentMemoryPatch is the JSON structure for StrategicMergePatch.
type deploymentMemoryPatch struct {
	Metadata patchMetadata `json:"metadata"`
	Spec     patchSpec     `json:"spec"`
}

type patchMetadata struct {
	Annotations map[string]string `json:"annotations"`
}

type patchSpec struct {
	Template patchTemplate `json:"template"`
}

type patchTemplate struct {
	Spec patchPodSpec `json:"spec"`
}

type patchPodSpec struct {
	Containers []patchContainer `json:"containers"`
}

type patchContainer struct {
	Name      string         `json:"name"`
	Resources patchResources `json:"resources"`
}

type patchResources struct {
	Limits   map[string]string `json:"limits"`
	Requests map[string]string `json:"requests"`
}

// patchDeploymentMemory applies a StrategicMergePatch to update the target
// container's memory limits and adds remediation annotations.
func (mr *ManualRemediator) patchDeploymentMemory(
	ctx context.Context,
	deploy *appsv1.Deployment,
	containerIdx int,
	newLimit, newRequest *resource.Quantity,
	oldLimitStr, newLimitStr string,
) error {
	containerName := deploy.Spec.Template.Spec.Containers[containerIdx].Name

	patch := deploymentMemoryPatch{
		Metadata: patchMetadata{
			Annotations: map[string]string{
				"self-healing.kubeheal.io/last-remediation": time.Now().Format(time.RFC3339),
				"self-healing.kubeheal.io/memory-increase":  oldLimitStr + "->" + newLimitStr,
			},
		},
		Spec: patchSpec{
			Template: patchTemplate{
				Spec: patchPodSpec{
					Containers: []patchContainer{
						{
							Name: containerName,
							Resources: patchResources{
								Limits:   map[string]string{"memory": newLimit.String()},
								Requests: map[string]string{"memory": newRequest.String()},
							},
						},
					},
				},
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	if _, patchErr := mr.clientset.AppsV1().Deployments(deploy.Namespace).Patch(
		ctx, deploy.Name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{},
	); patchErr != nil {
		return fmt.Errorf("failed to apply patch to deployment %s: %w", deploy.Name, patchErr)
	}
	return nil
}

// remediateGeneric handles generic issues by restarting pod
func (mr *ManualRemediator) remediateGeneric(ctx context.Context, issue *models.Issue) error {
	mr.log.WithFields(logrus.Fields{
		"namespace":  issue.Namespace,
		"resource":   issue.ResourceName,
		"issue_type": issue.Type,
	}).Info("Generic remediation: restarting resource")

	// Try deployment restart first if it's a deployment
	if issue.ResourceType == "deployment" || issue.ResourceType == "Deployment" {
		return mr.restartDeployment(ctx, issue)
	}

	// Otherwise delete the pod
	err := mr.clientset.CoreV1().Pods(issue.Namespace).Delete(ctx, issue.ResourceName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	mr.log.Info("Pod deleted for restart")
	return nil
}

// rollbackDeployment rolls back a deployment to previous revision
func (mr *ManualRemediator) rollbackDeployment(ctx context.Context, issue *models.Issue) error {
	mr.log.WithFields(logrus.Fields{
		"namespace":  issue.Namespace,
		"deployment": issue.ResourceName,
	}).Info("Rolling back deployment to previous revision")

	// Get deployment
	deployment, err := mr.clientset.AppsV1().Deployments(issue.Namespace).Get(ctx, issue.ResourceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}

	// Check if there are revisions to rollback to
	if deployment.Status.ObservedGeneration <= 1 {
		mr.log.Warn("No previous revision to rollback to, restarting pods instead")
		return mr.restartDeployment(ctx, issue)
	}

	// Trigger rollback by setting revision annotation
	// Note: In Kubernetes, rollback is achieved by finding previous ReplicaSet
	// and scaling it up while scaling down current one
	// For simplicity, we'll restart the deployment
	mr.log.Info("Triggering deployment restart")
	return mr.restartDeployment(ctx, issue)
}

// restartDeployment restarts a deployment by updating its template
func (mr *ManualRemediator) restartDeployment(ctx context.Context, issue *models.Issue) error {
	mr.log.WithFields(logrus.Fields{
		"namespace":  issue.Namespace,
		"deployment": issue.ResourceName,
	}).Info("Restarting deployment")

	// Get deployment
	deployment, err := mr.clientset.AppsV1().Deployments(issue.Namespace).Get(ctx, issue.ResourceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}

	// Add/update restart annotation to trigger rollout
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = make(map[string]string)
	}
	deployment.Spec.Template.Annotations["remediation.aiops/restarted-at"] = time.Now().Format(time.RFC3339)

	// Update deployment
	_, err = mr.clientset.AppsV1().Deployments(issue.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update deployment: %w", err)
	}

	mr.log.Info("Deployment restart triggered")
	return nil
}

// Helper methods for additional remediation scenarios

// scaleDeployment scales a deployment to specified replicas
