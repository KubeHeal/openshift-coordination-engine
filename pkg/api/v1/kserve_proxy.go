// Package v1 provides API handlers for the coordination engine.
package v1

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/pkg/kserve"
)

// KServeProxyHandler handles KServe model proxy API requests (ADR-039, ADR-040)
type KServeProxyHandler struct {
	proxyClient *kserve.ProxyClient
	log         *logrus.Logger
}

// NewKServeProxyHandler creates a new KServe proxy API handler
func NewKServeProxyHandler(proxyClient *kserve.ProxyClient, log *logrus.Logger) *KServeProxyHandler {
	return &KServeProxyHandler{
		proxyClient: proxyClient,
		log:         log,
	}
}

// GetProxyClient returns the KServe proxy client for use by other handlers
func (h *KServeProxyHandler) GetProxyClient() *kserve.ProxyClient {
	return h.proxyClient
}

// RegisterRoutes registers KServe proxy API routes
func (h *KServeProxyHandler) RegisterRoutes(router *mux.Router) {
	// POST /api/v1/detect - Call KServe model for predictions
	router.HandleFunc("/api/v1/detect", h.HandleDetect).Methods("POST")

	// GET /api/v1/models - List all registered KServe models
	router.HandleFunc("/api/v1/models", h.ListModels).Methods("GET")

	// GET /api/v1/models/{model}/health - Check model health
	router.HandleFunc("/api/v1/models/{model}/health", h.CheckModelHealth).Methods("GET")

	h.log.Info("KServe proxy API routes registered: /api/v1/detect, /api/v1/models, /api/v1/models/{model}/health")
}

// HandleDetect handles POST /api/v1/detect
// @Summary Call KServe model for predictions
// @Description Proxies prediction requests to KServe InferenceServices
// @Tags kserve
// @Accept json
// @Produce json
// @Param request body kserve.DetectRequest true "Detection request"
// @Success 200 {object} kserve.DetectResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Router /api/v1/detect [post]
func (h *KServeProxyHandler) HandleDetect(w http.ResponseWriter, r *http.Request) {
	// Check content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "" && !strings.HasPrefix(contentType, "application/json") {
		h.respondError(w, http.StatusBadRequest, "Content-Type must be application/json")
		return
	}

	// Decode request
	var req kserve.DetectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.WithError(err).Debug("Invalid detect request format")
		h.respondError(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	// Validate request
	if req.Model == "" {
		h.respondError(w, http.StatusBadRequest, "Missing 'model' field")
		return
	}

	if len(req.Instances) == 0 {
		h.respondError(w, http.StatusBadRequest, "Missing 'instances' field")
		return
	}

	h.log.WithFields(logrus.Fields{
		"model":     req.Model,
		"instances": len(req.Instances),
	}).Info("KServe detect request received")

	// Call KServe model
	resp, err := h.proxyClient.Predict(r.Context(), req.Model, req.Instances)
	if err != nil {
		h.log.WithError(err).WithField("model", req.Model).Error("KServe prediction failed")

		// Check error type for appropriate HTTP status
		var notFoundErr *kserve.ModelNotFoundError
		var unavailableErr *kserve.ModelUnavailableError
		switch {
		case errors.As(err, &notFoundErr):
			h.respondError(w, http.StatusNotFound, err.Error())
		case errors.As(err, &unavailableErr):
			h.respondError(w, http.StatusServiceUnavailable, err.Error())
		default:
			h.respondError(w, http.StatusInternalServerError, "Prediction failed: "+err.Error())
		}
		return
	}

	h.log.WithFields(logrus.Fields{
		"model":       req.Model,
		"predictions": len(resp.Predictions),
	}).Info("KServe prediction successful")

	h.respondJSON(w, http.StatusOK, resp)
}

// ListModels handles GET /api/v1/models
// @Summary List all registered KServe models
// @Description Returns a list of all registered KServe InferenceServices
// @Tags kserve
// @Produce json
// @Success 200 {object} ModelsListResponse
// @Router /api/v1/models [get]
func (h *KServeProxyHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	h.log.Debug("List models request received")

	models := h.proxyClient.ListModels()

	response := ModelsListResponse{
		Models: models,
		Count:  len(models),
	}

	h.log.WithField("count", len(models)).Debug("Returning model list")

	h.respondJSON(w, http.StatusOK, response)
}

// CheckModelHealth handles GET /api/v1/models/{model}/health
// @Summary Check KServe model health
// @Description Checks the health status of a specific KServe model
// @Tags kserve
// @Produce json
// @Param model path string true "Model name"
// @Success 200 {object} kserve.ModelHealthResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/models/{model}/health [get]
func (h *KServeProxyHandler) CheckModelHealth(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	modelName := vars["model"]

	if modelName == "" {
		h.respondError(w, http.StatusBadRequest, "Model name is required")
		return
	}

	h.log.WithField("model", modelName).Debug("Model health check request received")

	health, err := h.proxyClient.CheckModelHealth(r.Context(), modelName)
	if err != nil {
		var notFoundErr *kserve.ModelNotFoundError
		if errors.As(err, &notFoundErr) {
			h.respondError(w, http.StatusNotFound, err.Error())
			return
		}
	}

	h.log.WithFields(logrus.Fields{
		"model":  modelName,
		"status": health.Status,
	}).Debug("Model health check completed")

	h.respondJSON(w, http.StatusOK, health)
}

// ModelsListResponse represents the response for listing models
type ModelsListResponse struct {
	Models []string `json:"models"`
	Count  int      `json:"count"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Success bool   `json:"success"`
}

// respondJSON writes a JSON response
func (h *KServeProxyHandler) respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.WithError(err).Error("Failed to encode JSON response")
	}
}

// respondError writes an error response
func (h *KServeProxyHandler) respondError(w http.ResponseWriter, statusCode int, message string) {
	response := ErrorResponse{
		Error:   message,
		Success: false,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode error response")
	}
}
