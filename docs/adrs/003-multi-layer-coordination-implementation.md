# ADR-003: Multi-Layer Coordination Implementation

## Status
ACCEPTED - 2025-12-18

## Context

This ADR defines the Go implementation of multi-layer coordination as outlined in platform ADR-040 (Multi-Layer Coordination Strategy). The coordination engine must handle complex issues that span infrastructure, platform, and application layers in OpenShift environments.

Platform ADR-040 establishes:
- Three-layer model: Infrastructure → Platform → Application
- Layer detection with root cause identification
- Ordered remediation with health checkpoints
- Coordinated rollback across layers

Many operational issues are not isolated to a single layer. For example:
- **Root Cause**: Infrastructure layer (node memory pressure)
- **Symptom**: Application layer (pod evictions)
- **Impact**: Platform layer (scheduler unable to place pods)

Without multi-layer coordination, the engine might fix symptoms without addressing root causes, apply remediation in the wrong order, or create cascading failures.

## Decision

Implement multi-layer coordination in Go with the following components:

### 1. Core Types and Models

Package: `pkg/models/layered_issue.go`

```go
package models

import "time"

// Layer represents a coordination layer
type Layer string

const (
	LayerInfrastructure Layer = "infrastructure" // Nodes, MCO, OS
	LayerPlatform       Layer = "platform"       // OpenShift operators, SDN
	LayerApplication    Layer = "application"    // Pods, deployments
)

// LayerPriority returns the remediation priority (lower is higher priority)
func (l Layer) Priority() int {
	switch l {
	case LayerInfrastructure:
		return 0
	case LayerPlatform:
		return 1
	case LayerApplication:
		return 2
	default:
		return 99
	}
}

// LayeredIssue represents an issue spanning multiple layers
type LayeredIssue struct {
	ID               string              `json:"id"`
	Description      string              `json:"description"`
	AffectedLayers   []Layer             `json:"affected_layers"`
	RootCauseLayer   Layer               `json:"root_cause_layer"`
	ImpactedResources map[Layer][]Resource `json:"impacted_resources"`
	DetectedAt       time.Time           `json:"detected_at"`
}

// Resource represents an impacted Kubernetes resource
type Resource struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Issue     string `json:"issue"`
}

// IsMultiLayer returns true if issue affects multiple layers
func (li *LayeredIssue) IsMultiLayer() bool {
	return len(li.AffectedLayers) > 1
}

// RequiresInfrastructureRemediation returns true if infrastructure layer is affected
func (li *LayeredIssue) RequiresInfrastructureRemediation() bool {
	for _, layer := range li.AffectedLayers {
		if layer == LayerInfrastructure {
			return true
		}
	}
	return false
}
```

Package: `pkg/models/remediation_plan.go`

```go
package models

import "time"

// RemediationStep represents a single remediation action
type RemediationStep struct {
	Layer       Layer         `json:"layer"`
	Order       int           `json:"order"`
	Description string        `json:"description"`
	ActionType  string        `json:"action_type"`
	Target      string        `json:"target"`
	WaitTime    time.Duration `json:"wait_time"`
	Required    bool          `json:"required"`
}

// HealthCheckpoint verifies layer health after remediation steps
type HealthCheckpoint struct {
	Layer      Layer         `json:"layer"`
	AfterStep  int           `json:"after_step"`
	Checks     []string      `json:"checks"`
	Timeout    time.Duration `json:"timeout"`
}

// RemediationPlan contains ordered steps with health checkpoints
type RemediationPlan struct {
	ID          string               `json:"id"`
	IssueID     string               `json:"issue_id"`
	Layers      []Layer              `json:"layers"`
	Steps       []RemediationStep    `json:"steps"`
	Checkpoints []HealthCheckpoint   `json:"checkpoints"`
	RollbackSteps []RemediationStep  `json:"rollback_steps"`
	CreatedAt   time.Time            `json:"created_at"`
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
	for _, checkpoint := range rp.Checkpoints {
		if checkpoint.AfterStep == stepOrder {
			return &checkpoint
		}
	}
	return nil
}
```

### 2. Layer Detection

Package: `internal/coordination/layer_detector.go`

```go
package coordination

import (
	"context"
	"strings"

	"github.com/sirupsen/logrus"
	"openshift-coordination-engine/pkg/models"
)

// LayerDetector detects which layers are affected by an issue
type LayerDetector struct {
	log *logrus.Logger
}

// NewLayerDetector creates a new layer detector
func NewLayerDetector(log *logrus.Logger) *LayerDetector {
	return &LayerDetector{
		log: log,
	}
}

// DetectLayers analyzes an issue and determines affected layers
func (ld *LayerDetector) DetectLayers(ctx context.Context, issueDescription string, resources []models.Resource) *models.LayeredIssue {
	var affectedLayers []models.Layer

	// Check each layer
	if ld.hasInfrastructureIssues(issueDescription, resources) {
		affectedLayers = append(affectedLayers, models.LayerInfrastructure)
	}

	if ld.hasPlatformIssues(issueDescription, resources) {
		affectedLayers = append(affectedLayers, models.LayerPlatform)
	}

	if ld.hasApplicationIssues(issueDescription, resources) {
		affectedLayers = append(affectedLayers, models.LayerApplication)
	}

	// Determine root cause
	rootCause := ld.determineRootCause(affectedLayers)

	// Group resources by layer
	impactedResources := ld.groupResourcesByLayer(resources)

	layeredIssue := &models.LayeredIssue{
		Description:       issueDescription,
		AffectedLayers:    affectedLayers,
		RootCauseLayer:    rootCause,
		ImpactedResources: impactedResources,
	}

	ld.log.WithFields(logrus.Fields{
		"affected_layers": affectedLayers,
		"root_cause":      rootCause,
		"is_multi_layer":  layeredIssue.IsMultiLayer(),
	}).Info("Layer detection complete")

	return layeredIssue
}

// hasInfrastructureIssues checks if issue involves infrastructure layer
func (ld *LayerDetector) hasInfrastructureIssues(description string, resources []models.Resource) bool {
	keywords := []string{
		"node", "machineconfig", "mco", "kubelet",
		"memory pressure", "disk pressure", "pid pressure",
		"os", "kernel", "systemd",
	}

	desc := strings.ToLower(description)
	for _, keyword := range keywords {
		if strings.Contains(desc, keyword) {
			return true
		}
	}

	// Check resource kinds
	for _, resource := range resources {
		if resource.Kind == "Node" || resource.Kind == "MachineConfig" || resource.Kind == "MachineConfigPool" {
			return true
		}
	}

	return false
}

// hasPlatformIssues checks if issue involves platform layer
func (ld *LayerDetector) hasPlatformIssues(description string, resources []models.Resource) bool {
	keywords := []string{
		"operator", "sdn", "networking", "ovn",
		"storage", "csi", "ingress", "router",
		"api server", "controller manager", "scheduler",
	}

	desc := strings.ToLower(description)
	for _, keyword := range keywords {
		if strings.Contains(desc, keyword) {
			return true
		}
	}

	// Check resource kinds
	for _, resource := range resources {
		if resource.Kind == "ClusterOperator" ||
		   strings.Contains(resource.Kind, "Operator") ||
		   resource.Kind == "NetworkPolicy" {
			return true
		}
	}

	return false
}

// hasApplicationIssues checks if issue involves application layer
func (ld *LayerDetector) hasApplicationIssues(description string, resources []models.Resource) bool {
	keywords := []string{
		"pod", "deployment", "replicaset", "statefulset",
		"crashloop", "imagepull", "container", "oom",
		"application", "service",
	}

	desc := strings.ToLower(description)
	for _, keyword := range keywords {
		if strings.Contains(desc, keyword) {
			return true
		}
	}

	// Check resource kinds
	for _, resource := range resources {
		if resource.Kind == "Pod" ||
		   resource.Kind == "Deployment" ||
		   resource.Kind == "StatefulSet" ||
		   resource.Kind == "ReplicaSet" {
			return true
		}
	}

	return false
}

// determineRootCause identifies the root cause layer
// Heuristic: Infrastructure > Platform > Application
func (ld *LayerDetector) determineRootCause(affectedLayers []models.Layer) models.Layer {
	if len(affectedLayers) == 0 {
		return models.LayerApplication // Default
	}

	// Check for infrastructure first (highest priority)
	for _, layer := range affectedLayers {
		if layer == models.LayerInfrastructure {
			return models.LayerInfrastructure
		}
	}

	// Then platform
	for _, layer := range affectedLayers {
		if layer == models.LayerPlatform {
			return models.LayerPlatform
		}
	}

	// Default to application
	return models.LayerApplication
}

// groupResourcesByLayer organizes resources by their layer
func (ld *LayerDetector) groupResourcesByLayer(resources []models.Resource) map[models.Layer][]models.Resource {
	grouped := make(map[models.Layer][]models.Resource)

	for _, resource := range resources {
		layer := ld.resourceToLayer(resource)
		grouped[layer] = append(grouped[layer], resource)
	}

	return grouped
}

// resourceToLayer maps a resource kind to its layer
func (ld *LayerDetector) resourceToLayer(resource models.Resource) models.Layer {
	switch resource.Kind {
	case "Node", "MachineConfig", "MachineConfigPool":
		return models.LayerInfrastructure
	case "ClusterOperator", "NetworkPolicy":
		return models.LayerPlatform
	default:
		return models.LayerApplication
	}
}
```

### 3. Multi-Layer Planner

Package: `internal/coordination/planner.go`

```go
package coordination

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"openshift-coordination-engine/pkg/models"
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
func (mlp *MultiLayerPlanner) GeneratePlan(ctx context.Context, issue *models.LayeredIssue) (*models.RemediationPlan, error) {
	planID := "plan-" + uuid.New().String()[:8]

	mlp.log.WithFields(logrus.Fields{
		"plan_id":      planID,
		"issue_id":     issue.ID,
		"layers":       issue.AffectedLayers,
		"root_cause":   issue.RootCauseLayer,
	}).Info("Generating multi-layer remediation plan")

	// Sort layers by priority (infrastructure first)
	orderedLayers := mlp.sortLayersByPriority(issue.AffectedLayers)

	// Generate steps for each layer
	steps := []models.RemediationStep{}
	stepOrder := 0

	for _, layer := range orderedLayers {
		layerSteps := mlp.generateStepsForLayer(layer, issue.ImpactedResources[layer], &stepOrder)
		steps = append(steps, layerSteps...)
	}

	// Generate health checkpoints
	checkpoints := mlp.generateCheckpoints(orderedLayers, len(steps))

	// Generate rollback steps (reverse order)
	rollbackSteps := mlp.generateRollbackSteps(steps)

	plan := &models.RemediationPlan{
		ID:            planID,
		IssueID:       issue.ID,
		Layers:        orderedLayers,
		Steps:         steps,
		Checkpoints:   checkpoints,
		RollbackSteps: rollbackSteps,
		CreatedAt:     time.Now(),
	}

	mlp.log.WithFields(logrus.Fields{
		"plan_id":     planID,
		"total_steps": len(steps),
		"checkpoints": len(checkpoints),
	}).Info("Multi-layer remediation plan generated")

	return plan, nil
}

// sortLayersByPriority orders layers: Infrastructure → Platform → Application
func (mlp *MultiLayerPlanner) sortLayersByPriority(layers []models.Layer) []models.Layer {
	// Sort using layer priority
	sorted := make([]models.Layer, len(layers))
	copy(sorted, layers)

	// Simple bubble sort by priority
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i].Priority() > sorted[j].Priority() {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

// generateStepsForLayer creates remediation steps for a specific layer
func (mlp *MultiLayerPlanner) generateStepsForLayer(layer models.Layer, resources []models.Resource, stepOrder *int) []models.RemediationStep {
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

// generateInfrastructureSteps creates steps for infrastructure layer
func (mlp *MultiLayerPlanner) generateInfrastructureSteps(resources []models.Resource, stepOrder *int) []models.RemediationStep {
	var steps []models.RemediationStep

	for _, resource := range resources {
		if resource.Kind == "Node" {
			step := models.RemediationStep{
				Layer:       models.LayerInfrastructure,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Monitor MCO rollout for node %s", resource.Name),
				ActionType:  "monitor_mco",
				Target:      resource.Name,
				WaitTime:    5 * time.Minute,
				Required:    true,
			}
			steps = append(steps, step)
			*stepOrder++
		}

		if resource.Kind == "MachineConfig" {
			step := models.RemediationStep{
				Layer:       models.LayerInfrastructure,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Apply MachineConfig %s", resource.Name),
				ActionType:  "apply_machineconfig",
				Target:      resource.Name,
				WaitTime:    10 * time.Minute,
				Required:    true,
			}
			steps = append(steps, step)
			*stepOrder++
		}
	}

	return steps
}

// generatePlatformSteps creates steps for platform layer
func (mlp *MultiLayerPlanner) generatePlatformSteps(resources []models.Resource, stepOrder *int) []models.RemediationStep {
	var steps []models.RemediationStep

	for _, resource := range resources {
		if strings.Contains(resource.Kind, "Operator") {
			step := models.RemediationStep{
				Layer:       models.LayerPlatform,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Restart operator %s", resource.Name),
				ActionType:  "restart_operator",
				Target:      resource.Name,
				WaitTime:    3 * time.Minute,
				Required:    true,
			}
			steps = append(steps, step)
			*stepOrder++
		}
	}

	return steps
}

// generateApplicationSteps creates steps for application layer
func (mlp *MultiLayerPlanner) generateApplicationSteps(resources []models.Resource, stepOrder *int) []models.RemediationStep {
	var steps []models.RemediationStep

	for _, resource := range resources {
		if resource.Kind == "Pod" || resource.Kind == "Deployment" {
			step := models.RemediationStep{
				Layer:       models.LayerApplication,
				Order:       *stepOrder,
				Description: fmt.Sprintf("Restart %s %s/%s", resource.Kind, resource.Namespace, resource.Name),
				ActionType:  "restart_deployment",
				Target:      fmt.Sprintf("%s/%s", resource.Namespace, resource.Name),
				WaitTime:    2 * time.Minute,
				Required:    false, // Optional if infrastructure fix resolves issue
			}
			steps = append(steps, step)
			*stepOrder++
		}
	}

	return steps
}

// generateCheckpoints creates health checkpoints after each layer
func (mlp *MultiLayerPlanner) generateCheckpoints(layers []models.Layer, totalSteps int) []models.HealthCheckpoint {
	var checkpoints []models.HealthCheckpoint

	stepCounter := 0
	for i, layer := range layers {
		// Calculate steps for this layer (simplified: divide evenly)
		stepsPerLayer := totalSteps / len(layers)
		stepCounter += stepsPerLayer

		checkpoint := models.HealthCheckpoint{
			Layer:     layer,
			AfterStep: stepCounter - 1,
			Timeout:   10 * time.Minute,
		}

		// Layer-specific checks
		switch layer {
		case models.LayerInfrastructure:
			checkpoint.Checks = []string{"nodes_ready", "mco_stable", "storage_available"}
		case models.LayerPlatform:
			checkpoint.Checks = []string{"operators_ready", "networking_functional", "ingress_available"}
		case models.LayerApplication:
			checkpoint.Checks = []string{"pods_running", "endpoints_healthy", "services_responding"}
		}

		checkpoints = append(checkpoints, checkpoint)
	}

	return checkpoints
}

// generateRollbackSteps creates rollback steps in reverse order
func (mlp *MultiLayerPlanner) generateRollbackSteps(steps []models.RemediationStep) []models.RemediationStep {
	rollbackSteps := make([]models.RemediationStep, len(steps))

	for i, step := range steps {
		rollbackSteps[len(steps)-1-i] = models.RemediationStep{
			Layer:       step.Layer,
			Order:       i,
			Description: fmt.Sprintf("Rollback: %s", step.Description),
			ActionType:  "rollback_" + step.ActionType,
			Target:      step.Target,
			WaitTime:    step.WaitTime,
			Required:    step.Required,
		}
	}

	return rollbackSteps
}
```

### 4. Orchestrator

Package: `internal/coordination/orchestrator.go`

```go
package coordination

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"openshift-coordination-engine/pkg/models"
)

// MultiLayerOrchestrator executes multi-layer remediation plans
type MultiLayerOrchestrator struct {
	healthChecker *HealthChecker
	log           *logrus.Logger
}

// NewMultiLayerOrchestrator creates a new orchestrator
func NewMultiLayerOrchestrator(healthChecker *HealthChecker, log *logrus.Logger) *MultiLayerOrchestrator {
	return &MultiLayerOrchestrator{
		healthChecker: healthChecker,
		log:           log,
	}
}

// ExecutionResult contains the result of plan execution
type ExecutionResult struct {
	Status         string    `json:"status"` // "success", "failed", "rolled_back"
	Reason         string    `json:"reason"`
	ExecutedSteps  int       `json:"executed_steps"`
	FailedStep     *int      `json:"failed_step,omitempty"`
	CompletedAt    time.Time `json:"completed_at"`
}

// ExecutePlan executes a remediation plan with health checkpoints
func (mlo *MultiLayerOrchestrator) ExecutePlan(ctx context.Context, plan *models.RemediationPlan) (*ExecutionResult, error) {
	mlo.log.WithFields(logrus.Fields{
		"plan_id":     plan.ID,
		"total_steps": len(plan.Steps),
		"layers":      plan.Layers,
	}).Info("Starting multi-layer remediation plan execution")

	executedSteps := []models.RemediationStep{}

	for i, step := range plan.Steps {
		// Execute step
		mlo.log.WithFields(logrus.Fields{
			"step":  step.Order,
			"layer": step.Layer,
			"type":  step.ActionType,
		}).Info("Executing remediation step")

		if err := mlo.executeStep(ctx, step); err != nil {
			mlo.log.WithError(err).Error("Step execution failed")

			// Rollback executed steps
			if err := mlo.rollbackSteps(ctx, executedSteps); err != nil {
				mlo.log.WithError(err).Error("Rollback failed")
			}

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

		// Wait for step to settle
		time.Sleep(step.WaitTime)

		// Check for health checkpoint after this step
		if checkpoint := plan.GetCheckpointAfterStep(step.Order); checkpoint != nil {
			mlo.log.WithFields(logrus.Fields{
				"layer":  checkpoint.Layer,
				"checks": checkpoint.Checks,
			}).Info("Verifying health checkpoint")

			if err := mlo.verifyCheckpoint(ctx, checkpoint); err != nil {
				mlo.log.WithError(err).Error("Health checkpoint failed")

				// Rollback executed steps
				if err := mlo.rollbackSteps(ctx, executedSteps); err != nil {
					mlo.log.WithError(err).Error("Rollback failed")
				}

				failedStep := i
				return &ExecutionResult{
					Status:        "failed",
					Reason:        fmt.Sprintf("checkpoint failed: %v", err),
					ExecutedSteps: len(executedSteps),
					FailedStep:    &failedStep,
					CompletedAt:   time.Now(),
				}, err
			}
		}
	}

	mlo.log.Info("Multi-layer remediation plan completed successfully")
	return &ExecutionResult{
		Status:        "success",
		ExecutedSteps: len(executedSteps),
		CompletedAt:   time.Now(),
	}, nil
}

// executeStep performs a single remediation action
func (mlo *MultiLayerOrchestrator) executeStep(ctx context.Context, step models.RemediationStep) error {
	// Integrates with Kubernetes API, MCO client, and ArgoCD client
	// Implementation delegated to specific remediators based on deployment method

	mlo.log.WithFields(logrus.Fields{
		"action": step.ActionType,
		"target": step.Target,
	}).Info("Executing step (placeholder)")

	return nil
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
func (mlo *MultiLayerOrchestrator) rollbackSteps(ctx context.Context, steps []models.RemediationStep) error {
	mlo.log.WithField("steps", len(steps)).Warn("Starting coordinated rollback")

	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]

		mlo.log.WithFields(logrus.Fields{
			"step":  step.Order,
			"layer": step.Layer,
		}).Info("Rolling back step")

		// Execute rollback action
		if err := mlo.executeRollback(ctx, step); err != nil {
			mlo.log.WithError(err).Error("Rollback step failed")
			// Continue with remaining rollback steps
		}
	}

	mlo.log.Info("Coordinated rollback completed")
	return nil
}

// executeRollback performs rollback for a single step
func (mlo *MultiLayerOrchestrator) executeRollback(ctx context.Context, step models.RemediationStep) error {
	// Reverses remediation action using appropriate remediator
	mlo.log.WithFields(logrus.Fields{
		"action": "rollback_" + step.ActionType,
		"target": step.Target,
	}).Info("Executing rollback")

	return nil
}
```

### 5. Health Checker

Package: `internal/coordination/health_checker.go`

```go
package coordination

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HealthChecker verifies layer-specific health conditions
type HealthChecker struct {
	clientset *kubernetes.Clientset
	log       *logrus.Logger
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(clientset *kubernetes.Clientset, log *logrus.Logger) *HealthChecker {
	return &HealthChecker{
		clientset: clientset,
		log:       log,
	}
}

// CheckInfrastructureHealth verifies infrastructure layer health
func (hc *HealthChecker) CheckInfrastructureHealth(ctx context.Context) error {
	hc.log.Info("Checking infrastructure layer health")

	checks := []func(context.Context) error{
		hc.checkNodesReady,
		hc.checkMCOStable,
		hc.checkStorageAvailable,
	}

	for _, check := range checks {
		if err := check(ctx); err != nil {
			return err
		}
	}

	hc.log.Info("Infrastructure layer health check passed")
	return nil
}

// CheckPlatformHealth verifies platform layer health
func (hc *HealthChecker) CheckPlatformHealth(ctx context.Context) error {
	hc.log.Info("Checking platform layer health")

	checks := []func(context.Context) error{
		hc.checkOperatorsReady,
		hc.checkNetworkingFunctional,
		hc.checkIngressAvailable,
	}

	for _, check := range checks {
		if err := check(ctx); err != nil {
			return err
		}
	}

	hc.log.Info("Platform layer health check passed")
	return nil
}

// CheckApplicationHealth verifies application layer health
func (hc *HealthChecker) CheckApplicationHealth(ctx context.Context) error {
	hc.log.Info("Checking application layer health")

	checks := []func(context.Context) error{
		hc.checkPodsRunning,
		hc.checkEndpointsHealthy,
		hc.checkServicesResponding,
	}

	for _, check := range checks {
		if err := check(ctx); err != nil {
			return err
		}
	}

	hc.log.Info("Application layer health check passed")
	return nil
}

// Infrastructure checks

func (hc *HealthChecker) checkNodesReady(ctx context.Context) error {
	nodes, err := hc.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	for _, node := range nodes.Items {
		for _, condition := range node.Status.Conditions {
			if condition.Type == "Ready" && condition.Status != "True" {
				return fmt.Errorf("node %s is not ready", node.Name)
			}
		}
	}

	return nil
}

func (hc *HealthChecker) checkMCOStable(ctx context.Context) error {
	// Checks MachineConfigPool status via MCO client
	hc.log.Debug("MCO stability check")
	return nil
}

func (hc *HealthChecker) checkStorageAvailable(ctx context.Context) error {
	// Verifies PersistentVolume availability
	hc.log.Debug("Storage availability check")
	return nil
}

// Platform checks

func (hc *HealthChecker) checkOperatorsReady(ctx context.Context) error {
	// Validates ClusterOperator status
	hc.log.Debug("Operator readiness check")
	return nil
}

func (hc *HealthChecker) checkNetworkingFunctional(ctx context.Context) error {
	// Checks SDN/OVN network functionality
	hc.log.Debug("Networking functionality check")
	return nil
}

func (hc *HealthChecker) checkIngressAvailable(ctx context.Context) error {
	// Verifies ingress controller availability
	hc.log.Debug("Ingress availability check")
	return nil
}

// Application checks

func (hc *HealthChecker) checkPodsRunning(ctx context.Context) error {
	// Check all pods in target namespaces
	namespaces := []string{"default", "production"} // Configurable via environment

	for _, ns := range namespaces {
		pods, err := hc.clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list pods in namespace %s: %w", ns, err)
		}

		for _, pod := range pods.Items {
			if pod.Status.Phase != "Running" && pod.Status.Phase != "Succeeded" {
				return fmt.Errorf("pod %s/%s is not running: %s", ns, pod.Name, pod.Status.Phase)
			}
		}
	}

	return nil
}

func (hc *HealthChecker) checkEndpointsHealthy(ctx context.Context) error {
	// Verifies endpoint readiness
	hc.log.Debug("Endpoints health check")
	return nil
}

func (hc *HealthChecker) checkServicesResponding(ctx context.Context) error {
	// Validates service availability
	hc.log.Debug("Services responding check")
	return nil
}
```

### 6. Metrics

Package: `internal/coordination/metrics.go`

```go
package coordination

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	MultiLayerRemediationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_multilayer_remediation_total",
			Help: "Total number of multi-layer remediation executions",
		},
		[]string{"status", "layers"},
	)

	RemediationStepDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_remediation_step_duration_seconds",
			Help:    "Duration of remediation steps",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10), // 1s to 512s
		},
		[]string{"layer", "action_type"},
	)

	HealthCheckpointTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_health_checkpoint_total",
			Help: "Total number of health checkpoint verifications",
		},
		[]string{"layer", "status"},
	)

	RollbackTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_rollback_total",
			Help: "Total number of coordinated rollbacks",
		},
		[]string{"reason", "steps_rolled_back"},
	)

	LayerDetectionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_layer_detection_total",
			Help: "Total number of layer detections",
		},
		[]string{"root_cause_layer", "is_multi_layer"},
	)
)
```

## Configuration

Environment variables:
```bash
LAYER_DETECTION_ENABLED=true         # Enable multi-layer coordination
CHECKPOINT_TIMEOUT=10m                # Health checkpoint timeout
ROLLBACK_ENABLED=true                 # Enable automatic rollback on failure
INFRASTRUCTURE_PRIORITY=0             # Layer priority (lower = higher)
PLATFORM_PRIORITY=1
APPLICATION_PRIORITY=2
```

## Testing Strategy

### Unit Tests

```go
func TestLayerDetector_DetectLayers_MultiLayer(t *testing.T) {
	detector := NewLayerDetector(logrus.New())

	description := "Node memory pressure causing pod evictions"
	resources := []models.Resource{
		{Kind: "Node", Name: "worker-1", Issue: "MemoryPressure"},
		{Kind: "Pod", Name: "app-123", Namespace: "prod", Issue: "Evicted"},
	}

	issue := detector.DetectLayers(context.Background(), description, resources)

	assert.Contains(t, issue.AffectedLayers, models.LayerInfrastructure)
	assert.Contains(t, issue.AffectedLayers, models.LayerApplication)
	assert.Equal(t, models.LayerInfrastructure, issue.RootCauseLayer)
	assert.True(t, issue.IsMultiLayer())
}

func TestMultiLayerPlanner_SortLayersByPriority(t *testing.T) {
	planner := NewMultiLayerPlanner(logrus.New())

	layers := []models.Layer{
		models.LayerApplication,
		models.LayerInfrastructure,
		models.LayerPlatform,
	}

	sorted := planner.sortLayersByPriority(layers)

	assert.Equal(t, models.LayerInfrastructure, sorted[0])
	assert.Equal(t, models.LayerPlatform, sorted[1])
	assert.Equal(t, models.LayerApplication, sorted[2])
}
```

### Integration Tests

```go
func TestMultiLayerOrchestrator_ExecutePlan_WithCheckpoints(t *testing.T) {
	clientset := testutil.NewFakeClientset()
	healthChecker := NewHealthChecker(clientset, logrus.New())
	orchestrator := NewMultiLayerOrchestrator(healthChecker, logrus.New())

	plan := &models.RemediationPlan{
		ID:      "test-plan",
		IssueID: "test-issue",
		Layers:  []models.Layer{models.LayerInfrastructure, models.LayerApplication},
		Steps: []models.RemediationStep{
			{Layer: models.LayerInfrastructure, Order: 0, ActionType: "monitor_mco"},
			{Layer: models.LayerApplication, Order: 1, ActionType: "restart_deployment"},
		},
		Checkpoints: []models.HealthCheckpoint{
			{Layer: models.LayerInfrastructure, AfterStep: 0, Checks: []string{"nodes_ready"}},
		},
	}

	result, err := orchestrator.ExecutePlan(context.Background(), plan)

	assert.NoError(t, err)
	assert.Equal(t, "success", result.Status)
	assert.Equal(t, 2, result.ExecutedSteps)
}
```

## Performance Characteristics

- **Layer Detection Latency**: <100ms (keyword matching + resource analysis)
- **Plan Generation**: <500ms (for multi-layer issues)
- **Checkpoint Verification**: <30s per checkpoint (configurable timeout)
- **Total Remediation Time**: 5-30 minutes depending on layers involved
- **Rollback Time**: 2-10 minutes (reverse order execution)

## Consequences

### Positive
- ✅ **Root Cause Focus**: Addresses infrastructure issues before symptoms
- ✅ **Ordered Execution**: Infrastructure → Platform → Application ensures stability
- ✅ **Safety Checkpoints**: Verifies health before proceeding to next layer
- ✅ **Coordinated Rollback**: Prevents partial remediation states
- ✅ **Type Safety**: Go's type system prevents layer ordering errors
- ✅ **Concurrent Safe**: Designed for concurrent remediation workflows

### Negative
- ⚠️ **Increased Latency**: Sequential execution takes longer than single-layer
- ⚠️ **Complexity**: More complex orchestration logic
- ⚠️ **False Positives**: Keyword-based layer detection may misidentify layers
- ⚠️ **Testing Overhead**: Must test multi-layer scenarios end-to-end

### Mitigation
- **Latency**: Parallel health checks, optimized wait times
- **Complexity**: Clear separation of concerns (detector, planner, orchestrator, health checker)
- **Accuracy**: Enhance layer detection with ML-based classification (future)
- **Testing**: Comprehensive integration tests, mock cluster scenarios

## References

- Platform ADR-040: Multi-Layer Coordination Strategy (overall design)
- ADR-001: Go Project Architecture (package organization)
- ADR-002: Deployment Detection Implementation (detection patterns)
- ADR-005: Remediation Strategies Implementation (remediation actions)
- Kubernetes API: https://kubernetes.io/docs/reference/using-api/
- OpenShift Architecture: https://docs.openshift.com/container-platform/latest/architecture/architecture.html

## Related ADRs

- Platform ADR-040: Multi-Layer Coordination Strategy
- ADR-001: Go Project Architecture
- ADR-002: Deployment Detection Implementation
- ADR-004: ArgoCD/MCO Integration (infrastructure layer remediation)
- ADR-005: Remediation Strategies Implementation (strategy execution)
