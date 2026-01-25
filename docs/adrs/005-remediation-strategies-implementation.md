# ADR-005: Remediation Strategies Implementation

## Status
ACCEPTED - 2025-12-18

## Context

This ADR defines the Go implementation of deployment-aware remediation strategies as outlined in platform ADR-039 (Non-ArgoCD Application Remediation Strategy). The coordination engine must handle remediation for applications deployed through ArgoCD, Helm, Operators, or manually, respecting each deployment tool's lifecycle management.

Platform ADR-039 establishes:
- Four deployment methods: ArgoCD, Helm, Operator, Manual
- Detection priority with confidence scoring (ADR-002)
- Strategy-based routing to appropriate remediator
- Respect for deployment tool boundaries to avoid conflicts

Without proper remediation strategies:
- Helm releases break due to direct pod restarts
- Operator reconciliation loops revert manual changes
- GitOps workflows get bypassed
- Audit trails are lost

## Decision

Implement strategy-based remediation in Go with the following components:

### 1. Remediator Interface

Package: `internal/remediation/interfaces.go`

```go
package remediation

import (
	"context"

	"openshift-coordination-engine/pkg/models"
)

// Remediator performs remediation for a specific deployment method
type Remediator interface {
	// Remediate executes remediation logic
	Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo, issue *models.Issue) error

	// CanRemediate returns true if this remediator can handle the deployment
	CanRemediate(deploymentInfo *models.DeploymentInfo) bool

	// Name returns the remediator's name
	Name() string
}

// RemediationResult contains the outcome of remediation
type RemediationResult struct {
	Status   string `json:"status"` // "success", "failed", "recommendation"
	Method   string `json:"method"` // Remediation method used
	Message  string `json:"message,omitempty"`
	Duration string `json:"duration,omitempty"`
}
```

Package: `pkg/models/issue.go`

```go
package models

// Issue represents a problem requiring remediation
type Issue struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"` // "CrashLoopBackOff", "ImagePullBackOff", etc.
	Severity    string   `json:"severity"`
	Namespace   string   `json:"namespace"`
	ResourceType string  `json:"resource_type"` // "pod", "deployment"
	ResourceName string  `json:"resource_name"`
	Description string   `json:"description"`
}
```

### 2. Strategy Selector

Package: `internal/remediation/strategy_selector.go`

```go
package remediation

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"openshift-coordination-engine/pkg/models"
)

// StrategySelector routes remediation to the appropriate remediator
type StrategySelector struct {
	argocdRemediator   Remediator
	helmRemediator     Remediator
	operatorRemediator Remediator
	manualRemediator   Remediator
	log                *logrus.Logger
}

// NewStrategySelector creates a new strategy selector
func NewStrategySelector(
	argocdRemediator Remediator,
	helmRemediator Remediator,
	operatorRemediator Remediator,
	manualRemediator Remediator,
	log *logrus.Logger,
) *StrategySelector {
	return &StrategySelector{
		argocdRemediator:   argocdRemediator,
		helmRemediator:     helmRemediator,
		operatorRemediator: operatorRemediator,
		manualRemediator:   manualRemediator,
		log:                log,
	}
}

// SelectRemediator chooses the appropriate remediator based on deployment method
func (ss *StrategySelector) SelectRemediator(deploymentInfo *models.DeploymentInfo) Remediator {
	ss.log.WithFields(logrus.Fields{
		"method":     deploymentInfo.Method,
		"managed":    deploymentInfo.Managed,
		"confidence": deploymentInfo.Confidence,
	}).Info("Selecting remediation strategy")

	// Try each remediator in priority order
	remediators := []Remediator{
		ss.argocdRemediator,
		ss.helmRemediator,
		ss.operatorRemediator,
		ss.manualRemediator,
	}

	for _, remediator := range remediators {
		if remediator.CanRemediate(deploymentInfo) {
			ss.log.WithField("remediator", remediator.Name()).Info("Remediator selected")
			return remediator
		}
	}

	// Fallback to manual remediator
	ss.log.Warn("No specific remediator matched, falling back to manual")
	return ss.manualRemediator
}

// Remediate executes remediation using the selected strategy
func (ss *StrategySelector) Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo, issue *models.Issue) error {
	remediator := ss.SelectRemediator(deploymentInfo)

	ss.log.WithFields(logrus.Fields{
		"issue_id":   issue.ID,
		"issue_type": issue.Type,
		"remediator": remediator.Name(),
	}).Info("Starting remediation")

	return remediator.Remediate(ctx, deploymentInfo, issue)
}
```

### 3. Helm Remediator

Package: `internal/remediation/helm_remediator.go`

```go
package remediation

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"

	"openshift-coordination-engine/pkg/models"
)

// HelmRemediator handles Helm-managed application remediation
type HelmRemediator struct {
	log *logrus.Logger
}

// NewHelmRemediator creates a new Helm remediator
func NewHelmRemediator(log *logrus.Logger) *HelmRemediator {
	return &HelmRemediator{
		log: log,
	}
}

// Remediate triggers Helm upgrade or rollback
func (hr *HelmRemediator) Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo, issue *models.Issue) error {
	hr.log.WithFields(logrus.Fields{
		"release":   deploymentInfo.ManagedBy,
		"namespace": deploymentInfo.Namespace,
	}).Info("Starting Helm remediation")

	releaseName := deploymentInfo.ManagedBy
	namespace := deploymentInfo.Namespace

	// Check release status
	releaseStatus, err := hr.getReleaseStatus(ctx, releaseName, namespace)
	if err != nil {
		return fmt.Errorf("failed to get release status: %w", err)
	}

	hr.log.WithFields(logrus.Fields{
		"release": releaseName,
		"status":  releaseStatus,
	}).Info("Helm release status")

	// If release is in failed state, rollback
	if releaseStatus == "failed" || releaseStatus == "superseded" {
		hr.log.WithField("release", releaseName).Info("Rolling back Helm release")
		if err := hr.rollbackRelease(ctx, releaseName, namespace); err != nil {
			return fmt.Errorf("helm rollback failed: %w", err)
		}
		return nil
	}

	// Otherwise, trigger helm upgrade with --reuse-values
	hr.log.WithField("release", releaseName).Info("Upgrading Helm release")
	if err := hr.upgradeRelease(ctx, releaseName, namespace); err != nil {
		return fmt.Errorf("helm upgrade failed: %w", err)
	}

	return nil
}

// CanRemediate returns true if deployment is Helm-managed
func (hr *HelmRemediator) CanRemediate(deploymentInfo *models.DeploymentInfo) bool {
	return deploymentInfo.Method == models.DeploymentMethodHelm
}

// Name returns the remediator name
func (hr *HelmRemediator) Name() string {
	return "helm"
}

// getReleaseStatus queries Helm release status
func (hr *HelmRemediator) getReleaseStatus(ctx context.Context, releaseName, namespace string) (string, error) {
	cmd := exec.CommandContext(ctx, "helm", "status", releaseName, "-n", namespace, "-o", "json")

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("helm status command failed: %w", err)
	}

	// Parse JSON output to get status
	// Simplified: in production, use JSON unmarshaling
	if strings.Contains(string(output), "\"deployed\"") {
		return "deployed", nil
	} else if strings.Contains(string(output), "\"failed\"") {
		return "failed", nil
	} else if strings.Contains(string(output), "\"superseded\"") {
		return "superseded", nil
	}

	return "unknown", nil
}

// rollbackRelease rolls back Helm release to previous revision
func (hr *HelmRemediator) rollbackRelease(ctx context.Context, releaseName, namespace string) error {
	cmd := exec.CommandContext(ctx, "helm", "rollback", releaseName, "-n", namespace)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm rollback failed: %w, output: %s", err, string(output))
	}

	hr.log.WithFields(logrus.Fields{
		"release": releaseName,
		"output":  string(output),
	}).Info("Helm rollback completed")

	return nil
}

// upgradeRelease triggers Helm upgrade with --reuse-values
func (hr *HelmRemediator) upgradeRelease(ctx context.Context, releaseName, namespace string) error {
	// Get current chart
	cmd := exec.CommandContext(ctx, "helm", "upgrade", releaseName, releaseName,
		"-n", namespace,
		"--reuse-values",
		"--atomic",
		"--wait",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm upgrade failed: %w, output: %s", err, string(output))
	}

	hr.log.WithFields(logrus.Fields{
		"release": releaseName,
		"output":  string(output),
	}).Info("Helm upgrade completed")

	return nil
}
```

### 4. Operator Remediator

Package: `internal/remediation/operator_remediator.go`

```go
package remediation

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"openshift-coordination-engine/pkg/models"
)

// OperatorRemediator handles operator-managed application remediation
type OperatorRemediator struct {
	clientset *kubernetes.Clientset
	log       *logrus.Logger
}

// NewOperatorRemediator creates a new operator remediator
func NewOperatorRemediator(clientset *kubernetes.Clientset, log *logrus.Logger) *OperatorRemediator {
	return &OperatorRemediator{
		clientset: clientset,
		log:       log,
	}
}

// Remediate triggers operator reconciliation
func (or *OperatorRemediator) Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo, issue *models.Issue) error {
	or.log.WithFields(logrus.Fields{
		"operator":  deploymentInfo.ManagedBy,
		"namespace": issue.Namespace,
		"resource":  issue.ResourceName,
	}).Info("Starting operator remediation")

	// Find the Custom Resource (CR) that owns this resource
	cr, err := or.findOwningCR(ctx, issue.Namespace, issue.ResourceName)
	if err != nil {
		return fmt.Errorf("failed to find owning CR: %w", err)
	}

	if cr == nil {
		or.log.Warn("No owning CR found, cannot trigger operator reconciliation")
		return fmt.Errorf("no owning CR found for %s/%s", issue.Namespace, issue.ResourceName)
	}

	or.log.WithFields(logrus.Fields{
		"cr_kind": cr["kind"],
		"cr_name": cr["name"],
	}).Info("Found owning Custom Resource")

	// Trigger reconciliation by updating CR annotation
	if err := or.triggerReconciliation(ctx, cr, issue.Namespace); err != nil {
		return fmt.Errorf("failed to trigger reconciliation: %w", err)
	}

	or.log.Info("Operator reconciliation triggered successfully")
	return nil
}

// CanRemediate returns true if deployment is operator-managed
func (or *OperatorRemediator) CanRemediate(deploymentInfo *models.DeploymentInfo) bool {
	return deploymentInfo.Method == models.DeploymentMethodOperator && deploymentInfo.Managed
}

// Name returns the remediator name
func (or *OperatorRemediator) Name() string {
	return "operator"
}

// findOwningCR finds the Custom Resource that owns a pod/deployment
func (or *OperatorRemediator) findOwningCR(ctx context.Context, namespace, resourceName string) (map[string]string, error) {
	// Get pod to find owner references
	pod, err := or.clientset.CoreV1().Pods(namespace).Get(ctx, resourceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}

	// Check owner references
	for _, owner := range pod.OwnerReferences {
		// Skip non-custom resources (Deployment, ReplicaSet, etc.)
		if owner.Kind == "ReplicaSet" || owner.Kind == "Deployment" {
			continue
		}

		// Found a potential CR owner
		return map[string]string{
			"kind": owner.Kind,
			"name": owner.Name,
		}, nil
	}

	return nil, nil
}

// triggerReconciliation triggers operator reconciliation by updating CR annotation
func (or *OperatorRemediator) triggerReconciliation(ctx context.Context, cr map[string]string, namespace string) error {
	// Update CR annotation to trigger reconciliation
	// This is a simplified version - in production, use dynamic client for CRs

	or.log.WithFields(logrus.Fields{
		"cr_kind":      cr["kind"],
		"cr_name":      cr["name"],
		"namespace":    namespace,
		"annotation":   "remediation.aiops/trigger",
		"trigger_time": time.Now().Unix(),
	}).Info("Triggering operator reconciliation")

	// Uses dynamic client to patch CR with annotation
	// Steps: Get CR → Add/update annotation → Patch CR to cluster
	// Annotation format: remediation.aiops/trigger: <timestamp>
	// Operator detects annotation change and reconciles

	return nil
}
```

### 5. Manual Remediator

Package: `internal/remediation/manual_remediator.go`

```go
package remediation

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"openshift-coordination-engine/pkg/models"
)

// ManualRemediator handles manually-deployed application remediation
type ManualRemediator struct {
	clientset *kubernetes.Clientset
	log       *logrus.Logger
}

// NewManualRemediator creates a new manual remediator
func NewManualRemediator(clientset *kubernetes.Clientset, log *logrus.Logger) *ManualRemediator {
	return &ManualRemediator{
		clientset: clientset,
		log:       log,
	}
}

// Remediate performs direct Kubernetes API remediation
func (mr *ManualRemediator) Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo, issue *models.Issue) error {
	mr.log.WithFields(logrus.Fields{
		"namespace":   issue.Namespace,
		"resource":    issue.ResourceName,
		"issue_type":  issue.Type,
	}).Info("Starting manual remediation")

	switch issue.Type {
	case "CrashLoopBackOff":
		return mr.remediateCrashLoop(ctx, issue)
	case "ImagePullBackOff":
		return mr.remediateImagePull(ctx, issue)
	case "OOMKilled":
		return mr.remediateOOM(ctx, issue)
	default:
		return mr.remediateGeneric(ctx, issue)
	}
}

// CanRemediate returns true for manual deployments or unknown methods
func (mr *ManualRemediator) CanRemediate(deploymentInfo *models.DeploymentInfo) bool {
	return deploymentInfo.Method == models.DeploymentMethodManual ||
	       deploymentInfo.Method == models.DeploymentMethodUnknown
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
	}).Warn("ImagePullBackOff detected: manual intervention may be required")

	// Check if image exists, provide recommendation
	// In production: query container registry, check image availability

	return fmt.Errorf("ImagePullBackOff requires manual intervention: check image availability and credentials")
}

// remediateOOM handles OOMKilled pods
func (mr *ManualRemediator) remediateOOM(ctx context.Context, issue *models.Issue) error {
	mr.log.WithFields(logrus.Fields{
		"namespace": issue.Namespace,
		"pod":       issue.ResourceName,
	}).Warn("OOMKilled detected: consider increasing memory limits")

	// Delete pod to restart
	err := mr.clientset.CoreV1().Pods(issue.Namespace).Delete(ctx, issue.ResourceName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	mr.log.Info("Pod deleted, but OOM may recur without memory limit increase")
	return nil
}

// remediateGeneric handles generic issues
func (mr *ManualRemediator) remediateGeneric(ctx context.Context, issue *models.Issue) error {
	mr.log.WithFields(logrus.Fields{
		"namespace":  issue.Namespace,
		"resource":   issue.ResourceName,
		"issue_type": issue.Type,
	}).Info("Generic remediation: restarting pod")

	err := mr.clientset.CoreV1().Pods(issue.Namespace).Delete(ctx, issue.ResourceName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	return nil
}
```

### 6. Metrics

Package: `internal/remediation/metrics.go`

```go
package remediation

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	RemediationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_remediation_total",
			Help: "Total number of remediation attempts",
		},
		[]string{"method", "issue_type", "status"},
	)

	RemediationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_remediation_duration_seconds",
			Help:    "Duration of remediation operations",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		},
		[]string{"method", "issue_type"},
	)

	StrategySelectionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_strategy_selection_total",
			Help: "Total number of strategy selections",
		},
		[]string{"deployment_method", "remediator"},
	)

	RemediationSuccessRate = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "coordination_engine_remediation_success_rate",
			Help: "Success rate of remediation by method",
		},
		[]string{"method"},
	)
)
```

## Configuration

Environment variables:
```bash
HELM_BINARY_PATH=/usr/local/bin/helm    # Helm binary location
HELM_TIMEOUT=5m                          # Helm operation timeout
OPERATOR_RECONCILE_TIMEOUT=5m            # Time to wait for operator reconciliation
MANUAL_REMEDIATION_ENABLED=true          # Enable manual remediation fallback
```

## Testing Strategy

### Unit Tests

```go
func TestStrategySelector_SelectRemediator_ArgoCD(t *testing.T) {
	argocdRemed := &MockRemediator{name: "argocd", canHandle: true}
	helmRemed := &MockRemediator{name: "helm", canHandle: false}

	selector := NewStrategySelector(argocdRemed, helmRemed, nil, nil, logrus.New())

	deploymentInfo := &models.DeploymentInfo{
		Method: models.DeploymentMethodArgoCD,
		Managed: true,
	}

	selected := selector.SelectRemediator(deploymentInfo)

	assert.Equal(t, "argocd", selected.Name())
}

func TestHelmRemediator_Remediate_Rollback(t *testing.T) {
	// Mock Helm client that returns "failed" status
	// Test that rollback is triggered
}

func TestOperatorRemediator_FindOwningCR(t *testing.T) {
	// Create fake pod with CR owner reference
	// Test that CR is correctly identified
}

func TestManualRemediator_RemediateCrashLoop(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	remediator := NewManualRemediator(clientset, logrus.New())

	// Create test pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}
	clientset.CoreV1().Pods("default").Create(context.Background(), pod, metav1.CreateOptions{})

	issue := &models.Issue{
		Type:        "CrashLoopBackOff",
		Namespace:   "default",
		ResourceName: "test-pod",
	}

	err := remediator.remediateCrashLoop(context.Background(), issue)

	assert.NoError(t, err)

	// Verify pod was deleted
	_, err = clientset.CoreV1().Pods("default").Get(context.Background(), "test-pod", metav1.GetOptions{})
	assert.True(t, errors.IsNotFound(err))
}
```

### Integration Tests

```go
func TestRemediationStrategies_HelmIntegration(t *testing.T) {
	// Requires Helm-deployed test application
	// Test:
	// 1. Deploy Helm chart
	// 2. Simulate pod failure
	// 3. Trigger remediation
	// 4. Verify Helm upgrade executed
	// 5. Verify pod recovered
}

func TestRemediationStrategies_OperatorIntegration(t *testing.T) {
	// Requires operator-managed test application
	// Test:
	// 1. Deploy operator and CR
	// 2. Simulate pod failure
	// 3. Trigger remediation
	// 4. Verify CR annotation updated
	// 5. Verify operator reconciled
}
```

## Performance Characteristics

- **Strategy Selection**: <10ms (in-memory decision)
- **Helm Remediation**: 30s - 5m (depends on chart complexity)
- **Operator Remediation**: 10s - 2m (depends on operator reconciliation speed)
- **Manual Remediation**: 5s - 30s (direct Kubernetes API calls)
- **Detection Accuracy**: >90% for ArgoCD/Helm, >80% for Operator

## Consequences

### Positive
- ✅ **Deployment Awareness**: Respects how applications were deployed
- ✅ **Tool Compatibility**: Works with Helm, Operators, and manual deployments
- ✅ **Conflict Avoidance**: No reconciliation loop conflicts
- ✅ **Audit Trail**: Remediation tracked in deployment tools
- ✅ **Rollback Support**: Leverages Helm/Operator rollback mechanisms
- ✅ **Flexibility**: Handles mixed deployment environments

### Negative
- ⚠️ **Complexity**: Multiple remediation paths increase code complexity
- ⚠️ **Testing Overhead**: Must test all deployment method combinations
- ⚠️ **Helm Dependency**: Requires Helm CLI binary for Helm remediator
- ⚠️ **Operator Discovery**: CR owner discovery may fail for complex ownership chains

### Mitigation
- **Complexity**: Clear interface boundaries, comprehensive documentation
- **Testing**: Integration test suite covering all deployment methods
- **Helm Dependency**: Consider Helm Go SDK for programmatic access
- **Operator Discovery**: Fallback to manual remediation if CR not found

## References

- Platform ADR-039: Non-ArgoCD Application Remediation Strategy (overall design)
- ADR-001: Go Project Architecture (package organization)
- ADR-002: Deployment Detection Implementation (detection logic)
- ADR-004: ArgoCD/MCO Integration (ArgoCD remediator)
- Helm SDK: https://helm.sh/docs/topics/advanced/
- Kubernetes Operator: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/

## Related ADRs

- Platform ADR-039: Non-ArgoCD Application Remediation Strategy
- ADR-001: Go Project Architecture
- ADR-002: Deployment Detection Implementation
- ADR-004: ArgoCD/MCO Integration
- ADR-003: Multi-Layer Coordination Implementation (remediation execution)
