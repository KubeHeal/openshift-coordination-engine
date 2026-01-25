package remediation

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// OperatorRemediator handles operator-managed application remediation
// Uses dynamic client to trigger reconciliation via CR annotation updates
type OperatorRemediator struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	log           *logrus.Logger
}

// CustomResourceInfo contains information about a Custom Resource
type CustomResourceInfo struct {
	Kind       string
	Name       string
	APIVersion string
	Group      string
	Version    string
	Resource   string
}

// NewOperatorRemediator creates a new operator remediator
func NewOperatorRemediator(clientset kubernetes.Interface, dynamicClient dynamic.Interface, log *logrus.Logger) *OperatorRemediator {
	return &OperatorRemediator{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		log:           log,
	}
}

// Remediate triggers operator reconciliation by updating CR annotation
func (or *OperatorRemediator) Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo, issue *models.Issue) error {
	operatorName := deploymentInfo.GetDetail("operator")
	if operatorName == "" {
		operatorName = deploymentInfo.GetDetail("managed_by")
	}

	or.log.WithFields(logrus.Fields{
		"operator":      operatorName,
		"namespace":     issue.Namespace,
		"resource":      issue.ResourceName,
		"resource_type": issue.ResourceType,
		"method":        "operator",
	}).Info("Starting operator remediation")

	// Find the Custom Resource (CR) that owns this resource
	cr, err := or.findOwningCR(ctx, issue.Namespace, issue.ResourceName, issue.ResourceType)
	if err != nil {
		return fmt.Errorf("failed to find owning CR: %w", err)
	}

	if cr == nil {
		or.log.Warn("No owning CR found, cannot trigger operator reconciliation")
		return fmt.Errorf("no owning CR found for %s/%s", issue.Namespace, issue.ResourceName)
	}

	or.log.WithFields(logrus.Fields{
		"cr_kind":       cr.Kind,
		"cr_name":       cr.Name,
		"cr_apiversion": cr.APIVersion,
	}).Info("Found owning Custom Resource")

	// Trigger reconciliation by updating CR annotation
	if err := or.triggerReconciliation(ctx, cr, issue.Namespace); err != nil {
		return fmt.Errorf("failed to trigger reconciliation: %w", err)
	}

	or.log.WithField("cr_name", cr.Name).Info("Operator reconciliation triggered successfully")
	return nil
}

// CanRemediate returns true if deployment is operator-managed
func (or *OperatorRemediator) CanRemediate(deploymentInfo *models.DeploymentInfo) bool {
	return deploymentInfo.Method == models.DeploymentMethodOperator || deploymentInfo.IsOperatorManaged()
}

// Name returns the remediator name
func (or *OperatorRemediator) Name() string {
	return "operator"
}

// findOwningCR finds the Custom Resource that owns a deployment/pod
// It walks the owner reference chain to find the root CR
func (or *OperatorRemediator) findOwningCR(ctx context.Context, namespace, resourceName, resourceType string) (*CustomResourceInfo, error) {
	or.log.WithFields(logrus.Fields{
		"namespace":     namespace,
		"resource":      resourceName,
		"resource_type": resourceType,
	}).Debug("Looking for owning Custom Resource")

	// If resource is a Deployment, get it to check owner references
	if resourceType == "Deployment" {
		deployment, err := or.clientset.AppsV1().Deployments(namespace).Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get deployment: %w", err)
		}

		// Check deployment's owner references
		cr := or.extractCRFromOwnerRefs(deployment.OwnerReferences)
		if cr != nil {
			return cr, nil
		}

		// If deployment doesn't have CR owner, check its pods
		pods, err := or.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", resourceName),
		})
		if err != nil {
			or.log.WithError(err).Warn("Failed to list pods, checking deployment owner refs only")
			return nil, nil
		}

		if len(pods.Items) > 0 {
			cr := or.extractCRFromOwnerRefs(pods.Items[0].OwnerReferences)
			if cr != nil {
				return cr, nil
			}
		}

		return nil, nil
	}

	// If resource is a Pod, get it directly
	if resourceType == "Pod" {
		pod, err := or.clientset.CoreV1().Pods(namespace).Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get pod: %w", err)
		}

		return or.extractCRFromOwnerRefs(pod.OwnerReferences), nil
	}

	// For other resource types, we can't reliably find the CR without more information
	or.log.WithField("resource_type", resourceType).Warn("Unsupported resource type for CR lookup")
	return nil, fmt.Errorf("unsupported resource type for CR lookup: %s", resourceType)
}

// extractCRFromOwnerRefs extracts Custom Resource info from owner references
// Returns nil if no CR owner found (only built-in resources like Deployment, ReplicaSet)
func (or *OperatorRemediator) extractCRFromOwnerRefs(ownerRefs []metav1.OwnerReference) *CustomResourceInfo {
	for _, owner := range ownerRefs {
		// Skip built-in Kubernetes resources
		if or.isBuiltInKubernetesResource(owner.Kind) {
			continue
		}

		// This is likely a Custom Resource
		or.log.WithFields(logrus.Fields{
			"kind":       owner.Kind,
			"name":       owner.Name,
			"apiversion": owner.APIVersion,
		}).Debug("Found Custom Resource owner")

		// Parse APIVersion (format: group/version)
		group, version := parseAPIVersion(owner.APIVersion)

		// Infer resource name from kind (lowercase + pluralize)
		resource := inferResourceName(owner.Kind)

		return &CustomResourceInfo{
			Kind:       owner.Kind,
			Name:       owner.Name,
			APIVersion: owner.APIVersion,
			Group:      group,
			Version:    version,
			Resource:   resource,
		}
	}

	return nil
}

// isBuiltInKubernetesResource returns true if kind is a built-in Kubernetes resource
func (or *OperatorRemediator) isBuiltInKubernetesResource(kind string) bool {
	builtInKinds := []string{
		"Pod", "Deployment", "ReplicaSet", "StatefulSet", "DaemonSet",
		"Service", "ConfigMap", "Secret", "PersistentVolumeClaim",
		"Job", "CronJob", "Ingress", "NetworkPolicy",
	}

	for _, builtIn := range builtInKinds {
		if kind == builtIn {
			return true
		}
	}

	return false
}

// triggerReconciliation triggers operator reconciliation by updating CR annotation
// Uses dynamic client to patch the Custom Resource
func (or *OperatorRemediator) triggerReconciliation(ctx context.Context, cr *CustomResourceInfo, namespace string) error {
	// Create GVR (GroupVersionResource) for dynamic client
	gvr := schema.GroupVersionResource{
		Group:    cr.Group,
		Version:  cr.Version,
		Resource: cr.Resource,
	}

	or.log.WithFields(logrus.Fields{
		"cr_name":   cr.Name,
		"namespace": namespace,
		"gvr":       gvr.String(),
	}).Info("Updating CR to trigger reconciliation")

	// Verify the CR exists before patching
	_, err := or.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, cr.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CR: %w", err)
	}

	// Create patch to add/update remediation trigger annotation
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// Create merge patch as JSON string
	patchData := fmt.Sprintf(`{
		"metadata": {
			"annotations": {
				"remediation.aiops/trigger": "%s",
				"remediation.aiops/trigger-by": "coordination-engine"
			}
		}
	}`, timestamp)

	patchBytes := []byte(patchData)

	// Apply patch using merge patch
	or.log.WithFields(logrus.Fields{
		"cr_name":   cr.Name,
		"timestamp": timestamp,
		"patch":     patchData,
	}).Debug("Applying patch to CR")

	_, err = or.dynamicClient.Resource(gvr).Namespace(namespace).Patch(
		ctx,
		cr.Name,
		types.MergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)

	if err != nil {
		return fmt.Errorf("failed to patch CR: %w", err)
	}

	or.log.WithFields(logrus.Fields{
		"cr_name":             cr.Name,
		"reconciliation_time": timestamp,
	}).Info("CR annotation updated, operator should reconcile")

	return nil
}

// parseAPIVersion parses apiVersion into group and version
// Format: "group/version" or just "version" for core group
func parseAPIVersion(apiVersion string) (group, version string) {
	// Split by /
	parts := splitAPIVersion(apiVersion)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}

	// No group means core group
	return "", parts[0]
}

// splitAPIVersion splits apiVersion string on /
func splitAPIVersion(apiVersion string) []string {
	result := make([]string, 0, 2)
	current := ""
	for _, ch := range apiVersion {
		if ch == '/' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

// inferResourceName infers the resource name from kind
// Converts kind to lowercase and adds 's' for simple pluralization
// Note: This is a simplification. In production, use discovery API or maintain a mapping
func inferResourceName(kind string) string {
	// Convert to lowercase
	lower := ""
	for _, ch := range kind {
		if ch >= 'A' && ch <= 'Z' {
			lower += string(ch + 32) // Convert to lowercase
		} else {
			lower += string(ch)
		}
	}

	// Simple pluralization (add 's')
	// This works for most cases but not all (e.g., "Ingress" -> "ingresses")
	// In production, you'd want to use the discovery API or a proper pluralizer
	return lower + "s"
}
