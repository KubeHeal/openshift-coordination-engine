// Package v1 implements the v1 REST API handlers for the coordination engine.
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/internal/coordination"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// CoordinationHandler handles multi-layer coordination requests
type CoordinationHandler struct {
	layerDetector         *coordination.LayerDetector
	mlLayerDetector       *coordination.MLLayerDetector // Phase 6: ML-enhanced detector
	planner               *coordination.MultiLayerPlanner
	orchestrator          *coordination.MultiLayerOrchestrator
	coordinationWorkflows map[string]*CoordinationWorkflow
	mu                    sync.RWMutex
	log                   *logrus.Logger
	enableMLDetection     bool // Phase 6: feature flag for ML detection
}

// CoordinationWorkflow tracks multi-layer remediation workflows
type CoordinationWorkflow struct {
	ID              string                        `json:"id"`
	IncidentID      string                        `json:"incident_id"`
	Status          string                        `json:"status"` // pending, planning, executing, completed, failed
	LayeredIssue    *models.LayeredIssue          `json:"layered_issue,omitempty"`
	RemediationPlan *models.RemediationPlan       `json:"remediation_plan,omitempty"`
	ExecutionResult *coordination.ExecutionResult `json:"execution_result,omitempty"`
	CreatedAt       time.Time                     `json:"created_at"`
	StartedAt       *time.Time                    `json:"started_at,omitempty"`
	CompletedAt     *time.Time                    `json:"completed_at,omitempty"`
	ErrorMessage    string                        `json:"error_message,omitempty"`
}

// TriggerMultiLayerRemediationRequest is the request format for triggering multi-layer remediation
type TriggerMultiLayerRemediationRequest struct {
	IncidentID  string            `json:"incident_id"`
	Description string            `json:"description"`
	Resources   []models.Resource `json:"resources"`
}

// TriggerMultiLayerRemediationResponse is the response format
type TriggerMultiLayerRemediationResponse struct {
	WorkflowID     string         `json:"workflow_id"`
	Status         string         `json:"status"`
	AffectedLayers []models.Layer `json:"affected_layers"`
	RootCauseLayer models.Layer   `json:"root_cause_layer"`
	EstimatedSteps int            `json:"estimated_steps"`
}

// NewCoordinationHandler creates a new coordination handler
func NewCoordinationHandler(
	layerDetector *coordination.LayerDetector,
	planner *coordination.MultiLayerPlanner,
	orchestrator *coordination.MultiLayerOrchestrator,
	log *logrus.Logger,
) *CoordinationHandler {
	return &CoordinationHandler{
		layerDetector:         layerDetector,
		mlLayerDetector:       nil, // Set via SetMLLayerDetector if available
		planner:               planner,
		orchestrator:          orchestrator,
		coordinationWorkflows: make(map[string]*CoordinationWorkflow),
		log:                   log,
		enableMLDetection:     false, // Default to keyword-based detection
	}
}

// SetMLLayerDetector enables ML-enhanced layer detection (Phase 6)
func (ch *CoordinationHandler) SetMLLayerDetector(mlDetector *coordination.MLLayerDetector) {
	ch.mlLayerDetector = mlDetector
	ch.enableMLDetection = mlDetector != nil
	if ch.enableMLDetection {
		ch.log.Info("ML-enhanced layer detection enabled for coordination handler")
	}
}

// TriggerMultiLayerRemediation handles POST /api/v1/coordination/trigger
func (ch *CoordinationHandler) TriggerMultiLayerRemediation(w http.ResponseWriter, r *http.Request) {
	var req TriggerMultiLayerRemediationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ch.log.WithError(err).Error("Failed to decode request")
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Validate request
	if req.IncidentID == "" {
		http.Error(w, "incident_id is required", http.StatusBadRequest)
		return
	}
	if req.Description == "" {
		http.Error(w, "description is required", http.StatusBadRequest)
		return
	}
	if len(req.Resources) == 0 {
		http.Error(w, "at least one resource is required", http.StatusBadRequest)
		return
	}

	ch.log.WithFields(logrus.Fields{
		"incident_id":  req.IncidentID,
		"resources":    len(req.Resources),
		"ml_detection": ch.enableMLDetection,
	}).Info("Triggering multi-layer remediation")

	// Detect layers (use ML-enhanced detector if available, otherwise keyword-based)
	ctx := context.Background()
	var layeredIssue *models.LayeredIssue
	if ch.enableMLDetection && ch.mlLayerDetector != nil {
		// Phase 6: Use ML-enhanced detection
		layeredIssue = ch.mlLayerDetector.DetectLayersWithML(ctx, req.IncidentID, req.Description, req.Resources)
	} else {
		// Fallback: Use keyword-based detection
		layeredIssue = ch.layerDetector.DetectLayers(ctx, req.IncidentID, req.Description, req.Resources)
	}

	// Generate remediation plan
	plan, err := ch.planner.GeneratePlan(ctx, layeredIssue)
	if err != nil {
		ch.log.WithError(err).Error("Failed to generate remediation plan")
		http.Error(w, fmt.Sprintf("Failed to generate plan: %v", err), http.StatusInternalServerError)
		return
	}

	// Create coordination workflow
	workflow := &CoordinationWorkflow{
		ID:              generateCoordinationWorkflowID(),
		IncidentID:      req.IncidentID,
		Status:          "pending",
		LayeredIssue:    layeredIssue,
		RemediationPlan: plan,
		CreatedAt:       time.Now(),
	}

	// Store workflow
	ch.mu.Lock()
	ch.coordinationWorkflows[workflow.ID] = workflow
	ch.mu.Unlock()

	// Execute remediation in background
	go ch.executeCoordinationWorkflow(workflow)

	// Return response
	response := TriggerMultiLayerRemediationResponse{
		WorkflowID:     workflow.ID,
		Status:         workflow.Status,
		AffectedLayers: layeredIssue.AffectedLayers,
		RootCauseLayer: layeredIssue.RootCauseLayer,
		EstimatedSteps: len(plan.Steps),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		ch.log.WithError(err).Error("Failed to encode response")
	}
}

// GetCoordinationWorkflow handles GET /api/v1/coordination/workflows/{id}
func (ch *CoordinationHandler) GetCoordinationWorkflow(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	workflowID := vars["id"]

	ch.mu.RLock()
	workflow, exists := ch.coordinationWorkflows[workflowID]
	ch.mu.RUnlock()

	if !exists {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(workflow); err != nil {
		ch.log.WithError(err).Error("Failed to encode workflow response")
	}
}

// ListCoordinationWorkflows handles GET /api/v1/coordination/workflows
func (ch *CoordinationHandler) ListCoordinationWorkflows(w http.ResponseWriter, r *http.Request) {
	ch.mu.RLock()
	workflows := make([]*CoordinationWorkflow, 0, len(ch.coordinationWorkflows))
	for _, wf := range ch.coordinationWorkflows {
		workflows = append(workflows, wf)
	}
	ch.mu.RUnlock()

	response := map[string]interface{}{
		"workflows": workflows,
		"total":     len(workflows),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		ch.log.WithError(err).Error("Failed to encode workflows list response")
	}
}

// executeCoordinationWorkflow executes the multi-layer remediation workflow
func (ch *CoordinationHandler) executeCoordinationWorkflow(workflow *CoordinationWorkflow) {
	ch.log.WithField("workflow_id", workflow.ID).Info("Starting multi-layer remediation workflow")

	// Update status to executing
	ch.updateWorkflowStatus(workflow, "executing")
	startTime := time.Now()
	workflow.StartedAt = &startTime

	// Execute plan
	ctx := context.Background()
	result, err := ch.orchestrator.ExecutePlan(ctx, workflow.RemediationPlan)

	completedTime := time.Now()
	workflow.CompletedAt = &completedTime
	workflow.ExecutionResult = result

	if err != nil {
		ch.log.WithError(err).Error("Multi-layer remediation failed")
		workflow.Status = "failed"
		workflow.ErrorMessage = err.Error()
	} else {
		ch.log.Info("Multi-layer remediation completed successfully")
		workflow.Status = "completed"
	}

	// Save workflow state
	ch.saveWorkflow(workflow)

	ch.log.WithFields(logrus.Fields{
		"workflow_id": workflow.ID,
		"status":      workflow.Status,
		"duration":    completedTime.Sub(startTime).String(),
	}).Info("Multi-layer remediation workflow completed")
}

// updateWorkflowStatus updates the workflow status
func (ch *CoordinationHandler) updateWorkflowStatus(workflow *CoordinationWorkflow, status string) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	workflow.Status = status
}

// saveWorkflow persists workflow state
func (ch *CoordinationHandler) saveWorkflow(workflow *CoordinationWorkflow) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.coordinationWorkflows[workflow.ID] = workflow
}

// generateCoordinationWorkflowID generates a unique workflow ID
func generateCoordinationWorkflowID() string {
	return fmt.Sprintf("cwf-%d", time.Now().UnixNano()%1000000000)
}

// RegisterRoutes registers coordination API routes
func (ch *CoordinationHandler) RegisterRoutes(router *mux.Router) {
	apiV1 := router.PathPrefix("/api/v1").Subrouter()
	apiV1.HandleFunc("/coordination/trigger", ch.TriggerMultiLayerRemediation).Methods("POST")
	apiV1.HandleFunc("/coordination/workflows/{id}", ch.GetCoordinationWorkflow).Methods("GET")
	apiV1.HandleFunc("/coordination/workflows", ch.ListCoordinationWorkflows).Methods("GET")
}
