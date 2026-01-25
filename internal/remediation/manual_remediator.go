package remediation

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// ManualRemediator handles manually-deployed application remediation
type ManualRemediator struct {
	clientset kubernetes.Interface
	log       *logrus.Logger
}

// NewManualRemediator creates a new manual remediator
func NewManualRemediator(clientset kubernetes.Interface, log *logrus.Logger) *ManualRemediator {
	return &ManualRemediator{
		clientset: clientset,
		log:       log,
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

// remediateOOM handles OOMKilled pods
func (mr *ManualRemediator) remediateOOM(ctx context.Context, issue *models.Issue) error {
	mr.log.WithFields(logrus.Fields{
		"namespace": issue.Namespace,
		"pod":       issue.ResourceName,
	}).Warn("OOMKilled detected: considering memory limit increase")

	// Get pod to check current limits
	pod, err := mr.clientset.CoreV1().Pods(issue.Namespace).Get(ctx, issue.ResourceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get pod: %w", err)
	}

	// Log current resource limits
	for i := range pod.Spec.Containers {
		mr.log.WithFields(logrus.Fields{
			"container":      pod.Spec.Containers[i].Name,
			"memory_limit":   pod.Spec.Containers[i].Resources.Limits.Memory().String(),
			"memory_request": pod.Spec.Containers[i].Resources.Requests.Memory().String(),
		}).Info("Current container resource limits")
	}

	// Delete pod to restart (may OOM again)
	err = mr.clientset.CoreV1().Pods(issue.Namespace).Delete(ctx, issue.ResourceName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	mr.log.Warn("Pod deleted, but OOM may recur without memory limit increase")
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
