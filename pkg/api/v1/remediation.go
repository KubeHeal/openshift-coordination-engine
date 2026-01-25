package v1

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/internal/remediation"
	"github.com/tosin2013/openshift-coordination-engine/internal/storage"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// RemediationHandler handles remediation API requests
type RemediationHandler struct {
	orchestrator  *remediation.Orchestrator
	incidentStore *storage.IncidentStore
	log           *logrus.Logger
}

// NewRemediationHandler creates a new remediation handler
func NewRemediationHandler(orchestrator *remediation.Orchestrator, log *logrus.Logger) *RemediationHandler {
	return &RemediationHandler{
		orchestrator:  orchestrator,
		incidentStore: storage.NewIncidentStore(),
		log:           log,
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

// CreateIncidentRequest represents the request body for creating an incident
type CreateIncidentRequest struct {
	Title             string            `json:"title"`
	Description       string            `json:"description"`
	Severity          string            `json:"severity"`
	Target            string            `json:"target"`
	AffectedResources []string          `json:"affected_resources,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
}

// CreateIncidentResponse represents the response for creating an incident
type CreateIncidentResponse struct {
	Status     string           `json:"status"`
	IncidentID string           `json:"incident_id"`
	CreatedAt  string           `json:"created_at"`
	Incident   *models.Incident `json:"incident"`
	Message    string           `json:"message"`
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

// CreateIncident handles POST /api/v1/incidents
func (h *RemediationHandler) CreateIncident(w http.ResponseWriter, r *http.Request) {
	h.log.Info("Received create incident request")

	// Parse request body
	var req CreateIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.WithError(err).Error("Failed to decode request body")
		h.sendErrorResponse(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}

	// Create incident model from request
	incident := &models.Incident{
		Title:             req.Title,
		Description:       req.Description,
		Severity:          models.IncidentSeverity(req.Severity),
		Target:            req.Target,
		AffectedResources: req.AffectedResources,
		Labels:            req.Labels,
	}

	// Store incident (validation happens in Create)
	createdIncident, err := h.incidentStore.Create(incident)
	if err != nil {
		h.log.WithError(err).Error("Failed to create incident")
		h.sendErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	h.log.WithFields(logrus.Fields{
		"incident_id": createdIncident.ID,
		"title":       createdIncident.Title,
		"severity":    createdIncident.Severity,
		"target":      createdIncident.Target,
	}).Info("Incident created successfully")

	// Build response
	response := CreateIncidentResponse{
		Status:     "success",
		IncidentID: createdIncident.ID,
		CreatedAt:  createdIncident.CreatedAt.Format(time.RFC3339),
		Incident:   createdIncident,
		Message:    "Incident created successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode response")
	}
}

// ListIncidents handles GET /api/v1/incidents
func (h *RemediationHandler) ListIncidents(w http.ResponseWriter, r *http.Request) {
	h.log.Info("Listing incidents")

	// Parse query parameters for filtering
	query := r.URL.Query()
	namespace := query.Get("namespace")
	severity := query.Get("severity")

	// Get manually created incidents from the store
	filter := storage.ListFilter{
		Namespace: namespace,
		Severity:  severity,
		Limit:     50, // Default limit
	}
	storedIncidents := h.incidentStore.List(filter)

	// Get workflow-based incidents
	workflows := h.orchestrator.ListWorkflows()

	// Combine both sources into response
	incidents := make([]map[string]interface{}, 0, len(storedIncidents)+len(workflows))

	// Add stored incidents first
	for _, inc := range storedIncidents {
		incident := map[string]interface{}{
			"id":                 inc.ID,
			"title":              inc.Title,
			"description":        inc.Description,
			"target":             inc.Target,
			"severity":           string(inc.Severity),
			"status":             string(inc.Status),
			"created_at":         inc.CreatedAt.Format(time.RFC3339),
			"affected_resources": inc.AffectedResources,
			"labels":             inc.Labels,
			"source":             "manual",
		}
		if inc.WorkflowID != "" {
			incident["workflow_id"] = inc.WorkflowID
		}
		incidents = append(incidents, incident)
	}

	// Add workflow-based incidents
	for _, wf := range workflows {
		// Apply namespace filter if specified
		if namespace != "" && wf.Namespace != namespace {
			continue
		}

		incident := map[string]interface{}{
			"id":          wf.IncidentID,
			"namespace":   wf.Namespace,
			"target":      wf.Namespace,
			"resource":    wf.ResourceKind + "/" + wf.ResourceName,
			"issue_type":  wf.IssueType,
			"severity":    "high", // Default for workflow-based incidents
			"created_at":  wf.CreatedAt.Format(time.RFC3339),
			"workflow_id": wf.ID,
			"source":      "workflow",
		}

		// Map workflow status to incident status
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

// sendErrorResponse sends a JSON error response
func (h *RemediationHandler) sendErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	response := map[string]string{
		"status": "error",
		"error":  message,
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode error response")
	}
}
