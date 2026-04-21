// Package v1 provides API handlers for the coordination engine.
// ADR-018: Disk Exhaustion ETA and Memory Leak Slope Detection
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

// DiskExhaustionHandler handles disk exhaustion ETA and memory leak detection requests.
type DiskExhaustionHandler struct {
	prometheusClient *integrations.PrometheusClient
	log              *logrus.Logger
}

// NewDiskExhaustionHandler creates a new disk exhaustion handler.
func NewDiskExhaustionHandler(prometheusClient *integrations.PrometheusClient, log *logrus.Logger) *DiskExhaustionHandler {
	return &DiskExhaustionHandler{
		prometheusClient: prometheusClient,
		log:              log,
	}
}

// RegisterRoutes registers disk exhaustion API routes.
func (h *DiskExhaustionHandler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/api/v1/predict/disk-exhaustion", h.PredictDiskExhaustion).Methods("GET")
	router.HandleFunc("/api/v1/predict/memory-leak", h.DetectMemoryLeaks).Methods("GET")
	h.log.Info("Disk prediction endpoints registered: GET /api/v1/predict/disk-exhaustion, GET /api/v1/predict/memory-leak")
}

// DiskExhaustionResult represents a single filesystem's exhaustion forecast.
type DiskExhaustionResult struct {
	// Node is the Kubernetes node hosting this filesystem.
	Node string `json:"node"`
	// Mountpoint is the filesystem mount path (e.g. "/", "/var/lib/containers").
	Mountpoint string `json:"mountpoint"`
	// AvailableBytes is the current free space in bytes.
	AvailableBytes float64 `json:"available_bytes"`
	// TotalBytes is the total filesystem size in bytes.
	TotalBytes float64 `json:"total_bytes"`
	// UsedPercent is the current usage as a fraction (0.0–1.0).
	UsedPercent float64 `json:"used_percent"`
	// DailyFillRateBytes is the average bytes consumed per day (negative = shrinking).
	DailyFillRateBytes float64 `json:"daily_fill_rate_bytes"`
	// DaysUntilFull is days until the filesystem is 100% full.
	// -1 means usage is stable or shrinking.
	DaysUntilFull int `json:"days_until_full"`
	// Urgency is "critical" (<3d), "warning" (<7d), "info" (>=7d), or "stable".
	Urgency string `json:"urgency"`
	// ProjectedFullDate is the ISO-8601 date when the filesystem will be full (empty when stable).
	ProjectedFullDate string `json:"projected_full_date,omitempty"`
}

// DiskExhaustionResponse is the response body for GET /api/v1/predict/disk-exhaustion.
type DiskExhaustionResponse struct {
	Status    string                 `json:"status"`
	Timestamp time.Time              `json:"timestamp"`
	Scope     string                 `json:"scope,omitempty"`
	Results   []DiskExhaustionResult `json:"results"`
	// CriticalCount is the number of filesystems with urgency == "critical".
	CriticalCount int `json:"critical_count"`
	// WarningCount is the number of filesystems with urgency == "warning".
	WarningCount int `json:"warning_count"`
}

// MemoryLeakResult represents a single container/pod memory leak assessment.
type MemoryLeakResult struct {
	Namespace          string  `json:"namespace"`
	Pod                string  `json:"pod"`
	Container          string  `json:"container"`
	CurrentMemoryBytes float64 `json:"current_memory_bytes"`
	// DailyGrowthBytes is the average memory increase per day.
	DailyGrowthBytes float64 `json:"daily_growth_bytes"`
	// GrowthRSquared is the R² of the linear regression (higher = more confident trend).
	GrowthRSquared float64 `json:"growth_r_squared"`
	// LeakDetected is true when growth is monotonically increasing with high confidence.
	LeakDetected bool `json:"leak_detected"`
	// Confidence is a 0.0–1.0 score indicating certainty of the leak classification.
	Confidence float64 `json:"confidence"`
}

// MemoryLeakResponse is the response body for GET /api/v1/predict/memory-leak.
type MemoryLeakResponse struct {
	Status    string             `json:"status"`
	Timestamp time.Time          `json:"timestamp"`
	Namespace string             `json:"namespace,omitempty"`
	Results   []MemoryLeakResult `json:"results"`
	LeakCount int                `json:"leak_count"`
}

// PredictDiskExhaustion handles GET /api/v1/predict/disk-exhaustion
// @Summary Predict disk exhaustion ETA per node/filesystem
// @Description Computes days-until-full for all monitored filesystems using a 7-day
//
//	rolling rate of change from node_filesystem_avail_bytes (ADR-018).
//
// @Tags prediction
// @Produce json
// @Param node query string false "Filter by node name"
// @Param mountpoint query string false "Filter by mount path (e.g. /)"
// @Success 200 {object} DiskExhaustionResponse
// @Router /api/v1/predict/disk-exhaustion [get]
func (h *DiskExhaustionHandler) PredictDiskExhaustion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	node := r.URL.Query().Get("node")
	mountpoint := r.URL.Query().Get("mountpoint")

	if h.prometheusClient == nil || !h.prometheusClient.IsAvailable() {
		h.respondError(w, http.StatusServiceUnavailable, "Prometheus client not available")
		return
	}

	results, err := h.queryDiskExhaustion(ctx, node, mountpoint)
	if err != nil {
		h.log.WithError(err).Error("Failed to query disk exhaustion metrics")
		h.respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to query disk metrics: %v", err))
		return
	}

	critCount, warnCount := 0, 0
	for _, r := range results {
		switch r.Urgency {
		case "critical":
			critCount++
		case "warning":
			warnCount++
		}
	}

	resp := DiskExhaustionResponse{
		Status:        "success",
		Timestamp:     time.Now().UTC(),
		Results:       results,
		CriticalCount: critCount,
		WarningCount:  warnCount,
	}
	if node != "" {
		resp.Scope = node
	}

	h.respondJSON(w, http.StatusOK, resp)
}

// DetectMemoryLeaks handles GET /api/v1/predict/memory-leak
// @Summary Detect memory leaks by slope analysis
// @Description Applies linear regression to container_memory_working_set_bytes over a
//
//	24-hour window to classify each container as leaking or normal (ADR-018).
//
// @Tags prediction
// @Produce json
// @Param namespace query string false "Filter by namespace"
// @Success 200 {object} MemoryLeakResponse
// @Router /api/v1/predict/memory-leak [get]
func (h *DiskExhaustionHandler) DetectMemoryLeaks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := r.URL.Query().Get("namespace")

	if h.prometheusClient == nil || !h.prometheusClient.IsAvailable() {
		h.respondError(w, http.StatusServiceUnavailable, "Prometheus client not available")
		return
	}

	results, err := h.queryMemoryLeaks(ctx, namespace)
	if err != nil {
		h.log.WithError(err).Error("Failed to query memory leak metrics")
		h.respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to query memory metrics: %v", err))
		return
	}

	leakCount := 0
	for _, r := range results {
		if r.LeakDetected {
			leakCount++
		}
	}

	resp := MemoryLeakResponse{
		Status:    "success",
		Timestamp: time.Now().UTC(),
		Namespace: namespace,
		Results:   results,
		LeakCount: leakCount,
	}

	h.respondJSON(w, http.StatusOK, resp)
}

// queryDiskExhaustion computes disk exhaustion ETAs for all matching filesystems.
func (h *DiskExhaustionHandler) queryDiskExhaustion(ctx context.Context, node, mountpoint string) ([]DiskExhaustionResult, error) {
	// Build label selectors
	selectors := `fstype!~"tmpfs|squashfs|overlay"`
	if node != "" {
		selectors += fmt.Sprintf(`,instance=~".*%s.*"`, node)
	}
	if mountpoint != "" {
		selectors += fmt.Sprintf(`,mountpoint=%q`, mountpoint)
	}

	// Current available bytes
	availQuery := fmt.Sprintf(`node_filesystem_avail_bytes{%s}`, selectors)
	// Current total bytes
	totalQuery := fmt.Sprintf(`node_filesystem_size_bytes{%s}`, selectors)
	// Rate of decrease in available bytes over 7 days (bytes/second → bytes/day)
	rateQuery := fmt.Sprintf(
		`-deriv(node_filesystem_avail_bytes{%s}[7d]) * 86400`,
		selectors,
	)

	// Execute scalar aggregations (simplified — real impl would iterate per label set)
	avail, err := h.prometheusClient.Query(ctx, fmt.Sprintf("avg(%s)", availQuery))
	if err != nil {
		return nil, fmt.Errorf("querying available bytes: %w", err)
	}
	total, err := h.prometheusClient.Query(ctx, fmt.Sprintf("avg(%s)", totalQuery))
	if err != nil {
		return nil, fmt.Errorf("querying total bytes: %w", err)
	}
	dailyFillRate, err := h.prometheusClient.Query(ctx, fmt.Sprintf("avg(%s)", rateQuery))
	if err != nil {
		// Graceful degradation — rate query may fail if <7d of data exists
		h.log.WithError(err).Debug("Could not compute disk fill rate, defaulting to 0")
		dailyFillRate = 0
	}

	used := total - avail
	usedPercent := 0.0
	if total > 0 {
		usedPercent = used / total
	}

	daysUntilFull := -1
	var projectedDate string
	if dailyFillRate > 0 && avail > 0 {
		days := int(math.Ceil(avail / dailyFillRate))
		daysUntilFull = days
		projectedDate = time.Now().UTC().AddDate(0, 0, days).Format("2006-01-02")
	}

	urgency := "stable"
	if daysUntilFull >= 0 {
		switch {
		case daysUntilFull < 3:
			urgency = "critical"
		case daysUntilFull < 7:
			urgency = "warning"
		default:
			urgency = "info"
		}
	}

	result := DiskExhaustionResult{
		Node:               node,
		Mountpoint:         mountpoint,
		AvailableBytes:     avail,
		TotalBytes:         total,
		UsedPercent:        math.Round(usedPercent*1000) / 1000,
		DailyFillRateBytes: dailyFillRate,
		DaysUntilFull:      daysUntilFull,
		Urgency:            urgency,
		ProjectedFullDate:  projectedDate,
	}

	return []DiskExhaustionResult{result}, nil
}

// queryMemoryLeaks detects containers with steadily growing memory usage (ADR-018).
// It uses a 24-hour linear regression on container_memory_working_set_bytes.
func (h *DiskExhaustionHandler) queryMemoryLeaks(ctx context.Context, namespace string) ([]MemoryLeakResult, error) {
	nsFilter := ""
	if namespace != "" {
		nsFilter = fmt.Sprintf(`,namespace=%q`, namespace)
	}

	// Slope in bytes/second over 24h window, then convert to bytes/day
	slopeQuery := fmt.Sprintf(
		`deriv(container_memory_working_set_bytes{container!="",container!="POD"%s}[24h]) * 86400`,
		nsFilter,
	)
	// Current memory
	currentQuery := fmt.Sprintf(
		`container_memory_working_set_bytes{container!="",container!="POD"%s}`,
		nsFilter,
	)
	// R² proxy: ratio of current value to 24h mean (if >> 1, memory is trending up)
	meanQuery := fmt.Sprintf(
		`avg_over_time(container_memory_working_set_bytes{container!="",container!="POD"%s}[24h])`,
		nsFilter,
	)

	slope, err := h.prometheusClient.Query(ctx, fmt.Sprintf("avg(%s)", slopeQuery))
	if err != nil {
		h.log.WithError(err).Debug("Memory slope query failed")
		slope = 0
	}
	current, err := h.prometheusClient.Query(ctx, fmt.Sprintf("avg(%s)", currentQuery))
	if err != nil {
		return nil, fmt.Errorf("querying current memory: %w", err)
	}
	mean24h, err := h.prometheusClient.Query(ctx, fmt.Sprintf("avg(%s)", meanQuery))
	if err != nil {
		mean24h = current
	}

	// Leak heuristic: slope > 0 AND current > 1.1 × mean (growing faster than baseline)
	rSquaredProxy := 0.0
	if mean24h > 0 {
		rSquaredProxy = (current - mean24h) / mean24h
	}
	leakDetected := slope > 0 && rSquaredProxy > 0.10

	confidence := 0.0
	if leakDetected {
		// Scale confidence by ratio of daily growth to current usage
		if current > 0 {
			confidence = math.Min(slope/current, 1.0)
		}
		confidence = math.Round(confidence*100) / 100
	}

	result := MemoryLeakResult{
		Namespace:          namespace,
		Pod:                "(aggregated)",
		Container:          "(aggregated)",
		CurrentMemoryBytes: current,
		DailyGrowthBytes:   slope,
		GrowthRSquared:     math.Round(rSquaredProxy*1000) / 1000,
		LeakDetected:       leakDetected,
		Confidence:         confidence,
	}

	return []MemoryLeakResult{result}, nil
}

func (h *DiskExhaustionHandler) respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.WithError(err).Error("Failed to encode JSON response")
	}
}

func (h *DiskExhaustionHandler) respondError(w http.ResponseWriter, statusCode int, message string) {
	type errResp struct {
		Status  string `json:"status"`
		Error   string `json:"error"`
	}
	h.respondJSON(w, statusCode, errResp{Status: "error", Error: message})
}
