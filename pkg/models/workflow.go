package models

import "time"

// WorkflowStatus represents the current state of a remediation workflow
type WorkflowStatus string

// Workflow status constants
const (
	WorkflowStatusPending   WorkflowStatus = "pending"
	WorkflowStatusRunning   WorkflowStatus = "in_progress"
	WorkflowStatusCompleted WorkflowStatus = "completed"
	WorkflowStatusFailed    WorkflowStatus = "failed"
)

// Workflow represents a remediation workflow execution
type Workflow struct {
	ID               string         `json:"id"`
	IncidentID       string         `json:"incident_id"`
	Status           WorkflowStatus `json:"status"`
	DeploymentMethod string         `json:"deployment_method"`
	Namespace        string         `json:"namespace"`
	ResourceName     string         `json:"resource_name"`
	ResourceKind     string         `json:"resource_kind"`
	IssueType        string         `json:"issue_type"`
	Remediator       string         `json:"remediator,omitempty"`
	ErrorMessage     string         `json:"error_message,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	StartedAt        *time.Time     `json:"started_at,omitempty"`
	CompletedAt      *time.Time     `json:"completed_at,omitempty"`
	Steps            []WorkflowStep `json:"steps,omitempty"`
}

// WorkflowStep represents a single step in the workflow
type WorkflowStep struct {
	Order        int        `json:"order"`
	Layer        string     `json:"layer,omitempty"` // "infrastructure", "platform", "application"
	Description  string     `json:"description"`
	Status       string     `json:"status"` // "pending", "running", "completed", "failed"
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
}

// Duration returns the workflow execution duration
func (w *Workflow) Duration() time.Duration {
	if w.StartedAt == nil {
		return 0
	}

	end := time.Now()
	if w.CompletedAt != nil {
		end = *w.CompletedAt
	}

	return end.Sub(*w.StartedAt)
}

// AddStep adds a new step to the workflow
func (w *Workflow) AddStep(description string) *WorkflowStep {
	step := WorkflowStep{
		Order:       len(w.Steps),
		Description: description,
		Status:      "pending",
	}
	w.Steps = append(w.Steps, step)
	return &w.Steps[len(w.Steps)-1]
}

// IsActive returns true if workflow is currently running
func (w *Workflow) IsActive() bool {
	return w.Status == WorkflowStatusPending || w.Status == WorkflowStatusRunning
}
