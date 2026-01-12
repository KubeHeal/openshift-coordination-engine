package v1

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/internal/remediation"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// RemediationHandler handles remediation API requests
type RemediationHandler struct {
	orchestrator *remediation.Orchestrator
	log          *logrus.Logger
}

// NewRemediationHandler creates a new remediation handler
func NewRemediationHandler(orchestrator *remediation.Orchestrator, log *logrus.Logger) *RemediationHandler {
	return &RemediationHandler{
		orchestrator: orchestrator,
		log:          log,
	}
}

// GetIncidentStore returns the incident store for use by other handlers
func (h *RemediationHandler) GetIncidentStore() *storage.IncidentStore {
	return h.incidentStore
}

// TriggerRemediationRequest represents the request body for triggering remediation
type TriggerRemediationRequest struct {
	IncidentID string `json:"incident_id"`
	Namespace  string `json:"namespace"`
	Resource   struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"resource"`
	Issue struct {
		Type        string `json:"type"`
		Description string `json:"description"`
		Severity    string `json:"severity"`
	} `json:"issue"`
}

// TriggerRemediationResponse represents the response for triggering remediation
type TriggerRemediationResponse struct {
	WorkflowID        string `json:"workflow_id"`
	Status            string `json:"status"`
	DeploymentMethod  string `json:"deployment_method"`
	EstimatedDuration string `json:"estimated_duration"`
}

// WorkflowResponse represents the response for getting workflow details
type WorkflowResponse struct {
	ID               string                `json:"id"`
	IncidentID       string                `json:"incident_id"`
	Status           string                `json:"status"`
	DeploymentMethod string                `json:"deployment_method"`
	Namespace        string                `json:"namespace"`
	ResourceName     string                `json:"resource_name"`
	ResourceKind     string                `json:"resource_kind"`
	IssueType        string                `json:"issue_type"`
	Remediator       string                `json:"remediator,omitempty"`
	ErrorMessage     string                `json:"error_message,omitempty"`
	CreatedAt        string                `json:"created_at"`
	StartedAt        string                `json:"started_at,omitempty"`
	CompletedAt      string                `json:"completed_at,omitempty"`
	Duration         string                `json:"duration,omitempty"`
	Steps            []models.WorkflowStep `json:"steps,omitempty"`
}

// TriggerRemediation handles POST /api/v1/remediation/trigger
func (h *RemediationHandler) TriggerRemediation(w http.ResponseWriter, r *http.Request) {
	h.log.Info("Received remediation trigger request")

	// Parse request body
	var req TriggerRemediationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.WithError(err).Error("Failed to decode request body")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.IncidentID == "" {
		http.Error(w, "incident_id is required", http.StatusBadRequest)
		return
	}
	if req.Namespace == "" {
		http.Error(w, "namespace is required", http.StatusBadRequest)
		return
	}
	if req.Resource.Name == "" || req.Resource.Kind == "" {
		http.Error(w, "resource.name and resource.kind are required", http.StatusBadRequest)
		return
	}
	if req.Issue.Type == "" {
		http.Error(w, "issue.type is required", http.StatusBadRequest)
		return
	}

	h.log.WithFields(logrus.Fields{
		"incident_id": req.IncidentID,
		"namespace":   req.Namespace,
		"resource":    req.Resource.Name,
		"issue_type":  req.Issue.Type,
	}).Info("Triggering remediation workflow")

	// Create issue from request
	issue := &models.Issue{
		ID:           req.IncidentID, // Use incident ID as issue ID for now
		Type:         req.Issue.Type,
		Severity:     req.Issue.Severity,
		Namespace:    req.Namespace,
		ResourceType: req.Resource.Kind,
		ResourceName: req.Resource.Name,
		Description:  req.Issue.Description,
		DetectedAt:   time.Now(),
	}

	// Trigger remediation workflow
	workflow, err := h.orchestrator.TriggerRemediation(r.Context(), req.IncidentID, issue)
	if err != nil {
		h.log.WithError(err).Error("Failed to trigger remediation")
		http.Error(w, "Failed to trigger remediation: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build response
	response := TriggerRemediationResponse{
		WorkflowID:        workflow.ID,
		Status:            string(workflow.Status),
		DeploymentMethod:  workflow.DeploymentMethod,
		EstimatedDuration: "5m", // Default estimate
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode response")
	}

	h.log.WithFields(logrus.Fields{
		"workflow_id": workflow.ID,
		"status":      workflow.Status,
	}).Info("Remediation workflow triggered successfully")
}

// GetWorkflow handles GET /api/v1/workflows/{id}
func (h *RemediationHandler) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	workflowID := vars["id"]

	h.log.WithField("workflow_id", workflowID).Info("Getting workflow details")

	// Get workflow from orchestrator
	workflow, err := h.orchestrator.GetWorkflow(workflowID)
	if err != nil {
		h.log.WithError(err).Warn("Workflow not found")
		http.Error(w, "Workflow not found", http.StatusNotFound)
		return
	}

	// Build response
	response := WorkflowResponse{
		ID:               workflow.ID,
		IncidentID:       workflow.IncidentID,
		Status:           string(workflow.Status),
		DeploymentMethod: workflow.DeploymentMethod,
		Namespace:        workflow.Namespace,
		ResourceName:     workflow.ResourceName,
		ResourceKind:     workflow.ResourceKind,
		IssueType:        workflow.IssueType,
		Remediator:       workflow.Remediator,
		ErrorMessage:     workflow.ErrorMessage,
		CreatedAt:        workflow.CreatedAt.Format(time.RFC3339),
		Steps:            workflow.Steps,
	}

	if workflow.StartedAt != nil {
		response.StartedAt = workflow.StartedAt.Format(time.RFC3339)
	}
	if workflow.CompletedAt != nil {
		response.CompletedAt = workflow.CompletedAt.Format(time.RFC3339)
		response.Duration = workflow.Duration().String()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode workflow response")
	}

	h.log.WithFields(logrus.Fields{
		"workflow_id": workflowID,
		"status":      workflow.Status,
	}).Info("Workflow details retrieved successfully")
}

// ListIncidents handles GET /api/v1/incidents
// For now, this returns workflows (we'll enhance later with proper incident tracking)
func (h *RemediationHandler) ListIncidents(w http.ResponseWriter, r *http.Request) {
	h.log.Info("Listing incidents")

	// Get all workflows
	workflows := h.orchestrator.ListWorkflows()

	// Convert to incident response format
	incidents := make([]map[string]interface{}, 0, len(workflows))
	for _, wf := range workflows {
		incident := map[string]interface{}{
			"id":          wf.IncidentID,
			"namespace":   wf.Namespace,
			"resource":    wf.ResourceKind + "/" + wf.ResourceName,
			"issue_type":  wf.IssueType,
			"severity":    "high", // Default for now
			"created_at":  wf.CreatedAt.Format(time.RFC3339),
			"status":      string(wf.Status),
			"workflow_id": wf.ID,
		}

		// Add remediation status
		switch wf.Status {
		case models.WorkflowStatusCompleted:
			incident["status"] = "remediated"
		case models.WorkflowStatusFailed:
			incident["status"] = "failed"
		default:
			incident["status"] = "in_progress"
		}

		incidents = append(incidents, incident)
	}

	response := map[string]interface{}{
		"incidents": incidents,
		"total":     len(incidents),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode incidents response")
	}

	h.log.WithField("count", len(incidents)).Info("Incidents listed successfully")
}
