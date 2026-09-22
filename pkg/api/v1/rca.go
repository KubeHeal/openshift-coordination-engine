package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/KubeHeal/openshift-coordination-engine/internal/rca"
)

// RCAHandler handles deep root-cause analysis API requests (ADR-021, Issue #72).
type RCAHandler struct {
	clientset       kubernetes.Interface
	dynamicClient   dynamic.Interface
	discoveryClient discovery.DiscoveryInterface
	log             *logrus.Logger
}

// NewRCAHandler creates a new RCA endpoint handler.
func NewRCAHandler(
	clientset kubernetes.Interface,
	dynamicClient dynamic.Interface,
	discoveryClient discovery.DiscoveryInterface,
	log *logrus.Logger,
) *RCAHandler {
	return &RCAHandler{
		clientset:       clientset,
		dynamicClient:   dynamicClient,
		discoveryClient: discoveryClient,
		log:             log,
	}
}

// RegisterRoutes registers the RCA investigation endpoint.
func (h *RCAHandler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/api/v1/investigate/rca", h.InvestigateRCA).Methods("POST")
	h.log.Info("RCA investigation endpoint registered: POST /api/v1/investigate/rca")
}

// RCAInvestigateRequest is the wire-format request body.
type RCAInvestigateRequest struct {
	Service   string `json:"service"`
	Namespace string `json:"namespace"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

// RCAErrorResponse is returned on validation or processing errors.
type RCAErrorResponse struct {
	Status  string `json:"status"`
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
	Code    string `json:"code"`
}

const (
	rcaErrInvalidRequest = "INVALID_REQUEST"
	rcaErrAnalysisFailed = "ANALYSIS_FAILED"
)

// InvestigateRCA handles POST /api/v1/investigate/rca
func (h *RCAHandler) InvestigateRCA(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if contentType != "" && !strings.HasPrefix(contentType, "application/json") {
		h.respondError(w, http.StatusBadRequest, "Content-Type must be application/json", "", rcaErrInvalidRequest)
		return
	}

	var req RCAInvestigateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.WithError(err).Debug("Invalid RCA request format")
		h.respondError(w, http.StatusBadRequest, "Invalid request format", err.Error(), rcaErrInvalidRequest)
		return
	}

	rcaReq, err := h.validateAndParse(&req)
	if err != nil {
		h.log.WithError(err).Debug("RCA request validation failed")
		h.respondError(w, http.StatusBadRequest, err.Error(), "", rcaErrInvalidRequest)
		return
	}

	h.log.WithFields(logrus.Fields{
		"service":   rcaReq.Service,
		"namespace": rcaReq.Namespace,
		"start":     rcaReq.StartTime.Format(time.RFC3339),
		"end":       rcaReq.EndTime.Format(time.RFC3339),
	}).Info("Processing RCA investigation request")

	// Build correlators.
	eventCorr := rca.NewEventCorrelator(h.clientset, h.log)
	netpolCorr := rca.NewNetpolCorrelator(h.clientset, h.log)
	istioCorr := rca.NewIstioCorrelator(h.dynamicClient, h.discoveryClient, h.log)

	correlators := []rca.Correlator{eventCorr, netpolCorr, istioCorr}
	aggregator := rca.NewAggregator(correlators, istioCorr.IsAvailable(), h.log)

	result := aggregator.Run(rcaReq)

	h.log.WithFields(logrus.Fields{
		"root_causes":     len(result.RootCauses),
		"confidence":      result.ConfidenceScore,
		"istio_available": result.IstioAvailable,
	}).Info("RCA investigation completed")

	h.respondJSON(w, http.StatusOK, result)
}

// validateAndParse checks required fields and parses timestamps.
func (h *RCAHandler) validateAndParse(req *RCAInvestigateRequest) (*rca.Request, error) {
	if req.Service == "" {
		return nil, fmt.Errorf("service is required")
	}
	if req.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	if req.StartTime == "" {
		return nil, fmt.Errorf("start_time is required")
	}
	if req.EndTime == "" {
		return nil, fmt.Errorf("end_time is required")
	}

	start, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		return nil, fmt.Errorf("start_time must be RFC3339 format: %w", err)
	}
	end, err := time.Parse(time.RFC3339, req.EndTime)
	if err != nil {
		return nil, fmt.Errorf("end_time must be RFC3339 format: %w", err)
	}
	if !end.After(start) {
		return nil, fmt.Errorf("end_time must be after start_time")
	}
	if end.Sub(start) > 7*24*time.Hour {
		return nil, fmt.Errorf("time range must not exceed 7 days")
	}

	return &rca.Request{
		Service:   req.Service,
		Namespace: req.Namespace,
		StartTime: start,
		EndTime:   end,
	}, nil
}

func (h *RCAHandler) respondJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.WithError(err).Error("Failed to encode JSON response")
	}
}

func (h *RCAHandler) respondError(w http.ResponseWriter, code int, message, details, errCode string) {
	resp := RCAErrorResponse{
		Status:  "error",
		Error:   message,
		Details: details,
		Code:    errCode,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.log.WithError(err).Error("Failed to encode RCA error response")
	}
}
