package v1

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/internal/detector"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// DetectionHandler handles deployment detection API requests
type DetectionHandler struct {
	detector *detector.DeploymentDetector
	log      *logrus.Logger
}

// NewDetectionHandler creates a new detection API handler
func NewDetectionHandler(det *detector.DeploymentDetector, log *logrus.Logger) *DetectionHandler {
	return &DetectionHandler{
		detector: det,
		log:      log,
	}
}

// DetectionResponse represents the API response for deployment detection
type DetectionResponse struct {
	Success bool                   `json:"success"`
	Data    *models.DeploymentInfo `json:"data,omitempty"`
	Error   string                 `json:"error,omitempty"`
	Message string                 `json:"message,omitempty"`
}

// RegisterRoutes registers detection API routes
func (h *DetectionHandler) RegisterRoutes(router *mux.Router) {
	// Detection endpoints
	router.HandleFunc("/api/v1/detect/deployment/{namespace}/{name}", h.DetectDeployment).Methods("GET")
	router.HandleFunc("/api/v1/detect/statefulset/{namespace}/{name}", h.DetectStatefulSet).Methods("GET")
	router.HandleFunc("/api/v1/detect/daemonset/{namespace}/{name}", h.DetectDaemonSet).Methods("GET")
	router.HandleFunc("/api/v1/detect/cache/clear", h.ClearCache).Methods("POST")
	router.HandleFunc("/api/v1/detect/cache/stats", h.GetCacheStats).Methods("GET")

	h.log.Info("Detection API routes registered")
}

// DetectDeployment handles GET /api/v1/detect/deployment/{namespace}/{name}
// @Summary Detect deployment method for a Deployment
// @Description Detects how a Deployment was deployed (ArgoCD, Helm, Operator, Manual)
// @Tags detection
// @Produce json
// @Param namespace path string true "Namespace"
// @Param name path string true "Deployment name"
// @Success 200 {object} DetectionResponse
// @Failure 400 {object} DetectionResponse
// @Failure 404 {object} DetectionResponse
// @Failure 500 {object} DetectionResponse
// @Router /api/v1/detect/deployment/{namespace}/{name} [get]
func (h *DetectionHandler) DetectDeployment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]

	h.log.WithFields(logrus.Fields{
		"namespace":  namespace,
		"deployment": name,
		"endpoint":   "/api/v1/detect/deployment",
	}).Info("Deployment detection request received")

	// Validate inputs
	if namespace == "" || name == "" {
		h.respondError(w, http.StatusBadRequest, "namespace and name are required")
		return
	}

	// Detect deployment method
	info, err := h.detector.DetectDeploymentMethod(r.Context(), namespace, name)
	if err != nil {
		h.log.WithError(err).WithFields(logrus.Fields{
			"namespace":  namespace,
			"deployment": name,
		}).Error("Failed to detect deployment method")

		// Check if it's a not found error
		if isNotFoundError(err) {
			h.respondError(w, http.StatusNotFound, err.Error())
		} else {
			h.respondError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	h.log.WithFields(logrus.Fields{
		"namespace":  namespace,
		"deployment": name,
		"method":     info.Method,
		"confidence": info.Confidence,
	}).Info("Deployment method detected successfully")

	h.respondSuccess(w, info, "Deployment method detected successfully")
}

// DetectStatefulSet handles GET /api/v1/detect/statefulset/{namespace}/{name}
// @Summary Detect deployment method for a StatefulSet
// @Description Detects how a StatefulSet was deployed (ArgoCD, Helm, Operator, Manual)
// @Tags detection
// @Produce json
// @Param namespace path string true "Namespace"
// @Param name path string true "StatefulSet name"
// @Success 200 {object} DetectionResponse
// @Failure 400 {object} DetectionResponse
// @Failure 404 {object} DetectionResponse
// @Failure 500 {object} DetectionResponse
// @Router /api/v1/detect/statefulset/{namespace}/{name} [get]
func (h *DetectionHandler) DetectStatefulSet(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]

	h.log.WithFields(logrus.Fields{
		"namespace":   namespace,
		"statefulset": name,
		"endpoint":    "/api/v1/detect/statefulset",
	}).Info("StatefulSet detection request received")

	// Validate inputs
	if namespace == "" || name == "" {
		h.respondError(w, http.StatusBadRequest, "namespace and name are required")
		return
	}

	// Detect deployment method
	info, err := h.detector.DetectStatefulSetMethod(r.Context(), namespace, name)
	if err != nil {
		h.log.WithError(err).WithFields(logrus.Fields{
			"namespace":   namespace,
			"statefulset": name,
		}).Error("Failed to detect StatefulSet method")

		if isNotFoundError(err) {
			h.respondError(w, http.StatusNotFound, err.Error())
		} else {
			h.respondError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	h.log.WithFields(logrus.Fields{
		"namespace":   namespace,
		"statefulset": name,
		"method":      info.Method,
		"confidence":  info.Confidence,
	}).Info("StatefulSet method detected successfully")

	h.respondSuccess(w, info, "StatefulSet method detected successfully")
}

// DetectDaemonSet handles GET /api/v1/detect/daemonset/{namespace}/{name}
// @Summary Detect deployment method for a DaemonSet
// @Description Detects how a DaemonSet was deployed (ArgoCD, Helm, Operator, Manual)
// @Tags detection
// @Produce json
// @Param namespace path string true "Namespace"
// @Param name path string true "DaemonSet name"
// @Success 200 {object} DetectionResponse
// @Failure 400 {object} DetectionResponse
// @Failure 404 {object} DetectionResponse
// @Failure 500 {object} DetectionResponse
// @Router /api/v1/detect/daemonset/{namespace}/{name} [get]
func (h *DetectionHandler) DetectDaemonSet(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]

	h.log.WithFields(logrus.Fields{
		"namespace": namespace,
		"daemonset": name,
		"endpoint":  "/api/v1/detect/daemonset",
	}).Info("DaemonSet detection request received")

	// Validate inputs
	if namespace == "" || name == "" {
		h.respondError(w, http.StatusBadRequest, "namespace and name are required")
		return
	}

	// Detect deployment method
	info, err := h.detector.DetectDaemonSetMethod(r.Context(), namespace, name)
	if err != nil {
		h.log.WithError(err).WithFields(logrus.Fields{
			"namespace": namespace,
			"daemonset": name,
		}).Error("Failed to detect DaemonSet method")

		if isNotFoundError(err) {
			h.respondError(w, http.StatusNotFound, err.Error())
		} else {
			h.respondError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	h.log.WithFields(logrus.Fields{
		"namespace":  namespace,
		"daemonset":  name,
		"method":     info.Method,
		"confidence": info.Confidence,
	}).Info("DaemonSet method detected successfully")

	h.respondSuccess(w, info, "DaemonSet method detected successfully")
}

// ClearCache handles POST /api/v1/detect/cache/clear
// @Summary Clear detection cache
// @Description Clears all cached deployment detection results
// @Tags detection
// @Produce json
// @Success 200 {object} DetectionResponse
// @Router /api/v1/detect/cache/clear [post]
func (h *DetectionHandler) ClearCache(w http.ResponseWriter, r *http.Request) {
	h.log.Info("Cache clear request received")

	h.detector.ClearCache()

	h.log.Info("Detection cache cleared successfully")

	response := DetectionResponse{
		Success: true,
		Message: "Cache cleared successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode response")
	}
}

// GetCacheStats handles GET /api/v1/detect/cache/stats
// @Summary Get cache statistics
// @Description Returns statistics about the detection cache
// @Tags detection
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/detect/cache/stats [get]
func (h *DetectionHandler) GetCacheStats(w http.ResponseWriter, r *http.Request) {
	h.log.Debug("Cache stats request received")

	stats := h.detector.GetCacheStats()

	h.log.WithField("stats", stats).Debug("Returning cache stats")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    stats,
	}); err != nil {
		h.log.WithError(err).Error("Failed to encode cache stats response")
	}
}

// Helper methods

func (h *DetectionHandler) respondSuccess(w http.ResponseWriter, data *models.DeploymentInfo, message string) {
	response := DetectionResponse{
		Success: true,
		Data:    data,
		Message: message,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode response")
	}
}

func (h *DetectionHandler) respondError(w http.ResponseWriter, statusCode int, errorMsg string) {
	response := DetectionResponse{
		Success: false,
		Error:   errorMsg,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode error response")
	}
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	// Check for Kubernetes not found errors
	errMsg := err.Error()
	return errMsg != "" &&
		(strings.Contains(errMsg, "not found") ||
			strings.Contains(errMsg, "NotFound"))
}
