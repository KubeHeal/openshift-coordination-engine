package v1

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/KubeHeal/openshift-coordination-engine/internal/remediation"
	"github.com/KubeHeal/openshift-coordination-engine/internal/storage"
	"github.com/KubeHeal/openshift-coordination-engine/pkg/models"
)

// RemediationHandler handles remediation API requests
type RemediationHandler struct {
	orchestrator  *remediation.Orchestrator
	incidentStore *storage.IncidentStore
	log           *logrus.Logger
}

// NewRemediationHandler creates a new remediation handler with in-memory incident store
func NewRemediationHandler(orchestrator *remediation.Orchestrator, log *logrus.Logger) *RemediationHandler {
	return &RemediationHandler{
		orchestrator:  orchestrator,
		incidentStore: storage.NewIncidentStore(),
		log:           log,
	}
}

// NewRemediationHandlerWithStore creates a new remediation handler with provided incident store
func NewRemediationHandlerWithStore(orchestrator *remediation.Orchestrator, incidentStore *storage.IncidentStore, log *logrus.Logger) *RemediationHandler {
	return &RemediationHandler{
		orchestrator:  orchestrator,
		incidentStore: incidentStore,
		log:           log,
	}
}

// GetIncidentStore returns the incident store for use by other handlers
func (h *RemediationHandler) GetIncidentStore() *storage.IncidentStore {
	return h.incidentStore
}

// RemediationResource identifies the Kubernetes object to repair.
type RemediationResource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// RemediationIssue describes the detected problem.
type RemediationIssue struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
}

// TriggerRemediationRequest represents the request body for triggering remediation
type TriggerRemediationRequest struct {
	IncidentID string              `json:"incident_id"`
	Namespace  string              `json:"namespace"`
	Resource   RemediationResource `json:"resource"`
	Issue      RemediationIssue    `json:"issue"`
	DryRun     bool                `json:"dry_run,omitempty"`
}

// TriggerRemediationResponse represents the response for triggering remediation
type TriggerRemediationResponse struct {
	WorkflowID        string   `json:"workflow_id"`
	Status            string   `json:"status"`
	DeploymentMethod  string   `json:"deployment_method"`
	EstimatedDuration string   `json:"estimated_duration"`
	PlannedActions    []string `json:"planned_actions,omitempty"`
}

// WorkflowResponse represents the response for getting workflow details
type WorkflowResponse struct {
	ID               string                      `json:"id"`
	IncidentID       string                      `json:"incident_id"`
	Status           string                      `json:"status"`
	DeploymentMethod string                      `json:"deployment_method"`
	Namespace        string                      `json:"namespace"`
	ResourceName     string                      `json:"resource_name"`
	ResourceKind     string                      `json:"resource_kind"`
	IssueType        string                      `json:"issue_type"`
	Remediator       string                      `json:"remediator,omitempty"`
	ErrorMessage     string                      `json:"error_message,omitempty"`
	CreatedAt        string                      `json:"created_at"`
	StartedAt        string                      `json:"started_at,omitempty"`
	CompletedAt      string                      `json:"completed_at,omitempty"`
	Duration         string                      `json:"duration,omitempty"`
	Steps            []models.WorkflowStep       `json:"steps,omitempty"`
	Layers           []string                    `json:"layers,omitempty"`
	Checkpoints      []models.WorkflowCheckpoint `json:"checkpoints,omitempty"`
}

func (h *RemediationHandler) linkWorkflow(incidentID, workflowID string) {
	incident, err := h.incidentStore.Get(incidentID)
	if err != nil {
		return
	}
	incident.WorkflowID = workflowID
	if err := h.incidentStore.Update(incident); err != nil {
		h.log.WithError(err).WithField("incident_id", incidentID).Warn("failed to record workflow_id on incident")
	}
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
// @Summary Trigger a remediation workflow
// @Description Starts remediation for an incident against a detected deployment method
// @Tags remediation
// @Accept json
// @Produce json
// @Param request body TriggerRemediationRequest true "Remediation trigger request"
// @Success 202 {object} TriggerRemediationResponse
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Failed to trigger remediation"
// @Router /api/v1/remediation/trigger [post]
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
		"dry_run":     req.DryRun,
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

	if req.DryRun {
		h.writeDryRun(w, r, issue)
		return
	}

	// Trigger remediation workflow
	workflow, err := h.orchestrator.TriggerRemediation(r.Context(), req.IncidentID, issue)
	if err != nil {
		h.log.WithError(err).Error("Failed to trigger remediation")
		http.Error(w, "Failed to trigger remediation: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.linkWorkflow(req.IncidentID, workflow.ID)
	h.writeTriggerResponse(w, &TriggerRemediationResponse{
		WorkflowID:        workflow.ID,
		Status:            string(workflow.Status),
		DeploymentMethod:  workflow.DeploymentMethod,
		EstimatedDuration: workflow.EstimatedDuration,
	})

	h.log.WithFields(logrus.Fields{
		"workflow_id": workflow.ID,
		"status":      workflow.Status,
	}).Info("Remediation workflow triggered successfully")
}

func (h *RemediationHandler) writeDryRun(w http.ResponseWriter, r *http.Request, issue *models.Issue) {
	method, duration, actions, err := h.orchestrator.PreviewRemediation(r.Context(), issue)
	if err != nil {
		h.log.WithError(err).Error("Failed to preview remediation")
		http.Error(w, "Failed to trigger remediation: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.writeTriggerResponse(w, &TriggerRemediationResponse{
		Status:            string(models.WorkflowStatusPending),
		DeploymentMethod:  method,
		EstimatedDuration: duration,
		PlannedActions:    actions,
	})
	h.log.WithFields(logrus.Fields{
		"deployment_method": method,
		"dry_run":           true,
	}).Info("Remediation dry run completed")
}

func (h *RemediationHandler) writeTriggerResponse(w http.ResponseWriter, response *TriggerRemediationResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode response")
	}
}

// GetWorkflow handles GET /api/v1/workflows/{id}
// @Summary Get a remediation workflow
// @Description Returns the status and steps of a remediation workflow
// @Tags remediation
// @Produce json
// @Param id path string true "Workflow ID"
// @Success 200 {object} WorkflowResponse
// @Failure 404 {string} string "Workflow not found"
// @Router /api/v1/workflows/{id} [get]
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
		Layers:           workflow.Layers,
		Checkpoints:      append([]models.WorkflowCheckpoint(nil), workflow.Checkpoints...),
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
// @Summary Create an incident
// @Description Records an incident that can later be used to trigger remediation
// @Tags remediation
// @Accept json
// @Produce json
// @Param request body CreateIncidentRequest true "Incident to create"
// @Success 201 {object} CreateIncidentResponse
// @Failure 400 {object} APIErrorResponse
// @Router /api/v1/incidents [post]
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

// IncidentListItem is one row returned by GET /api/v1/incidents.
type IncidentListItem struct {
	ID                string            `json:"id,omitempty"`
	Title             string            `json:"title,omitempty"`
	Description       string            `json:"description,omitempty"`
	Target            string            `json:"target,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	Resource          string            `json:"resource,omitempty"`
	IssueType         string            `json:"issue_type,omitempty"`
	Severity          string            `json:"severity,omitempty"`
	Status            string            `json:"status,omitempty"`
	CreatedAt         string            `json:"created_at,omitempty"`
	StartedAt         string            `json:"started_at,omitempty"`
	CompletedAt       string            `json:"completed_at,omitempty"`
	DurationSeconds   float64           `json:"duration_seconds,omitempty"`
	WorkflowID        string            `json:"workflow_id,omitempty"`
	ActionType        string            `json:"action_type,omitempty"`
	Source            string            `json:"source,omitempty"`
	AffectedResources []string          `json:"affected_resources,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
}

// IncidentListSummary is the count block MCP reads from GET /api/v1/incidents.
type IncidentListSummary struct {
	Total      int            `json:"total"`
	Active     int            `json:"active"`
	Completed  int            `json:"completed"`
	Failed     int            `json:"failed"`
	BySeverity map[string]int `json:"by_severity"`
}

// ListIncidentsResponse is the body of GET /api/v1/incidents.
type ListIncidentsResponse struct {
	Incidents []IncidentListItem  `json:"incidents"`
	Total     int                 `json:"total"`
	Summary   IncidentListSummary `json:"summary"`
}

// ListIncidents handles GET /api/v1/incidents
// @Summary List incidents
// @Description Lists stored incidents and incidents derived from remediation workflows
// @Tags remediation
// @Produce json
// @Param namespace query string false "Filter by namespace"
// @Param severity query string false "Filter by severity. all matches every severity"
// @Param status query string false "Filter by status. all matches every status. active, running, completed, and failed are aliases"
// @Param limit query int false "Maximum rows to return. Defaults to 50"
// @Param offset query int false "Rows to skip after filtering. Defaults to 0"
// @Success 200 {object} ListIncidentsResponse
// @Router /api/v1/incidents [get]
func (h *RemediationHandler) ListIncidents(w http.ResponseWriter, r *http.Request) {
	h.log.Info("Listing incidents")

	query := r.URL.Query()
	namespace := strings.TrimSpace(query.Get("namespace"))
	severity := optionalFilter(query.Get("severity"))
	status := optionalFilter(query.Get("status"))
	limit := listLimit(query.Get("limit"))
	offset := listOffset(query.Get("offset"))

	rows := h.incidentRows(namespace)
	filtered := filterIncidentRows(rows, namespace, severity, status)
	summary := summarizeIncidents(filtered)
	page := pageIncidentRows(filtered, offset, limit)

	response := ListIncidentsResponse{
		Incidents: page,
		Total:     summary.Total,
		Summary:   summary,
	}
	if response.Incidents == nil {
		response.Incidents = []IncidentListItem{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode incidents response")
	}

	h.log.WithField("count", len(page)).Info("Incidents listed successfully")
}

func (h *RemediationHandler) incidentRows(namespace string) []IncidentListItem {
	stored := h.incidentStore.List(storage.ListFilter{Namespace: namespace})
	rows := make([]IncidentListItem, 0, len(stored)+8)
	for _, incident := range stored {
		rows = append(rows, IncidentListItem{
			ID:                incident.ID,
			Title:             incident.Title,
			Description:       incident.Description,
			Target:            incident.Target,
			Severity:          string(incident.Severity),
			Status:            storedListStatus(incident.Status),
			CreatedAt:         incident.CreatedAt.Format(time.RFC3339),
			WorkflowID:        incident.WorkflowID,
			Source:            "manual",
			AffectedResources: incident.AffectedResources,
			Labels:            incident.Labels,
		})
	}
	for _, workflow := range h.orchestrator.ListWorkflows() {
		if namespace != "" && workflow.Namespace != namespace {
			continue
		}
		rows = append(rows, workflowListItem(workflow, h.workflowSeverity(workflow)))
	}
	sortIncidentRows(rows)
	return rows
}

func (h *RemediationHandler) workflowSeverity(workflow *models.Workflow) string {
	if workflow.Severity != "" {
		return workflow.Severity
	}
	incident, err := h.incidentStore.Get(workflow.IncidentID)
	if err != nil {
		return ""
	}
	return string(incident.Severity)
}

func optionalFilter(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "all") {
		return ""
	}
	return value
}

func listLimit(raw string) int {
	const defaultLimit = 50
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLimit
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return defaultLimit
	}
	return parsed
}

func listOffset(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func filterIncidentRows(rows []IncidentListItem, namespace, severity, status string) []IncidentListItem {
	filtered := make([]IncidentListItem, 0, len(rows))
	for i := range rows {
		if namespace != "" && rows[i].Namespace != namespace && rows[i].Target != namespace {
			continue
		}
		if severity != "" && rows[i].Severity != severity {
			continue
		}
		if !statusMatches(rows[i].Status, status) {
			continue
		}
		filtered = append(filtered, rows[i])
	}
	return filtered
}

func pageIncidentRows(rows []IncidentListItem, offset, limit int) []IncidentListItem {
	if offset >= len(rows) {
		return []IncidentListItem{}
	}
	rows = rows[offset:]
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func summarizeIncidents(rows []IncidentListItem) IncidentListSummary {
	summary := IncidentListSummary{
		Total:      len(rows),
		BySeverity: map[string]int{},
	}
	for i := range rows {
		switch canonicalStatus(rows[i].Status) {
		case "active":
			summary.Active++
		case "completed":
			summary.Completed++
		case "failed":
			summary.Failed++
		}
		if rows[i].Severity != "" {
			summary.BySeverity[rows[i].Severity]++
		}
	}
	return summary
}

func statusMatches(rowStatus, filter string) bool {
	if filter == "" {
		return true
	}
	return canonicalStatus(rowStatus) == canonicalStatus(filter)
}

func canonicalStatus(status string) string {
	switch strings.ToLower(status) {
	case "active", "pending", "running", "in_progress":
		return "active"
	case "completed", "remediated", "resolved":
		return "completed"
	case "failed":
		return "failed"
	default:
		return strings.ToLower(status)
	}
}

func storedListStatus(status models.IncidentStatus) string {
	if status == models.IncidentStatusResolved {
		return "completed"
	}
	return string(status)
}

func workflowListItem(workflow *models.Workflow, severity string) IncidentListItem {
	item := IncidentListItem{
		ID:         workflow.IncidentID,
		Namespace:  workflow.Namespace,
		Target:     workflow.Namespace,
		Resource:   workflow.ResourceKind + "/" + workflow.ResourceName,
		IssueType:  workflow.IssueType,
		ActionType: workflow.IssueType,
		Severity:   severity,
		Status:     workflowIncidentStatus(workflow.Status),
		CreatedAt:  workflow.CreatedAt.Format(time.RFC3339),
		WorkflowID: workflow.ID,
		Source:     "workflow",
	}
	if workflow.StartedAt != nil {
		item.StartedAt = workflow.StartedAt.Format(time.RFC3339)
	}
	if workflow.CompletedAt != nil {
		item.CompletedAt = workflow.CompletedAt.Format(time.RFC3339)
	}
	if workflow.StartedAt != nil && workflow.CompletedAt != nil {
		item.DurationSeconds = workflow.CompletedAt.Sub(*workflow.StartedAt).Seconds()
	}
	return item
}

func sortIncidentRows(rows []IncidentListItem) {
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].CreatedAt > rows[j].CreatedAt
	})
}

func workflowIncidentStatus(status models.WorkflowStatus) string {
	switch status {
	case models.WorkflowStatusCompleted:
		return "completed"
	case models.WorkflowStatusFailed:
		return "failed"
	default:
		return "running"
	}
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
