package models

import "time"

// HealthStatus represents the overall health status
type HealthStatus string

const (
	// HealthStatusHealthy indicates all systems are operational
	HealthStatusHealthy HealthStatus = "healthy"
	// HealthStatusDegraded indicates some non-critical issues
	HealthStatusDegraded HealthStatus = "degraded"
	// HealthStatusUnhealthy indicates critical issues
	HealthStatusUnhealthy HealthStatus = "unhealthy"
)

// ComponentStatus represents the status of an individual component
type ComponentStatus string

const (
	// ComponentStatusOK indicates component is healthy
	ComponentStatusOK ComponentStatus = "ok"
	// ComponentStatusDegraded indicates component has non-critical issues
	ComponentStatusDegraded ComponentStatus = "degraded"
	// ComponentStatusDown indicates component is unavailable
	ComponentStatusDown ComponentStatus = "down"
)

// DependencyHealth represents the health of an external dependency
type DependencyHealth struct {
	Name      string          `json:"name"`
	Status    ComponentStatus `json:"status"`
	Message   string          `json:"message,omitempty"`
	Latency   *int64          `json:"latency_ms,omitempty"` // Response time in milliseconds
	CheckedAt time.Time       `json:"checked_at"`
}

// RBACStatus represents RBAC permission check status
type RBACStatus struct {
	Status            ComponentStatus `json:"status"`
	PermissionsTotal  int             `json:"permissions_total"`
	PermissionsOK     int             `json:"permissions_ok"`
	PermissionsFailed int             `json:"permissions_failed"`
	CriticalOK        bool            `json:"critical_ok"`
	Message           string          `json:"message,omitempty"`
}

// HealthResponse represents the comprehensive health check response
type HealthResponse struct {
	Status       HealthStatus                `json:"status"`
	Timestamp    time.Time                   `json:"timestamp"`
	Version      string                      `json:"version"`
	Uptime       int64                       `json:"uptime_seconds"`
	Dependencies map[string]DependencyHealth `json:"dependencies"`
	RBAC         RBACStatus                  `json:"rbac"`
	Details      map[string]interface{}      `json:"details,omitempty"`
}

// NewHealthResponse creates a new health response with defaults
func NewHealthResponse(version string, startTime time.Time) *HealthResponse {
	return &HealthResponse{
		Status:       HealthStatusHealthy,
		Timestamp:    time.Now(),
		Version:      version,
		Uptime:       int64(time.Since(startTime).Seconds()),
		Dependencies: make(map[string]DependencyHealth),
		Details:      make(map[string]interface{}),
	}
}

// AddDependency adds a dependency health check result
func (h *HealthResponse) AddDependency(name string, dep *DependencyHealth) {
	h.Dependencies[name] = *dep

	// Update overall status based on dependency status
	if dep.Status == ComponentStatusDown {
		// Check if this is a critical dependency
		if name == "kubernetes" {
			h.Status = HealthStatusUnhealthy
		} else if h.Status == HealthStatusHealthy {
			h.Status = HealthStatusDegraded
		}
	} else if dep.Status == ComponentStatusDegraded && h.Status == HealthStatusHealthy {
		h.Status = HealthStatusDegraded
	}
}

// SetRBACStatus sets the RBAC status
func (h *HealthResponse) SetRBACStatus(rbac RBACStatus) {
	h.RBAC = rbac

	// Update overall status if RBAC has critical issues
	if !rbac.CriticalOK {
		h.Status = HealthStatusUnhealthy
	} else if rbac.PermissionsFailed > 0 && h.Status == HealthStatusHealthy {
		h.Status = HealthStatusDegraded
	}
}
