// Package v1 provides version 1 API handlers.
package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"github.com/tosin2013/openshift-coordination-engine/internal/integrations"
	"github.com/tosin2013/openshift-coordination-engine/pkg/capacity"
)

// CapacityHandler handles capacity analysis API requests
type CapacityHandler struct {
	analyzer         *capacity.Analyzer
	prometheusClient *integrations.PrometheusClient
	log              *logrus.Logger
}

// NewCapacityHandler creates a new capacity API handler
func NewCapacityHandler(k8sClient kubernetes.Interface, prometheusClient *integrations.PrometheusClient, log *logrus.Logger) *CapacityHandler {
	return &CapacityHandler{
		analyzer:         capacity.NewAnalyzer(k8sClient, log),
		prometheusClient: prometheusClient,
		log:              log,
	}
}

// NamespaceCapacityResponse represents the API response for namespace capacity
type NamespaceCapacityResponse struct {
	Status               string                        `json:"status"`
	Namespace            string                        `json:"namespace"`
	Timestamp            time.Time                     `json:"timestamp"`
	Quota                *capacity.NamespaceQuota      `json:"quota"`
	CurrentUsage         *capacity.ResourceUsage       `json:"current_usage"`
	Available            *capacity.AvailableCapacity   `json:"available"`
	Trending             *capacity.TrendingInfo        `json:"trending,omitempty"`
	InfrastructureImpact *capacity.InfrastructureImpact `json:"infrastructure_impact,omitempty"`
}

// ClusterCapacityResponse represents the API response for cluster-wide capacity
type ClusterCapacityResponse struct {
	Status          string                         `json:"status"`
	Scope           string                         `json:"scope"`
	Timestamp       time.Time                      `json:"timestamp"`
	ClusterCapacity *capacity.ClusterCapacity      `json:"cluster_capacity"`
	ClusterUsage    *capacity.ClusterUsage         `json:"cluster_usage"`
	Namespaces      []capacity.NamespaceSummary    `json:"namespaces"`
	Infrastructure  *capacity.ClusterInfrastructure `json:"infrastructure,omitempty"`
}

// CapacityErrorResponse represents an error response for capacity endpoints
type CapacityErrorResponse struct {
	Status  string `json:"status"`
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// RegisterRoutes registers capacity API routes
func (h *CapacityHandler) RegisterRoutes(router *mux.Router) {
	// Namespace capacity endpoint
	router.HandleFunc("/api/v1/capacity/namespace/{namespace}", h.NamespaceCapacity).Methods("GET")

	// Cluster-wide capacity endpoint
	router.HandleFunc("/api/v1/capacity/cluster", h.ClusterCapacity).Methods("GET")

	h.log.Info("Capacity API routes registered: /api/v1/capacity/namespace/{namespace}, /api/v1/capacity/cluster")
}

// NamespaceCapacity handles GET /api/v1/capacity/namespace/{namespace}
// @Summary Get namespace capacity analysis
// @Description Returns capacity analysis for a specific namespace including quota, usage, availability, trending, and infrastructure impact
// @Tags capacity
// @Produce json
// @Param namespace path string true "Namespace name"
// @Param include_trending query bool false "Include trending analysis (default: true)"
// @Param include_infrastructure query bool false "Include infrastructure impact analysis (default: false)"
// @Param window query string false "Trending window - 7d, 14d, 30d (default: 7d)"
// @Success 200 {object} NamespaceCapacityResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/capacity/namespace/{namespace} [get]
func (h *CapacityHandler) NamespaceCapacity(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]

	// Parse query parameters
	includeTrending := parseBoolParam(r, "include_trending", true)
	includeInfrastructure := parseBoolParam(r, "include_infrastructure", false)
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "7d"
	}

	h.log.WithFields(logrus.Fields{
		"namespace":              namespace,
		"include_trending":       includeTrending,
		"include_infrastructure": includeInfrastructure,
		"window":                 window,
	}).Info("Namespace capacity request received")

	// Validate namespace
	if namespace == "" {
		h.respondError(w, http.StatusBadRequest, "namespace is required")
		return
	}

	ctx := r.Context()

	// Get namespace quota
	quota, err := h.analyzer.GetNamespaceQuota(ctx, namespace)
	if err != nil {
		h.log.WithError(err).WithField("namespace", namespace).Error("Failed to get namespace quota")
		h.respondError(w, http.StatusInternalServerError, "failed to get namespace quota")
		return
	}

	// Get pod count
	podCount, err := h.analyzer.GetNamespacePodCount(ctx, namespace)
	if err != nil {
		h.log.WithError(err).WithField("namespace", namespace).Error("Failed to get pod count")
		h.respondError(w, http.StatusInternalServerError, "failed to get pod count")
		return
	}

	// Get current usage from Prometheus
	currentUsage := &capacity.ResourceUsage{
		PodCount: podCount,
	}

	if h.prometheusClient != nil && h.prometheusClient.IsAvailable() {
		cpuUsage, err := h.prometheusClient.GetNamespaceCPUUsage(ctx, namespace)
		if err != nil {
			h.log.WithError(err).Debug("Failed to get CPU usage from Prometheus")
		} else {
			percent := 0.0
			if quota.CPU != nil && quota.CPU.LimitNumeric > 0 {
				percent = (cpuUsage / quota.CPU.LimitNumeric) * 100
			}
			currentUsage.CPU = &capacity.CPUUsage{
				Used:        formatCPU(cpuUsage),
				UsedNumeric: cpuUsage,
				Percent:     percent,
			}
		}

		memUsage, err := h.prometheusClient.GetNamespaceMemoryUsage(ctx, namespace)
		if err != nil {
			h.log.WithError(err).Debug("Failed to get memory usage from Prometheus")
		} else {
			percent := 0.0
			if quota.Memory != nil && quota.Memory.LimitBytes > 0 {
				percent = (float64(memUsage) / float64(quota.Memory.LimitBytes)) * 100
			}
			currentUsage.Memory = &capacity.MemoryUsage{
				Used:      formatBytes(memUsage),
				UsedBytes: memUsage,
				Percent:   percent,
			}
		}
	}

	// Calculate available capacity
	available := capacity.CalculateAvailableCapacity(quota, currentUsage)

	// Build response
	response := &NamespaceCapacityResponse{
		Status:       "success",
		Namespace:    namespace,
		Timestamp:    time.Now().UTC(),
		Quota:        quota,
		CurrentUsage: currentUsage,
		Available:    available,
	}

	// Include trending analysis if requested
	if includeTrending && h.prometheusClient != nil && h.prometheusClient.IsAvailable() {
		trending := h.calculateTrending(ctx, namespace, window, quota, currentUsage)
		if trending != nil {
			response.Trending = trending
		}
	}

	// Include infrastructure impact if requested
	if includeInfrastructure && h.prometheusClient != nil && h.prometheusClient.IsAvailable() {
		infrastructure := h.calculateInfrastructureImpact(ctx)
		if infrastructure != nil {
			response.InfrastructureImpact = infrastructure
		}
	}

	h.log.WithFields(logrus.Fields{
		"namespace":    namespace,
		"has_quota":    quota.HasQuota,
		"pod_count":    podCount,
		"has_trending": response.Trending != nil,
	}).Info("Namespace capacity analysis completed")

	h.respondJSON(w, http.StatusOK, response)
}

// ClusterCapacity handles GET /api/v1/capacity/cluster
// @Summary Get cluster-wide capacity analysis
// @Description Returns cluster-wide capacity analysis including total capacity, usage, and namespace breakdown
// @Tags capacity
// @Produce json
// @Success 200 {object} ClusterCapacityResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/capacity/cluster [get]
func (h *CapacityHandler) ClusterCapacity(w http.ResponseWriter, r *http.Request) {
	h.log.Info("Cluster capacity request received")

	ctx := r.Context()

	// Get cluster capacity from nodes
	clusterCapacity, err := h.analyzer.GetClusterCapacity(ctx)
	if err != nil {
		h.log.WithError(err).Error("Failed to get cluster capacity")
		h.respondError(w, http.StatusInternalServerError, "failed to get cluster capacity")
		return
	}

	// Get cluster pod count
	podCount, err := h.analyzer.GetClusterPodCount(ctx)
	if err != nil {
		h.log.WithError(err).Error("Failed to get cluster pod count")
		h.respondError(w, http.StatusInternalServerError, "failed to get cluster pod count")
		return
	}

	// Get cluster usage from Prometheus
	clusterUsage := &capacity.ClusterUsage{
		PodCount: podCount,
	}

	if h.prometheusClient != nil && h.prometheusClient.IsAvailable() {
		cpuUsage, err := h.prometheusClient.GetClusterCPUUsage(ctx)
		if err != nil {
			h.log.WithError(err).Debug("Failed to get cluster CPU usage from Prometheus")
		} else {
			clusterUsage.CPU = &capacity.CPUUsage{
				Used:        formatCPU(cpuUsage),
				UsedNumeric: cpuUsage,
				Percent:     0, // Calculate based on allocatable CPU if needed
			}
		}

		memUsage, err := h.prometheusClient.GetClusterMemoryUsage(ctx)
		if err != nil {
			h.log.WithError(err).Debug("Failed to get cluster memory usage from Prometheus")
		} else {
			clusterUsage.Memory = &capacity.MemoryUsage{
				Used:      formatBytes(memUsage),
				UsedBytes: memUsage,
				Percent:   0, // Calculate based on allocatable memory if needed
			}
		}
	}

	// Get namespace summaries
	namespaces, err := h.analyzer.ListNamespaces(ctx)
	if err != nil {
		h.log.WithError(err).Error("Failed to list namespaces")
		h.respondError(w, http.StatusInternalServerError, "failed to list namespaces")
		return
	}

	namespaceSummaries := make([]capacity.NamespaceSummary, 0)
	for _, ns := range namespaces {
		// Skip system namespaces for summary
		if isSystemNamespace(ns) {
			continue
		}

		nsPodCount, err := h.analyzer.GetNamespacePodCount(ctx, ns)
		if err != nil {
			continue
		}

		summary := capacity.NamespaceSummary{
			Name:     ns,
			PodCount: nsPodCount,
		}

		// Get namespace resource usage if Prometheus is available
		if h.prometheusClient != nil && h.prometheusClient.IsAvailable() {
			cpuUsage, _ := h.prometheusClient.GetNamespaceCPURollingMean(ctx, ns)
			memUsage, _ := h.prometheusClient.GetNamespaceMemoryRollingMean(ctx, ns)
			summary.CPUPercent = cpuUsage * 100
			summary.MemoryPercent = memUsage * 100
		}

		namespaceSummaries = append(namespaceSummaries, summary)
	}

	// Build response
	response := &ClusterCapacityResponse{
		Status:          "success",
		Scope:           "cluster",
		Timestamp:       time.Now().UTC(),
		ClusterCapacity: clusterCapacity,
		ClusterUsage:    clusterUsage,
		Namespaces:      namespaceSummaries,
	}

	// Include infrastructure health
	if h.prometheusClient != nil && h.prometheusClient.IsAvailable() {
		response.Infrastructure = h.calculateClusterInfrastructure(ctx)
	}

	h.log.WithFields(logrus.Fields{
		"namespace_count": len(namespaceSummaries),
		"pod_count":       podCount,
	}).Info("Cluster capacity analysis completed")

	h.respondJSON(w, http.StatusOK, response)
}

// calculateTrending calculates trending data for a namespace
func (h *CapacityHandler) calculateTrending(ctx context.Context, namespace, window string, quota *capacity.NamespaceQuota, usage *capacity.ResourceUsage) *capacity.TrendingInfo {
	// Get CPU trend data
	cpuTrend, err := h.prometheusClient.GetNamespaceCPUTrend(ctx, namespace, window)
	if err != nil {
		h.log.WithError(err).Debug("Failed to get CPU trend data")
	}

	// Get memory trend data
	memTrend, err := h.prometheusClient.GetNamespaceMemoryTrend(ctx, namespace, window)
	if err != nil {
		h.log.WithError(err).Debug("Failed to get memory trend data")
	}

	// Convert Prometheus data points to capacity data points
	cpuDataPoints := make([]capacity.DataPoint, 0, len(cpuTrend))
	for _, dp := range cpuTrend {
		cpuDataPoints = append(cpuDataPoints, capacity.DataPoint{
			Timestamp: dp.Timestamp,
			Value:     dp.Value,
		})
	}

	memDataPoints := make([]capacity.DataPoint, 0, len(memTrend))
	for _, dp := range memTrend {
		memDataPoints = append(memDataPoints, capacity.DataPoint{
			Timestamp: dp.Timestamp,
			Value:     dp.Value,
		})
	}

	// Get current values and limits
	currentCPU := 0.0
	cpuLimit := 0.0
	if usage.CPU != nil {
		currentCPU = usage.CPU.UsedNumeric
	}
	if quota.CPU != nil {
		cpuLimit = quota.CPU.LimitNumeric
	}

	currentMemory := 0.0
	memoryLimit := 0.0
	if usage.Memory != nil {
		currentMemory = float64(usage.Memory.UsedBytes)
	}
	if quota.Memory != nil {
		memoryLimit = float64(quota.Memory.LimitBytes)
	}

	return capacity.AnalyzeTrend(cpuDataPoints, memDataPoints, currentCPU, cpuLimit, currentMemory, memoryLimit)
}

// calculateInfrastructureImpact calculates infrastructure impact metrics
func (h *CapacityHandler) calculateInfrastructureImpact(ctx context.Context) *capacity.InfrastructureImpact {
	impact := &capacity.InfrastructureImpact{
		ControlPlaneHealth: "unknown",
	}

	// Get etcd object count
	etcdCount, err := h.prometheusClient.GetEtcdObjectCount(ctx)
	if err == nil {
		impact.EtcdObjectCount = etcdCount
		// Assuming etcd max objects is around 10000 for capacity calculation
		impact.EtcdCapacityPercent = float64(etcdCount) / 10000.0 * 100
	}

	// Get API server QPS
	apiQPS, err := h.prometheusClient.GetAPIServerQPS(ctx)
	if err == nil {
		impact.APIServerQPS = apiQPS
	}

	// Get scheduler queue length
	queueLength, err := h.prometheusClient.GetSchedulerQueueLength(ctx)
	if err == nil {
		impact.SchedulerQueueLength = queueLength
	}

	// Get control plane health
	health, err := h.prometheusClient.GetControlPlaneHealth(ctx)
	if err == nil {
		impact.ControlPlaneHealth = health
	}

	return impact
}

// calculateClusterInfrastructure calculates cluster infrastructure metrics
func (h *CapacityHandler) calculateClusterInfrastructure(ctx context.Context) *capacity.ClusterInfrastructure {
	infrastructure := &capacity.ClusterInfrastructure{
		EtcdHealth: "unknown",
	}

	// Get control plane health
	health, err := h.prometheusClient.GetControlPlaneHealth(ctx)
	if err == nil {
		infrastructure.EtcdHealth = health
	}

	return infrastructure
}

// Helper functions

func parseBoolParam(r *http.Request, name string, defaultValue bool) bool {
	value := r.URL.Query().Get(name)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

func formatCPU(cores float64) string {
	millicores := int64(cores * 1000)
	return strconv.FormatInt(millicores, 10) + "m"
}

func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return strconv.FormatFloat(float64(bytes)/float64(GB), 'f', 1, 64) + "Gi"
	case bytes >= MB:
		return strconv.FormatFloat(float64(bytes)/float64(MB), 'f', 1, 64) + "Mi"
	case bytes >= KB:
		return strconv.FormatFloat(float64(bytes)/float64(KB), 'f', 1, 64) + "Ki"
	default:
		return strconv.FormatInt(bytes, 10)
	}
}

func isSystemNamespace(ns string) bool {
	systemPrefixes := []string{
		"kube-",
		"openshift-",
		"default",
	}
	for _, prefix := range systemPrefixes {
		if ns == prefix || len(ns) > len(prefix) && ns[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func (h *CapacityHandler) respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.WithError(err).Error("Failed to encode JSON response")
	}
}

func (h *CapacityHandler) respondError(w http.ResponseWriter, statusCode int, message string) {
	response := CapacityErrorResponse{
		Status:  "error",
		Error:   http.StatusText(statusCode),
		Message: message,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode error response")
	}
}
