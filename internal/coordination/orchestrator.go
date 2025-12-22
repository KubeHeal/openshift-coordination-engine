package coordination

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"github.com/tosin2013/openshift-coordination-engine/internal/detector"
	"github.com/tosin2013/openshift-coordination-engine/internal/remediation"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// MultiLayerOrchestrator executes multi-layer remediation plans
type MultiLayerOrchestrator struct {
	healthChecker    *HealthChecker
	detector         *detector.Detector
	strategySelector remediation.Remediator
	clientset        kubernetes.Interface
	log              *logrus.Logger
}

// NewMultiLayerOrchestrator creates a new multi-layer orchestrator
func NewMultiLayerOrchestrator(
	healthChecker *HealthChecker,
	det *detector.Detector,
	strategySelector remediation.Remediator,
	clientset kubernetes.Interface,
	log *logrus.Logger,
) *MultiLayerOrchestrator {
	return &MultiLayerOrchestrator{
		healthChecker:    healthChecker,
		detector:         det,
		strategySelector: strategySelector,
		clientset:        clientset,
		log:              log,
	}
}

// ExecutionResult contains the result of plan execution
type ExecutionResult struct {
	Status        string    `json:"status"` // success, failed, rolled_back
	Reason        string    `json:"reason,omitempty"`
	ExecutedSteps int       `json:"executed_steps"`
	FailedStep    *int      `json:"failed_step,omitempty"`
	CompletedAt   time.Time `json:"completed_at"`
}

// ExecutePlan executes a remediation plan with health checkpoints
// Returns execution result and error
func (mlo *MultiLayerOrchestrator) ExecutePlan(ctx context.Context, plan *models.RemediationPlan) (*ExecutionResult, error) {
	mlo.log.WithFields(logrus.Fields{
		"plan_id":     plan.ID,
		"total_steps": len(plan.Steps),
		"layers":      plan.Layers,
	}).Info("Starting multi-layer remediation plan execution")

	// Record plan execution start
	RecordPlanExecutionStart()
	startTime := time.Now()

	plan.MarkExecuting()
	executedSteps := []models.RemediationStep{}

	for i, step := range plan.Steps {
		// Execute step
		mlo.log.WithFields(logrus.Fields{
			"step":        step.Order,
			"layer":       step.Layer,
			"action_type": step.ActionType,
			"target":      step.Target,
		}).Info("Executing remediation step")

		if err := mlo.executeStep(ctx, &step); err != nil {
			mlo.log.WithError(err).WithField("step", step.Order).Error("Step execution failed")

			// For non-required steps, log warning but continue
			if !step.Required {
				mlo.log.WithField("step", step.Order).Warn("Non-required step failed, continuing")
				continue
			}

			// Rollback executed steps
			plan.MarkFailed()
			rollbackStart := time.Now()
			if err := mlo.rollbackSteps(ctx, executedSteps); err != nil {
				mlo.log.WithError(err).Error("Rollback failed")
				plan.MarkRolledBack()
			}
			rollbackDuration := time.Since(rollbackStart).Seconds()
			RecordRollback("step_failed", len(executedSteps), rollbackDuration)

			// Record plan execution failure
			duration := time.Since(startTime).Seconds()
			RecordPlanExecutionEnd("failed", len(plan.Layers), duration)

			failedStep := i
			return &ExecutionResult{
				Status:        "failed",
				Reason:        err.Error(),
				ExecutedSteps: len(executedSteps),
				FailedStep:    &failedStep,
				CompletedAt:   time.Now(),
			}, err
		}

		executedSteps = append(executedSteps, step)
		plan.AdvanceStep()

		// Wait for step to settle
		if step.WaitTime > 0 {
			mlo.log.WithField("wait_time", step.WaitTime).Debug("Waiting for step to settle")
			select {
			case <-time.After(step.WaitTime):
			case <-ctx.Done():
				// Record plan execution cancellation
				duration := time.Since(startTime).Seconds()
				RecordPlanExecutionEnd("cancelled", len(plan.Layers), duration)

				return &ExecutionResult{
					Status:        "failed",
					Reason:        "context cancelled",
					ExecutedSteps: len(executedSteps),
					CompletedAt:   time.Now(),
				}, fmt.Errorf("plan execution cancelled: %w", ctx.Err())
			}
		}

		// Check for health checkpoint after this step
		if checkpoint := plan.GetCheckpointAfterStep(step.Order); checkpoint != nil {
			mlo.log.WithFields(logrus.Fields{
				"layer":  checkpoint.Layer,
				"checks": len(checkpoint.Checks),
			}).Info("Verifying health checkpoint")

			checkpointStart := time.Now()
			if err := mlo.verifyCheckpoint(ctx, checkpoint); err != nil {
				checkpointDuration := time.Since(checkpointStart).Seconds()
				RecordHealthCheckpoint(checkpoint.Layer, checkpointDuration, false)
				mlo.log.WithError(err).Error("Health checkpoint failed")

				// For non-required checkpoints, log warning but continue
				if !checkpoint.Required {
					mlo.log.Warn("Non-required checkpoint failed, continuing")
					continue
				}

				// Rollback executed steps
				plan.MarkFailed()
				rollbackStart := time.Now()
				if err := mlo.rollbackSteps(ctx, executedSteps); err != nil {
					mlo.log.WithError(err).Error("Rollback failed")
					plan.MarkRolledBack()
				}
				rollbackDuration := time.Since(rollbackStart).Seconds()
				RecordRollback("checkpoint_failed", len(executedSteps), rollbackDuration)

				// Record plan execution failure
				duration := time.Since(startTime).Seconds()
				RecordPlanExecutionEnd("failed", len(plan.Layers), duration)

				failedStep := i
				return &ExecutionResult{
					Status:        "failed",
					Reason:        fmt.Sprintf("checkpoint failed: %v", err),
					ExecutedSteps: len(executedSteps),
					FailedStep:    &failedStep,
					CompletedAt:   time.Now(),
				}, err
			}

			// Record successful checkpoint
			checkpointDuration := time.Since(checkpointStart).Seconds()
			RecordHealthCheckpoint(checkpoint.Layer, checkpointDuration, true)
		}
	}

	mlo.log.Info("Multi-layer remediation plan completed successfully")
	plan.MarkCompleted()

	// Record plan execution success
	duration := time.Since(startTime).Seconds()
	RecordPlanExecutionEnd("success", len(plan.Layers), duration)

	return &ExecutionResult{
		Status:        "success",
		ExecutedSteps: len(executedSteps),
		CompletedAt:   time.Now(),
	}, nil
}

// executeStep performs a single remediation action
func (mlo *MultiLayerOrchestrator) executeStep(ctx context.Context, step *models.RemediationStep) error {
	mlo.log.WithFields(logrus.Fields{
		"action": step.ActionType,
		"target": step.Target,
		"layer":  step.Layer,
	}).Info("Executing remediation step")

	switch step.Layer {
	case models.LayerInfrastructure:
		return mlo.executeInfrastructureStep(ctx, step)
	case models.LayerPlatform:
		return mlo.executePlatformStep(ctx, step)
	case models.LayerApplication:
		return mlo.executeApplicationStep(ctx, step)
	default:
		return fmt.Errorf("unknown layer: %s", step.Layer)
	}
}

// executeInfrastructureStep executes infrastructure layer remediation
//
//nolint:unparam // ctx kept for API consistency and future use
func (mlo *MultiLayerOrchestrator) executeInfrastructureStep(ctx context.Context, step *models.RemediationStep) error {
	mlo.log.WithFields(logrus.Fields{
		"action": step.ActionType,
		"target": step.Target,
	}).Info("Executing infrastructure step")

	// Infrastructure steps are mostly monitoring (MCO manages node updates)
	// We verify the operations are progressing correctly rather than triggering them
	switch step.ActionType {
	case "monitor_mco", "monitor_machineconfig", "monitor_mcp":
		// Monitor MCO progress - these are passive monitoring operations
		// The actual remediation happens via MCO automatically
		mlo.log.WithField("target", step.Target).Info("Monitoring MCO operation")
		return nil

	default:
		mlo.log.WithField("action", step.ActionType).Warn("Unknown infrastructure action type")
		return nil // Non-critical, continue execution
	}
}

// executePlatformStep executes platform layer remediation
//
//nolint:unparam // ctx kept for API consistency and future use
func (mlo *MultiLayerOrchestrator) executePlatformStep(ctx context.Context, step *models.RemediationStep) error {
	mlo.log.WithFields(logrus.Fields{
		"action": step.ActionType,
		"target": step.Target,
	}).Info("Executing platform step")

	switch step.ActionType {
	case "trigger_operator_reconciliation":
		// Platform operators handle their own reconciliation
		// We just verify the operator is healthy
		mlo.log.WithField("target", step.Target).Info("Monitoring operator reconciliation")
		return nil

	case "monitor_clusteroperator":
		// Monitor ClusterOperator status - passive monitoring
		mlo.log.WithField("target", step.Target).Info("Monitoring ClusterOperator status")
		return nil

	default:
		mlo.log.WithField("action", step.ActionType).Warn("Unknown platform action type")
		return nil // Non-critical, continue execution
	}
}

// executeApplicationStep executes application layer remediation using remediators
func (mlo *MultiLayerOrchestrator) executeApplicationStep(ctx context.Context, step *models.RemediationStep) error {
	mlo.log.WithFields(logrus.Fields{
		"action": step.ActionType,
		"target": step.Target,
	}).Info("Executing application step")

	// Parse namespace and resource name from target (format: "namespace/name")
	namespace, resourceName, err := parseTarget(step.Target)
	if err != nil {
		return fmt.Errorf("invalid target format: %w", err)
	}

	// Determine resource kind from metadata
	resourceKind := step.Metadata["deployment"]
	if resourceKind == "" {
		resourceKind = step.Metadata["statefulset"]
	}
	if resourceKind == "" {
		resourceKind = step.Metadata["pod"]
	}
	if resourceKind == "" {
		resourceKind = "Deployment" // Default assumption
	}

	// Create issue for remediation
	issue := &models.Issue{
		ID:           fmt.Sprintf("step-%d", step.Order),
		Type:         mapActionTypeToIssueType(step.ActionType),
		Description:  step.Description,
		Namespace:    namespace,
		ResourceName: resourceName,
		ResourceType: resourceKind,
		Severity:     "medium",
	}

	// Detect deployment method
	deploymentInfo, err := mlo.detector.DetectByKind(ctx, namespace, resourceName, resourceKind)
	if err != nil {
		mlo.log.WithError(err).Warn("Failed to detect deployment method, using manual remediation")
		deploymentInfo = models.NewDeploymentInfo(
			namespace,
			resourceName,
			resourceKind,
			models.DeploymentMethodUnknown,
			0.5,
		)
	}

	// Execute remediation using strategy selector
	mlo.log.WithFields(logrus.Fields{
		"namespace":         namespace,
		"resource":          resourceName,
		"kind":              resourceKind,
		"deployment_method": deploymentInfo.Method,
	}).Info("Executing application remediation")

	if err := mlo.strategySelector.Remediate(ctx, deploymentInfo, issue); err != nil {
		return fmt.Errorf("application remediation failed: %w", err)
	}
	return nil
}

// parseTarget parses "namespace/name" format
func parseTarget(target string) (namespace, name string, err error) {
	parts := splitTarget(target)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("target must be in format 'namespace/name', got: %s", target)
	}
	return parts[0], parts[1], nil
}

// splitTarget splits on "/" character
func splitTarget(target string) []string {
	result := []string{}
	current := ""
	for _, ch := range target {
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

// mapActionTypeToIssueType maps remediation step action types to issue types
func mapActionTypeToIssueType(actionType string) string {
	switch actionType {
	case "restart_pod":
		return "pod_crash_loop"
	case "restart_deployment":
		return "deployment_not_ready"
	case "restart_statefulset":
		return "statefulset_not_ready"
	default:
		return "generic_issue"
	}
}

// verifyCheckpoint checks health conditions for a layer
func (mlo *MultiLayerOrchestrator) verifyCheckpoint(ctx context.Context, checkpoint *models.HealthCheckpoint) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, checkpoint.Timeout)
	defer cancel()

	switch checkpoint.Layer {
	case models.LayerInfrastructure:
		return mlo.healthChecker.CheckInfrastructureHealth(timeoutCtx)
	case models.LayerPlatform:
		return mlo.healthChecker.CheckPlatformHealth(timeoutCtx)
	case models.LayerApplication:
		return mlo.healthChecker.CheckApplicationHealth(timeoutCtx)
	default:
		return fmt.Errorf("unknown layer: %s", checkpoint.Layer)
	}
}

// rollbackSteps executes rollback in reverse order
//
//nolint:unparam // error return kept for future error aggregation
func (mlo *MultiLayerOrchestrator) rollbackSteps(ctx context.Context, steps []models.RemediationStep) error {
	mlo.log.WithField("steps", len(steps)).Warn("Starting coordinated rollback")

	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]

		mlo.log.WithFields(logrus.Fields{
			"step":  step.Order,
			"layer": step.Layer,
		}).Info("Rolling back step")

		// Execute rollback action
		if err := mlo.executeRollback(ctx, &step); err != nil {
			mlo.log.WithError(err).Error("Rollback step failed")
			// Continue with remaining rollback steps
		}

		// Brief wait between rollback steps
		time.Sleep(500 * time.Millisecond)
	}

	mlo.log.Info("Coordinated rollback completed")
	return nil
}

// executeRollback performs rollback for a single step
func (mlo *MultiLayerOrchestrator) executeRollback(ctx context.Context, step *models.RemediationStep) error {
	mlo.log.WithFields(logrus.Fields{
		"action": "rollback_" + step.ActionType,
		"target": step.Target,
		"layer":  step.Layer,
	}).Info("Executing rollback")

	// Rollback strategy depends on layer and action type
	switch step.Layer {
	case models.LayerApplication:
		return mlo.rollbackApplicationStep(ctx, step)
	case models.LayerPlatform:
		// Platform rollbacks are typically handled by operators automatically
		mlo.log.WithField("target", step.Target).Info("Platform rollback handled by operator")
		return nil
	case models.LayerInfrastructure:
		// Infrastructure rollbacks are handled by MCO automatically
		mlo.log.WithField("target", step.Target).Info("Infrastructure rollback handled by MCO")
		return nil
	default:
		mlo.log.WithField("layer", step.Layer).Warn("Unknown layer for rollback")
		return nil // Non-critical
	}
}

// rollbackApplicationStep rolls back application layer changes
//
//nolint:unparam // ctx kept for API consistency and future use
func (mlo *MultiLayerOrchestrator) rollbackApplicationStep(ctx context.Context, step *models.RemediationStep) error {
	mlo.log.WithFields(logrus.Fields{
		"action": step.ActionType,
		"target": step.Target,
	}).Info("Rolling back application step")

	// Parse target
	namespace, resourceName, err := parseTarget(step.Target)
	if err != nil {
		mlo.log.WithError(err).Warn("Invalid target for rollback, skipping")
		return nil // Non-critical, continue rollback
	}

	// For application layer, rollback typically means reverting to previous state
	// Most remediators (Helm, ArgoCD) support automatic rollback
	// For manual restarts, we don't need explicit rollback (the damage is done)

	switch step.ActionType {
	case "restart_deployment", "restart_statefulset", "restart_pod":
		// For restarts, rollback would be reverting to previous version if available
		// Since we don't track previous versions here, we log and continue
		mlo.log.WithFields(logrus.Fields{
			"namespace": namespace,
			"resource":  resourceName,
			"action":    step.ActionType,
		}).Warn("Cannot rollback restart operation, change is permanent")
		return nil

	default:
		mlo.log.WithField("action", step.ActionType).Warn("Unknown action type for rollback")
		return nil
	}
}
