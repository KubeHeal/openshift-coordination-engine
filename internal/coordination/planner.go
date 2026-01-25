package coordination

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// MultiLayerPlanner generates remediation plans for multi-layer issues
type MultiLayerPlanner struct {
	log *logrus.Logger
}

// NewMultiLayerPlanner creates a new multi-layer planner
func NewMultiLayerPlanner(log *logrus.Logger) *MultiLayerPlanner {
	return &MultiLayerPlanner{
		log: log,
	}
}

// GeneratePlan creates an ordered remediation plan from a layered issue
// Steps are ordered by layer priority: Infrastructure → Platform → Application
func (mlp *MultiLayerPlanner) GeneratePlan(ctx context.Context, issue *models.LayeredIssue) (*models.RemediationPlan, error) {
	planID := "plan-" + uuid.New().String()[:8]
	startTime := time.Now()

	mlp.log.WithFields(logrus.Fields{
		"plan_id":    planID,
		"issue_id":   issue.ID,
		"layers":     issue.AffectedLayers,
		"root_cause": issue.RootCauseLayer,
	}).Info("Generating multi-layer remediation plan")

	// Get layers ordered by priority (infrastructure first)
	orderedLayers := issue.GetLayersByPriority()

	// Create plan
	plan := models.NewRemediationPlan(issue.ID, orderedLayers)
	plan.ID = planID

	// Generate steps for each layer in priority order
	stepOrder := 1
	for _, layer := range orderedLayers {
		resources := issue.GetResourcesForLayer(layer)
		layerSteps := mlp.generateStepsForLayer(layer, resources, &stepOrder)

		for i := range layerSteps {
			plan.AddStep(&layerSteps[i])
		}
	}

	// Generate health checkpoints after each layer
	checkpoints := mlp.generateCheckpoints(orderedLayers, plan.Steps)
	for _, checkpoint := range checkpoints {
		plan.AddCheckpoint(checkpoint)
	}

	// Generate rollback steps (reverse order)
	rollbackSteps := mlp.generateRollbackSteps(plan.Steps)
	for i := range rollbackSteps {
		plan.AddRollbackStep(&rollbackSteps[i])
	}

	mlp.log.WithFields(logrus.Fields{
		"plan_id":     planID,
		"total_steps": len(plan.Steps),
		"checkpoints": len(plan.Checkpoints),
		"rollbacks":   len(plan.RollbackSteps),
	}).Info("Multi-layer remediation plan generated")

	// Record metrics
	duration := time.Since(startTime).Seconds()
	RecordPlanGeneration(len(orderedLayers), duration, true)
	RecordPlanSteps(len(orderedLayers), len(plan.Steps))

	return plan, nil
}

// generateStepsForLayer creates remediation steps for a specific layer
func (mlp *MultiLayerPlanner) generateStepsForLayer(layer models.Layer, resources []models.Resource, stepOrder *int) []models.RemediationStep {
	mlp.log.WithFields(logrus.Fields{
		"layer":     layer,
		"resources": len(resources),
	}).Debug("Generating steps for layer")

	var steps []models.RemediationStep

	switch layer {
	case models.LayerInfrastructure:
		steps = mlp.generateInfrastructureSteps(resources, stepOrder)
	case models.LayerPlatform:
		steps = mlp.generatePlatformSteps(resources, stepOrder)
	case models.LayerApplication:
		steps = mlp.generateApplicationSteps(resources, stepOrder)
	}

	return steps
}

// generateInfrastructureSteps creates steps for infrastructure layer remediation
func (mlp *MultiLayerPlanner) generateInfrastructureSteps(resources []models.Resource, stepOrder *int) []models.RemediationStep {
	var steps []models.RemediationStep

	for _, resource := range resources {
		switch resource.Kind {
		case "Node":
			// For node issues, we typically monitor MCO rather than directly intervening
			step := models.RemediationStep{
				Layer:       models.LayerInfrastructure,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Monitor MCO rollout for node %s", resource.Name),
				ActionType:  "monitor_mco",
				Target:      resource.Name,
				WaitTime:    5 * time.Minute,
				Required:    true,
				Metadata:    map[string]string{"node": resource.Name},
			}
			steps = append(steps, step)
			*stepOrder++

		case "MachineConfig":
			step := models.RemediationStep{
				Layer:       models.LayerInfrastructure,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Monitor MachineConfig %s application", resource.Name),
				ActionType:  "monitor_machineconfig",
				Target:      resource.Name,
				WaitTime:    10 * time.Minute,
				Required:    true,
				Metadata:    map[string]string{"machineconfig": resource.Name},
			}
			steps = append(steps, step)
			*stepOrder++

		case "MachineConfigPool":
			step := models.RemediationStep{
				Layer:       models.LayerInfrastructure,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Monitor MachineConfigPool %s update", resource.Name),
				ActionType:  "monitor_mcp",
				Target:      resource.Name,
				WaitTime:    15 * time.Minute,
				Required:    true,
				Metadata:    map[string]string{"mcp": resource.Name},
			}
			steps = append(steps, step)
			*stepOrder++
		}
	}

	return steps
}

// generatePlatformSteps creates steps for platform layer remediation
func (mlp *MultiLayerPlanner) generatePlatformSteps(resources []models.Resource, stepOrder *int) []models.RemediationStep {
	var steps []models.RemediationStep

	for _, resource := range resources {
		if strings.Contains(resource.Kind, "Operator") {
			// For operators, we trigger reconciliation via annotation
			step := models.RemediationStep{
				Layer:       models.LayerPlatform,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Trigger reconciliation for %s", resource.Name),
				ActionType:  "trigger_operator_reconciliation",
				Target:      fmt.Sprintf("%s/%s", resource.Namespace, resource.Name),
				WaitTime:    3 * time.Minute,
				Required:    true,
				Metadata: map[string]string{
					"operator":  resource.Name,
					"namespace": resource.Namespace,
				},
			}
			steps = append(steps, step)
			*stepOrder++
		}

		if resource.Kind == "ClusterOperator" {
			step := models.RemediationStep{
				Layer:       models.LayerPlatform,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Monitor ClusterOperator %s status", resource.Name),
				ActionType:  "monitor_clusteroperator",
				Target:      resource.Name,
				WaitTime:    5 * time.Minute,
				Required:    true,
				Metadata:    map[string]string{"clusteroperator": resource.Name},
			}
			steps = append(steps, step)
			*stepOrder++
		}
	}

	return steps
}

// generateApplicationSteps creates steps for application layer remediation
func (mlp *MultiLayerPlanner) generateApplicationSteps(resources []models.Resource, stepOrder *int) []models.RemediationStep {
	var steps []models.RemediationStep

	for _, resource := range resources {
		switch resource.Kind {
		case "Pod":
			step := models.RemediationStep{
				Layer:       models.LayerApplication,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Restart pod %s/%s", resource.Namespace, resource.Name),
				ActionType:  "restart_pod",
				Target:      fmt.Sprintf("%s/%s", resource.Namespace, resource.Name),
				WaitTime:    2 * time.Minute,
				Required:    false, // Not required if infrastructure/platform fixes resolve issue
				Metadata: map[string]string{
					"pod":       resource.Name,
					"namespace": resource.Namespace,
				},
			}
			steps = append(steps, step)
			*stepOrder++

		case "Deployment":
			step := models.RemediationStep{
				Layer:       models.LayerApplication,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Restart deployment %s/%s", resource.Namespace, resource.Name),
				ActionType:  "restart_deployment",
				Target:      fmt.Sprintf("%s/%s", resource.Namespace, resource.Name),
				WaitTime:    2 * time.Minute,
				Required:    false,
				Metadata: map[string]string{
					"deployment": resource.Name,
					"namespace":  resource.Namespace,
				},
			}
			steps = append(steps, step)
			*stepOrder++

		case "StatefulSet":
			step := models.RemediationStep{
				Layer:       models.LayerApplication,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Restart StatefulSet %s/%s", resource.Namespace, resource.Name),
				ActionType:  "restart_statefulset",
				Target:      fmt.Sprintf("%s/%s", resource.Namespace, resource.Name),
				WaitTime:    3 * time.Minute,
				Required:    false,
				Metadata: map[string]string{
					"statefulset": resource.Name,
					"namespace":   resource.Namespace,
				},
			}
			steps = append(steps, step)
			*stepOrder++
		}
	}

	return steps
}

// generateCheckpoints creates health checkpoints after each layer's remediation
func (mlp *MultiLayerPlanner) generateCheckpoints(layers []models.Layer, steps []models.RemediationStep) []models.HealthCheckpoint {
	checkpoints := make([]models.HealthCheckpoint, 0, len(layers))

	// Find the last step for each layer
	lastStepPerLayer := make(map[models.Layer]int)
	for _, step := range steps {
		lastStepPerLayer[step.Layer] = step.Order
	}

	// Create checkpoint after each layer's last step
	for _, layer := range layers {
		lastStep, exists := lastStepPerLayer[layer]
		if !exists {
			continue
		}

		checkpoint := models.HealthCheckpoint{
			Layer:     layer,
			AfterStep: lastStep,
			Timeout:   10 * time.Minute,
			Required:  true,
		}

		// Layer-specific health checks
		switch layer {
		case models.LayerInfrastructure:
			checkpoint.Checks = []string{
				"nodes_ready",
				"mco_stable",
				"storage_available",
				"system_pods_running",
			}
		case models.LayerPlatform:
			checkpoint.Checks = []string{
				"operators_ready",
				"clusteroperators_available",
				"networking_functional",
				"ingress_available",
			}
		case models.LayerApplication:
			checkpoint.Checks = []string{
				"pods_running",
				"deployments_ready",
				"endpoints_healthy",
				"services_responding",
			}
		}

		checkpoints = append(checkpoints, checkpoint)
	}

	return checkpoints
}

// generateRollbackSteps creates rollback steps in reverse order
// These are executed if remediation fails
func (mlp *MultiLayerPlanner) generateRollbackSteps(steps []models.RemediationStep) []models.RemediationStep {
	rollbackSteps := make([]models.RemediationStep, 0, len(steps))

	// Reverse the steps
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]

		rollbackStep := models.RemediationStep{
			Layer:       step.Layer,
			Order:       len(steps) - i,
			Description: fmt.Sprintf("Rollback: %s", step.Description),
			ActionType:  "rollback_" + step.ActionType,
			Target:      step.Target,
			WaitTime:    step.WaitTime,
			Required:    step.Required,
			Metadata:    step.Metadata,
		}

		rollbackSteps = append(rollbackSteps, rollbackStep)
	}

	return rollbackSteps
}
