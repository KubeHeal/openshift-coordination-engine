package remediation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/KubeHeal/openshift-coordination-engine/internal/detector"
	"github.com/KubeHeal/openshift-coordination-engine/internal/storage"
	"github.com/KubeHeal/openshift-coordination-engine/pkg/models"
)

// Orchestrator manages remediation workflow execution
type Orchestrator struct {
	detector   *detector.Detector
	remediator Remediator
	workflows  map[string]*models.Workflow
	store      *storage.WorkflowStore
	mu         sync.RWMutex
	log        *logrus.Logger
}

const defaultCheckpointTimeout = time.Minute

// NewOrchestrator creates a new remediation orchestrator.
// When dataDir is set, workflows are loaded from and written to {dataDir}/workflows.
func NewOrchestrator(
	det *detector.Detector,
	remediator Remediator,
	log *logrus.Logger,
	dataDir string,
) *Orchestrator {
	if log == nil {
		log = logrus.New()
	}
	orchestrator := &Orchestrator{
		detector:   det,
		remediator: remediator,
		workflows:  make(map[string]*models.Workflow),
		log:        log,
	}
	if strings.TrimSpace(dataDir) == "" {
		return orchestrator
	}
	store, err := storage.NewWorkflowStore(dataDir, log)
	if err != nil {
		log.WithError(err).Error("workflow persistence unavailable; workflows will be in-memory")
		return orchestrator
	}
	orchestrator.store = store
	loaded, err := store.Load()
	if err != nil {
		log.WithError(err).Error("failed to load workflows")
		return orchestrator
	}
	for _, workflow := range loaded {
		orchestrator.workflows[workflow.ID] = workflow
	}
	log.WithFields(logrus.Fields{
		"dir":   dataDir,
		"count": len(loaded),
	}).Info("loaded remediation workflows")
	return orchestrator
}

// TriggerRemediation initiates a remediation workflow
func (o *Orchestrator) TriggerRemediation(ctx context.Context, incidentID string, issue *models.Issue) (*models.Workflow, error) {
	o.log.WithFields(logrus.Fields{
		"incident_id": incidentID,
		"issue_type":  issue.Type,
		"namespace":   issue.Namespace,
		"resource":    issue.ResourceName,
	}).Info("Triggering remediation workflow")

	// Validate issue
	if err := issue.Validate(); err != nil {
		return nil, fmt.Errorf("invalid issue: %w", err)
	}

	deploymentInfo := o.deploymentForIssue(ctx, issue)

	// Create workflow and persist it before the request returns.
	workflow := o.createWorkflow(incidentID, issue, deploymentInfo)
	o.saveWorkflow(workflow)

	// Execute remediation in background — context.Background is intentional: the
	// workflow outlives the HTTP request that triggered it.
	go o.executeWorkflow(context.Background(), workflow, deploymentInfo, issue) // #nosec G118 -- background goroutine must not use request-scoped context

	return workflow, nil
}

// PreviewRemediation detects how a workflow would run without storing or executing it.
func (o *Orchestrator) PreviewRemediation(ctx context.Context, issue *models.Issue) (string, string, error) {
	if err := issue.Validate(); err != nil {
		return "", "", fmt.Errorf("invalid issue: %w", err)
	}
	info := o.deploymentForIssue(ctx, issue)
	return string(info.Method), formatMinutes(defaultCheckpointTimeout), nil
}

func (o *Orchestrator) deploymentForIssue(ctx context.Context, issue *models.Issue) *models.DeploymentInfo {
	info, err := o.detectDeploymentMethod(ctx, issue)
	if err != nil {
		o.log.WithError(err).Warn("Failed to detect deployment method, using manual remediation")
		return models.NewDeploymentInfo(
			issue.Namespace,
			issue.ResourceName,
			issue.ResourceType,
			models.DeploymentMethodUnknown,
			0.5,
		)
	}
	return info
}

// GetWorkflow retrieves a workflow by ID
func (o *Orchestrator) GetWorkflow(workflowID string) (*models.Workflow, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	workflow, exists := o.workflows[workflowID]
	if !exists {
		return nil, fmt.Errorf("workflow not found: %s", workflowID)
	}

	return workflow, nil
}

// ListWorkflows returns all workflows
func (o *Orchestrator) ListWorkflows() []*models.Workflow {
	o.mu.RLock()
	defer o.mu.RUnlock()

	workflows := make([]*models.Workflow, 0, len(o.workflows))
	for _, wf := range o.workflows {
		workflows = append(workflows, wf)
	}

	return workflows
}

// createWorkflow creates a new workflow instance
func (o *Orchestrator) createWorkflow(incidentID string, issue *models.Issue, deploymentInfo *models.DeploymentInfo) *models.Workflow {
	layer := layerForResource(issue.ResourceType)
	workflow := &models.Workflow{
		ID:                generateWorkflowID(),
		IncidentID:        incidentID,
		Status:            models.WorkflowStatusPending,
		DeploymentMethod:  string(deploymentInfo.Method),
		Namespace:         issue.Namespace,
		ResourceName:      issue.ResourceName,
		ResourceKind:      issue.ResourceType,
		IssueType:         issue.Type,
		Severity:          issue.Severity,
		EstimatedDuration: formatMinutes(defaultCheckpointTimeout),
		Layers:            []string{layer},
		Checkpoints: []models.WorkflowCheckpoint{{
			Layer:     layer,
			AfterStep: 1,
			Status:    models.CheckpointPending,
			Checks:    []string{"pods_running", "endpoints_healthy"},
		}},
		CreatedAt: time.Now(),
	}

	step := workflow.AddStep(fmt.Sprintf("Detect deployment method for %s/%s", issue.Namespace, issue.ResourceName))
	step.Layer = layer

	return workflow
}

// executeWorkflow executes the remediation workflow
func (o *Orchestrator) executeWorkflow(ctx context.Context, workflow *models.Workflow, deploymentInfo *models.DeploymentInfo, issue *models.Issue) {
	o.log.WithField("workflow_id", workflow.ID).Info("Starting workflow execution")

	// Record workflow start metrics
	RecordWorkflowStart()

	// Update workflow status
	o.updateWorkflowStatus(workflow, models.WorkflowStatusRunning)
	startTime := time.Now()
	workflow.StartedAt = &startTime

	// Add remediation step
	step := workflow.AddStep(fmt.Sprintf("Execute %s remediation for %s", o.remediator.Name(), issue.Type))
	if len(workflow.Layers) > 0 {
		step.Layer = workflow.Layers[0]
	}
	workflow.Remediator = o.remediator.Name()

	// Save workflow state
	o.saveWorkflow(workflow)

	// Execute remediation
	err := o.remediator.Remediate(ctx, deploymentInfo, issue)

	completedTime := time.Now()
	workflow.CompletedAt = &completedTime
	duration := completedTime.Sub(startTime).Seconds()

	if err != nil {
		o.log.WithError(err).Error("Remediation failed")
		workflow.Status = models.WorkflowStatusFailed
		workflow.ErrorMessage = err.Error()
		step.Status = "failed"
		step.ErrorMessage = err.Error()
		markCheckpoints(workflow, models.CheckpointFailed)

		// Record remediation failure metrics
		RecordRemediation(o.remediator.Name(), string(deploymentInfo.Method), issue.Type, duration, false)
		RecordRemediationFailure(o.remediator.Name(), string(deploymentInfo.Method), issue.Type, "remediation_error")
		RecordWorkflowEnd("failed")
	} else {
		o.log.Info("Remediation completed successfully")
		workflow.Status = models.WorkflowStatusCompleted
		step.Status = "completed"
		step.CompletedAt = &completedTime
		markCheckpoints(workflow, models.CheckpointPassed)

		// Record remediation success metrics
		RecordRemediation(o.remediator.Name(), string(deploymentInfo.Method), issue.Type, duration, true)
		RecordWorkflowEnd("completed")
	}

	// Save final workflow state
	o.saveWorkflow(workflow)

	o.log.WithFields(logrus.Fields{
		"workflow_id": workflow.ID,
		"status":      workflow.Status,
		"duration":    workflow.Duration().String(),
	}).Info("Workflow execution completed")
}

// detectDeploymentMethod detects how the resource was deployed
func (o *Orchestrator) detectDeploymentMethod(ctx context.Context, issue *models.Issue) (*models.DeploymentInfo, error) {
	// Map issue resource type to Kubernetes kind
	var kind string
	switch issue.ResourceType {
	case "deployment", "Deployment":
		kind = "Deployment"
	case "statefulset", "StatefulSet":
		kind = "StatefulSet"
	case "daemonset", "DaemonSet":
		kind = "DaemonSet"
	case "pod", "Pod":
		// For pods, we need to find the owner
		kind = "Deployment" // Default assumption
	default:
		kind = "Deployment"
	}

	deploymentInfo, err := o.detector.DetectByKind(ctx, issue.Namespace, issue.ResourceName, kind)
	if err != nil {
		return nil, fmt.Errorf("failed to detect deployment info: %w", err)
	}
	return deploymentInfo, nil
}

// updateWorkflowStatus updates the workflow status
func (o *Orchestrator) updateWorkflowStatus(workflow *models.Workflow, status models.WorkflowStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()
	workflow.Status = status
}

// saveWorkflow persists workflow state in memory and, when configured, on disk.
func (o *Orchestrator) saveWorkflow(workflow *models.Workflow) {
	o.mu.Lock()
	o.workflows[workflow.ID] = workflow
	var encoded []byte
	var marshalErr error
	if o.store != nil {
		encoded, marshalErr = json.MarshalIndent(workflow, "", "  ")
	}
	store := o.store
	o.mu.Unlock()
	if store == nil {
		return
	}
	if marshalErr != nil {
		o.log.WithError(marshalErr).WithField("workflow_id", workflow.ID).Error("failed to encode workflow")
		return
	}
	if err := store.SaveBytes(workflow.ID, encoded); err != nil {
		o.log.WithError(err).WithField("workflow_id", workflow.ID).Error("failed to persist workflow")
	}
}

func layerForResource(kind string) string {
	switch strings.ToLower(kind) {
	case "deployment", "statefulset", "daemonset", "pod":
		return "application"
	default:
		return "platform"
	}
}

func formatMinutes(duration time.Duration) string {
	minutes := int(duration.Round(time.Minute) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("%dm", minutes)
}

func markCheckpoints(workflow *models.Workflow, status string) {
	for i := range workflow.Checkpoints {
		workflow.Checkpoints[i].Status = status
	}
}

// generateWorkflowID generates a unique workflow ID
func generateWorkflowID() string {
	return "wf-" + uuid.New().String()[:8]
}
