// Package v1 provides API handlers for the coordination engine.
// ADR-019: VPA-style Right-Sizing Recommendation Engine
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/KubeHeal/openshift-coordination-engine/internal/integrations"
)

// RightSizingHandler handles CPU/memory right-sizing recommendation requests.
// It compares historical P95 usage against current resource requests and limits
// to suggest more accurate configuration for application teams (ADR-019).
type RightSizingHandler struct {
	prometheusClient *integrations.PrometheusClient
	log              *logrus.Logger
}

// NewRightSizingHandler creates a new right-sizing handler.
func NewRightSizingHandler(prometheusClient *integrations.PrometheusClient, log *logrus.Logger) *RightSizingHandler {
	return &RightSizingHandler{
		prometheusClient: prometheusClient,
		log:              log,
	}
}

// RegisterRoutes registers right-sizing API routes.
func (h *RightSizingHandler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/api/v1/recommendations/rightsizing", h.GetRightSizingRecommendations).Methods("GET")
	h.log.Info("Right-sizing endpoint registered: GET /api/v1/recommendations/rightsizing")
}

// ContainerRightSizingRecommendation is a per-container recommendation.
type ContainerRightSizingRecommendation struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container"`

	// CPU fields (all in cores)
	CurrentCPURequest    string  `json:"current_cpu_request"`
	CurrentCPULimit      string  `json:"current_cpu_limit"`
	P95CPUUsageCores     float64 `json:"p95_cpu_usage_cores"`
	RecommendedCPUReq    string  `json:"recommended_cpu_request"`
	RecommendedCPULimit  string  `json:"recommended_cpu_limit"`

	// Memory fields (all in bytes)
	CurrentMemoryRequest    string  `json:"current_memory_request"`
	CurrentMemoryLimit      string  `json:"current_memory_limit"`
	P95MemoryUsageBytes     float64 `json:"p95_memory_usage_bytes"`
	RecommendedMemoryReq    string  `json:"recommended_memory_request"`
	RecommendedMemoryLimit  string  `json:"recommended_memory_limit"`

	// Classification
	// Sizing is "over-provisioned", "under-provisioned", or "right-sized".
	CPUSizing    string `json:"cpu_sizing"`
	MemorySizing string `json:"memory_sizing"`

	// ThrottleRatePct is the CPU throttle rate (%) over the analysis window.
	// Nil when cAdvisor CFS metrics are not available.
	ThrottleRatePct *float64 `json:"throttle_rate_pct,omitempty"`
}

// RightSizingResponse is the response body for GET /api/v1/recommendations/rightsizing.
type RightSizingResponse struct {
	Status          string                                `json:"status"`
	Timestamp       time.Time                             `json:"timestamp"`
	Namespace       string                                `json:"namespace,omitempty"`
	AnalysisWindow  string                                `json:"analysis_window"`
	Recommendations []ContainerRightSizingRecommendation  `json:"recommendations"`
	OverProvisioned int                                   `json:"over_provisioned_count"`
	UnderProvisioned int                                  `json:"under_provisioned_count"`
	RightSized      int                                   `json:"right_sized_count"`
}

// GetRightSizingRecommendations handles GET /api/v1/recommendations/rightsizing
// @Summary Get CPU and memory right-sizing recommendations
// @Description Compares 30-day P95 resource usage against current requests/limits
// @Tags recommendations
// @Produce json
// @Param namespace query string false "Filter by namespace"
// @Param pod query string false "Filter by pod name (prefix)"
// @Param window query string false "Analysis window: 7d, 14d, 30d (default: 30d)"
// @Success 200 {object} RightSizingResponse
// @Router /api/v1/recommendations/rightsizing [get]
func (h *RightSizingHandler) GetRightSizingRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	namespace := r.URL.Query().Get("namespace")
	pod := r.URL.Query().Get("pod")
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "30d"
	}

	if h.prometheusClient == nil || !h.prometheusClient.IsAvailable() {
		h.rsRespondError(w, http.StatusServiceUnavailable, "Prometheus client not available — right-sizing requires 30-day metrics")
		return
	}

	recs, err := h.computeRecommendations(ctx, namespace, pod, window)
	if err != nil {
		h.log.WithError(err).Error("Failed to compute right-sizing recommendations")
		h.rsRespondError(w, http.StatusInternalServerError, fmt.Sprintf("Analysis failed: %v", err))
		return
	}

	over, under, right := 0, 0, 0
	for _, rec := range recs {
		if rec.CPUSizing == "over-provisioned" || rec.MemorySizing == "over-provisioned" {
			over++
		} else if rec.CPUSizing == "under-provisioned" || rec.MemorySizing == "under-provisioned" {
			under++
		} else {
			right++
		}
	}

	resp := RightSizingResponse{
		Status:           "success",
		Timestamp:        time.Now().UTC(),
		Namespace:        namespace,
		AnalysisWindow:   window,
		Recommendations:  recs,
		OverProvisioned:  over,
		UnderProvisioned: under,
		RightSized:       right,
	}
	h.rsRespondJSON(w, http.StatusOK, resp)
}

// computeRecommendations queries Prometheus for P95 usage vs current requests/limits.
// This aggregated implementation returns a single cluster-wide recommendation row.
// A production implementation would iterate per pod/container label set.
func (h *RightSizingHandler) computeRecommendations(ctx context.Context, namespace, pod, window string) ([]ContainerRightSizingRecommendation, error) {
	nsFilter, podFilter := "", ""
	if namespace != "" {
		nsFilter = fmt.Sprintf(`,namespace=%q`, namespace)
	}
	if pod != "" {
		podFilter = fmt.Sprintf(`,pod=~"%s.*"`, pod)
	}
	scope := nsFilter + podFilter

	// P95 CPU usage over window (cores)
	p95CPUQuery := fmt.Sprintf(
		`quantile_over_time(0.95, rate(container_cpu_usage_seconds_total{container!="",container!="POD"%s}[5m])[%s:5m])`,
		scope, window,
	)
	// P95 memory usage over window (bytes)
	p95MemQuery := fmt.Sprintf(
		`quantile_over_time(0.95, container_memory_working_set_bytes{container!="",container!="POD"%s}[%s:5m])`,
		scope, window,
	)
	// Current CPU request (cores)
	cpuReqQuery := fmt.Sprintf(
		`avg(kube_pod_container_resource_requests{resource="cpu",container!=""%s})`,
		scope,
	)
	// Current CPU limit (cores)
	cpuLimQuery := fmt.Sprintf(
		`avg(kube_pod_container_resource_limits{resource="cpu",container!=""%s})`,
		scope,
	)
	// Current memory request (bytes)
	memReqQuery := fmt.Sprintf(
		`avg(kube_pod_container_resource_requests{resource="memory",container!=""%s})`,
		scope,
	)
	// Current memory limit (bytes)
	memLimQuery := fmt.Sprintf(
		`avg(kube_pod_container_resource_limits{resource="memory",container!=""%s})`,
		scope,
	)
	// CPU throttle rate
	throttleQuery := fmt.Sprintf(
		`avg(rate(container_cpu_cfs_throttled_seconds_total{container!=""%s}[5m])) / avg(rate(container_cpu_cfs_periods_total{container!=""%s}[5m]))`,
		scope, scope,
	)

	p95CPU := h.queryOrDefault(ctx, fmt.Sprintf("avg(%s)", p95CPUQuery), 0)
	p95Mem := h.queryOrDefault(ctx, fmt.Sprintf("avg(%s)", p95MemQuery), 0)
	cpuReq := h.queryOrDefault(ctx, cpuReqQuery, 0.1)
	cpuLim := h.queryOrDefault(ctx, cpuLimQuery, 0.2)
	memReq := h.queryOrDefault(ctx, memReqQuery, 128*1024*1024)
	memLim := h.queryOrDefault(ctx, memLimQuery, 256*1024*1024)

	throttleVal := h.queryOrDefault(ctx, throttleQuery, -1)
	var throttlePtr *float64
	if throttleVal >= 0 {
		pct := math.Round(throttleVal*10000) / 100 // convert fraction → %
		throttlePtr = &pct
	}

	// Add 20% headroom above P95 for request; 50% headroom for limit
	recCPUReq := p95CPU * 1.20
	recCPULim := p95CPU * 1.50
	recMemReq := p95Mem * 1.20
	recMemLim := p95Mem * 1.50

	cpuSizing := classifySizing(cpuReq, p95CPU)
	memSizing := classifySizing(memReq, p95Mem)

	rec := ContainerRightSizingRecommendation{
		Namespace:            namespace,
		Pod:                  pod,
		Container:            "(aggregated)",
		CurrentCPURequest:    formatCores(cpuReq),
		CurrentCPULimit:      formatCores(cpuLim),
		P95CPUUsageCores:     math.Round(p95CPU*1000) / 1000,
		RecommendedCPUReq:    formatCores(recCPUReq),
		RecommendedCPULimit:  formatCores(recCPULim),
		CurrentMemoryRequest: formatBytes(int64(memReq)),
		CurrentMemoryLimit:   formatBytes(int64(memLim)),
		P95MemoryUsageBytes:  p95Mem,
		RecommendedMemoryReq: formatBytes(int64(recMemReq)),
		RecommendedMemoryLimit: formatBytes(int64(recMemLim)),
		CPUSizing:            cpuSizing,
		MemorySizing:         memSizing,
		ThrottleRatePct:      throttlePtr,
	}
	return []ContainerRightSizingRecommendation{rec}, nil
}

// classifySizing returns "over-provisioned", "under-provisioned", or "right-sized"
// based on how far the current request deviates from P95 usage.
func classifySizing(currentRequest, p95Usage float64) string {
	if p95Usage == 0 {
		return "right-sized"
	}
	ratio := currentRequest / p95Usage
	switch {
	case ratio > 2.0:
		return "over-provisioned"
	case ratio < 0.8:
		return "under-provisioned"
	default:
		return "right-sized"
	}
}

// formatCores formats a CPU value in cores to a Kubernetes-style string.
func formatCores(cores float64) string {
	if cores < 0.001 {
		return "1m"
	}
	if cores < 1.0 {
		return fmt.Sprintf("%dm", int(math.Round(cores*1000)))
	}
	return fmt.Sprintf("%.2f", cores)
}

// queryOrDefault runs a PromQL query and returns defaultVal on error.
func (h *RightSizingHandler) queryOrDefault(ctx context.Context, query string, defaultVal float64) float64 {
	v, err := h.prometheusClient.Query(ctx, query)
	if err != nil {
		h.log.WithError(err).WithField("query", query).Debug("Right-sizing query failed, using default")
		return defaultVal
	}
	return v
}

func (h *RightSizingHandler) rsRespondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.WithError(err).Error("Failed to encode JSON response")
	}
}

func (h *RightSizingHandler) rsRespondError(w http.ResponseWriter, statusCode int, message string) {
	type errResp struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	h.rsRespondJSON(w, statusCode, errResp{Status: "error", Error: message})
}
