package models

import (
	"fmt"
	"time"
)

// RemediationStep represents a single remediation action in a multi-layer plan
type RemediationStep struct {
	Layer       Layer             `json:"layer"`
	Order       int               `json:"order"`
	Description string            `json:"description"`
	ActionType  string            `json:"action_type"` // restart, rollback, scale, drain, etc.
	Target      string            `json:"target"`      // Resource identifier
	WaitTime    time.Duration     `json:"wait_time"`   // Time to wait after this step
	Required    bool              `json:"required"`    // If false, continue on failure
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// String returns a human-readable representation of the step
func (rs *RemediationStep) String() string {
	return fmt.Sprintf("Step %d [%s]: %s (%s on %s)",
		rs.Order, rs.Layer, rs.Description, rs.ActionType, rs.Target)
}

// HealthCheckpoint verifies layer health after remediation steps
type HealthCheckpoint struct {
	Layer     Layer         `json:"layer"`
	AfterStep int           `json:"after_step"`
	Checks    []string      `json:"checks"`   // Health check descriptions
	Timeout   time.Duration `json:"timeout"`  // Max time to wait for health
	Required  bool          `json:"required"` // If false, continue on failure
}

// String returns a human-readable representation of the checkpoint
func (hc *HealthCheckpoint) String() string {
	return fmt.Sprintf("Checkpoint [%s] after step %d: %d checks",
		hc.Layer, hc.AfterStep, len(hc.Checks))
}

// RemediationPlan contains ordered steps with health checkpoints for multi-layer remediation
type RemediationPlan struct {
	ID            string             `json:"id"`
	IssueID       string             `json:"issue_id"`
	Layers        []Layer            `json:"layers"`
	Steps         []RemediationStep  `json:"steps"`
	Checkpoints   []HealthCheckpoint `json:"checkpoints"`
	RollbackSteps []RemediationStep  `json:"rollback_steps,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
	Status        string             `json:"status"` // pending, executing, completed, failed, rolled_back
	CurrentStep   int                `json:"current_step"`
}

// NewRemediationPlan creates a new remediation plan
func NewRemediationPlan(issueID string, layers []Layer) *RemediationPlan {
	return &RemediationPlan{
		ID:            fmt.Sprintf("plan-%d", time.Now().UnixNano()),
		IssueID:       issueID,
		Layers:        layers,
		Steps:         []RemediationStep{},
		Checkpoints:   []HealthCheckpoint{},
		RollbackSteps: []RemediationStep{},
		CreatedAt:     time.Now(),
		Status:        "pending",
		CurrentStep:   0,
	}
}

// AddStep adds a remediation step to the plan
func (rp *RemediationPlan) AddStep(step *RemediationStep) {
	// Auto-assign order if not set
	if step.Order == 0 {
		step.Order = len(rp.Steps) + 1
	}
	rp.Steps = append(rp.Steps, *step)
}

// AddCheckpoint adds a health checkpoint to the plan
func (rp *RemediationPlan) AddCheckpoint(checkpoint HealthCheckpoint) {
	rp.Checkpoints = append(rp.Checkpoints, checkpoint)
}

// AddRollbackStep adds a rollback step to the plan
func (rp *RemediationPlan) AddRollbackStep(step *RemediationStep) {
	rp.RollbackSteps = append(rp.RollbackSteps, *step)
}

// GetStepsForLayer returns all steps for a specific layer
func (rp *RemediationPlan) GetStepsForLayer(layer Layer) []RemediationStep {
	var steps []RemediationStep
	for _, step := range rp.Steps {
		if step.Layer == layer {
			steps = append(steps, step)
		}
	}
	return steps
}

// GetCheckpointAfterStep returns the checkpoint after a specific step
func (rp *RemediationPlan) GetCheckpointAfterStep(stepOrder int) *HealthCheckpoint {
	for i := range rp.Checkpoints {
		if rp.Checkpoints[i].AfterStep == stepOrder {
			return &rp.Checkpoints[i]
		}
	}
	return nil
}

// GetNextStep returns the next step to execute, or nil if complete
func (rp *RemediationPlan) GetNextStep() *RemediationStep {
	if rp.CurrentStep >= len(rp.Steps) {
		return nil
	}
	return &rp.Steps[rp.CurrentStep]
}

// AdvanceStep moves to the next step
func (rp *RemediationPlan) AdvanceStep() {
	rp.CurrentStep++
}

// IsComplete returns true if all steps have been executed
func (rp *RemediationPlan) IsComplete() bool {
	return rp.CurrentStep >= len(rp.Steps)
}

// RequiresRollback returns true if the plan has rollback steps
func (rp *RemediationPlan) RequiresRollback() bool {
	return len(rp.RollbackSteps) > 0
}

// MarkExecuting marks the plan as currently executing
func (rp *RemediationPlan) MarkExecuting() {
	rp.Status = "executing"
}

// MarkCompleted marks the plan as successfully completed
func (rp *RemediationPlan) MarkCompleted() {
	rp.Status = "completed"
}

// MarkFailed marks the plan as failed
func (rp *RemediationPlan) MarkFailed() {
	rp.Status = "failed"
}

// MarkRolledBack marks the plan as rolled back
func (rp *RemediationPlan) MarkRolledBack() {
	rp.Status = "rolled_back"
}

// Validate checks if the remediation plan is valid
func (rp *RemediationPlan) Validate() error {
	if rp.ID == "" {
		return fmt.Errorf("plan ID is required")
	}

	if rp.IssueID == "" {
		return fmt.Errorf("issue ID is required")
	}

	if len(rp.Layers) == 0 {
		return fmt.Errorf("at least one layer is required")
	}

	for _, layer := range rp.Layers {
		if err := layer.Validate(); err != nil {
			return fmt.Errorf("invalid layer: %w", err)
		}
	}

	if len(rp.Steps) == 0 {
		return fmt.Errorf("at least one step is required")
	}

	// Validate step ordering
	for i, step := range rp.Steps {
		expectedOrder := i + 1
		if step.Order != expectedOrder {
			return fmt.Errorf("step %d has wrong order: expected %d, got %d",
				i, expectedOrder, step.Order)
		}
	}

	return nil
}

// String returns a human-readable representation
func (rp *RemediationPlan) String() string {
	return fmt.Sprintf("RemediationPlan[%s]: issue=%s, layers=%d, steps=%d, status=%s",
		rp.ID, rp.IssueID, len(rp.Layers), len(rp.Steps), rp.Status)
}
