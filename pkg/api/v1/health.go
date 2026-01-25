package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/tosin2013/openshift-coordination-engine/internal/rbac"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// HealthHandler handles health check requests
type HealthHandler struct {
	log          *logrus.Logger
	k8sClient    *kubernetes.Clientset
	rbacVerifier *rbac.Verifier
	mlServiceURL string
	version      string
	startTime    time.Time
	httpClient   *http.Client
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(log *logrus.Logger, k8sClient *kubernetes.Clientset, rbacVerifier *rbac.Verifier, mlServiceURL, version string, startTime time.Time) *HealthHandler {
	return &HealthHandler{
		log:          log,
		k8sClient:    k8sClient,
		rbacVerifier: rbacVerifier,
		mlServiceURL: mlServiceURL,
		version:      version,
		startTime:    startTime,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// ServeHTTP handles the health check request
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Create health response
	health := models.NewHealthResponse(h.version, h.startTime)

	// Check Kubernetes connectivity
	kubernetesHealth := h.checkKubernetes(ctx)
	health.AddDependency("kubernetes", &kubernetesHealth)

	// Check ML service connectivity (non-critical)
	mlServiceHealth := h.checkMLService(ctx)
	health.AddDependency("ml_service", &mlServiceHealth)

	// Check RBAC permissions
	rbacStatus := h.checkRBAC(ctx)
	health.SetRBACStatus(rbacStatus)

	// Add additional details
	health.Details["namespace"] = h.rbacVerifier
	health.Details["service_account"] = "self-healing-operator"

	// Set response headers
	w.Header().Set("Content-Type", "application/json")

	// Set HTTP status code based on health status
	switch health.Status {
	case models.HealthStatusHealthy:
		w.WriteHeader(http.StatusOK)
	case models.HealthStatusDegraded:
		w.WriteHeader(http.StatusOK) // 200 but degraded
	case models.HealthStatusUnhealthy:
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	// Encode response
	if err := json.NewEncoder(w).Encode(health); err != nil {
		h.log.WithError(err).Error("Failed to encode health response")
	}
}

// checkKubernetes verifies Kubernetes API connectivity
func (h *HealthHandler) checkKubernetes(ctx context.Context) models.DependencyHealth {
	start := time.Now()
	dep := models.DependencyHealth{
		Name:      "kubernetes",
		CheckedAt: time.Now(),
	}

	// Try to list namespaces with a limit
	_, err := h.k8sClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	latency := time.Since(start).Milliseconds()
	dep.Latency = &latency

	if err != nil {
		dep.Status = models.ComponentStatusDown
		dep.Message = fmt.Sprintf("Failed to connect: %v", err)
		h.log.WithError(err).Warn("Kubernetes health check failed")
	} else {
		dep.Status = models.ComponentStatusOK
		dep.Message = "Connected"
	}

	return dep
}

// checkMLService verifies ML service connectivity
func (h *HealthHandler) checkMLService(ctx context.Context) models.DependencyHealth {
	start := time.Now()
	dep := models.DependencyHealth{
		Name:      "ml_service",
		CheckedAt: time.Now(),
	}

	// Try to reach ML service health endpoint
	mlHealthURL := fmt.Sprintf("%s/health", h.mlServiceURL)

	req, err := http.NewRequestWithContext(ctx, "GET", mlHealthURL, http.NoBody)
	if err != nil {
		dep.Status = models.ComponentStatusDown
		dep.Message = fmt.Sprintf("Failed to create request: %v", err)
		return dep
	}

	resp, err := h.httpClient.Do(req)
	latency := time.Since(start).Milliseconds()
	dep.Latency = &latency

	if err != nil {
		// ML service is optional, so degraded instead of down
		dep.Status = models.ComponentStatusDegraded
		dep.Message = fmt.Sprintf("Unreachable: %v", err)
		h.log.WithError(err).Debug("ML service health check failed (non-critical)")
	} else {
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				h.log.WithError(closeErr).Warn("Failed to close ML service health check response body")
			}
		}()
		if resp.StatusCode == http.StatusOK {
			dep.Status = models.ComponentStatusOK
			dep.Message = "Connected"
		} else {
			dep.Status = models.ComponentStatusDegraded
			dep.Message = fmt.Sprintf("Returned status %d", resp.StatusCode)
		}
	}

	return dep
}

// checkRBAC verifies RBAC permissions
func (h *HealthHandler) checkRBAC(ctx context.Context) models.RBACStatus {
	status := models.RBACStatus{
		Status: models.ComponentStatusOK,
	}

	// Check critical permissions only (fast check)
	err := h.rbacVerifier.CheckCriticalPermissions(ctx)
	if err != nil {
		status.Status = models.ComponentStatusDown
		status.CriticalOK = false
		status.Message = fmt.Sprintf("Critical permissions missing: %v", err)
		h.log.WithError(err).Warn("RBAC critical permissions check failed")
		return status
	}

	status.CriticalOK = true

	// Optionally run full permission check (can be slow)
	// For health checks, we only verify critical permissions
	// Full verification can be done via separate endpoint or script
	status.Message = "Critical permissions verified"

	return status
}
