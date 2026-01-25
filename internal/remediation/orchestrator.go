package remediation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/internal/detector"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// Orchestrator manages remediation workflow execution
type Orchestrator struct {
	detector   *detector.Detector
	remediator Remediator
	workflows  map[string]*models.Workflow
	mu         sync.RWMutex
	log        *logrus.Logger
}

// NewOrchestrator creates a new remediation orchestrator
func NewOrchestrator(
	det *detector.Detector,
	remediator Remediator,
	log *logrus.Logger,
) *Orchestrator {
	return &Orchestrator{
		detector:   det,
		remediator: remediator,
		workflows:  make(map[string]*models.Workflow),
		log:        log,
	}
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

	// Detect deployment method
	deploymentInfo, err := o.detectDeploymentMethod(ctx, issue)
	if err != nil {
		o.log.WithError(err).Warn("Failed to detect deployment method, using manual remediation")
		// Create unknown deployment info for manual remediation
		deploymentInfo = models.NewDeploymentInfo(
			issue.Namespace,
			issue.ResourceName,
			issue.ResourceType,
			models.DeploymentMethodUnknown,
			0.5,
		)
	}

	// Create workflow
	workflow := o.createWorkflow(incidentID, issue, deploymentInfo)

	// Store workflow
	o.mu.Lock()
	o.workflows[workflow.ID] = workflow
	o.mu.Unlock()

	// Execute remediation in background
	go o.executeWorkflow(context.Background(), workflow, deploymentInfo, issue)

	return workflow, nil
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
	workflow := &models.Workflow{
		ID:               generateWorkflowID(),
		IncidentID:       incidentID,
		Status:           models.WorkflowStatusPending,
		DeploymentMethod: string(deploymentInfo.Method),
		Namespace:        issue.Namespace,
		ResourceName:     issue.ResourceName,
		ResourceKind:     issue.ResourceType,
		IssueType:        issue.Type,
		CreatedAt:        time.Now(),
	}

	// Add initial step
	workflow.AddStep(fmt.Sprintf("Detect deployment method for %s/%s", issue.Namespace, issue.ResourceName))

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

		// Record remediation failure metrics
		RecordRemediation(o.remediator.Name(), string(deploymentInfo.Method), issue.Type, duration, false)
		RecordRemediationFailure(o.remediator.Name(), string(deploymentInfo.Method), issue.Type, "remediation_error")
		RecordWorkflowEnd("failed")
	} else {
		o.log.Info("Remediation completed successfully")
		workflow.Status = models.WorkflowStatusCompleted
		step.Status = "completed"
		step.CompletedAt = &completedTime

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

// saveWorkflow persists workflow state
func (o *Orchestrator) saveWorkflow(workflow *models.Workflow) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.workflows[workflow.ID] = workflow
}

// generateWorkflowID generates a unique workflow ID
func generateWorkflowID() string {
	return "wf-" + uuid.New().String()[:8]
}
