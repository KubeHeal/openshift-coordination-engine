package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewHealthResponse(t *testing.T) {
	version := "1.0.0"
	startTime := time.Now().Add(-5 * time.Minute)

	health := NewHealthResponse(version, startTime)

	assert.Equal(t, HealthStatusHealthy, health.Status)
	assert.Equal(t, version, health.Version)
	assert.NotZero(t, health.Uptime)
	assert.GreaterOrEqual(t, health.Uptime, int64(300)) // At least 5 minutes
	assert.NotNil(t, health.Dependencies)
	assert.NotNil(t, health.Details)
}

func TestHealthResponse_AddDependency_Healthy(t *testing.T) {
	health := NewHealthResponse("1.0.0", time.Now())

	dep := DependencyHealth{
		Name:      "database",
		Status:    ComponentStatusOK,
		Message:   "Connected",
		CheckedAt: time.Now(),
	}

	health.AddDependency("database", &dep)

	assert.Equal(t, HealthStatusHealthy, health.Status)
	assert.Contains(t, health.Dependencies, "database")
	assert.Equal(t, ComponentStatusOK, health.Dependencies["database"].Status)
}

func TestHealthResponse_AddDependency_Degraded(t *testing.T) {
	health := NewHealthResponse("1.0.0", time.Now())

	dep := DependencyHealth{
		Name:      "cache",
		Status:    ComponentStatusDegraded,
		Message:   "Slow response",
		CheckedAt: time.Now(),
	}

	health.AddDependency("cache", &dep)

	assert.Equal(t, HealthStatusDegraded, health.Status)
}

func TestHealthResponse_AddDependency_CriticalDown(t *testing.T) {
	health := NewHealthResponse("1.0.0", time.Now())

	dep := DependencyHealth{
		Name:      "kubernetes",
		Status:    ComponentStatusDown,
		Message:   "Connection failed",
		CheckedAt: time.Now(),
	}

	health.AddDependency("kubernetes", &dep)

	// Kubernetes is critical, so status should be unhealthy
	assert.Equal(t, HealthStatusUnhealthy, health.Status)
}

func TestHealthResponse_AddDependency_NonCriticalDown(t *testing.T) {
	health := NewHealthResponse("1.0.0", time.Now())

	dep := DependencyHealth{
		Name:      "ml_service",
		Status:    ComponentStatusDown,
		Message:   "Connection failed",
		CheckedAt: time.Now(),
	}

	health.AddDependency("ml_service", &dep)

	// ML service is non-critical, so status should be degraded
	assert.Equal(t, HealthStatusDegraded, health.Status)
}

func TestHealthResponse_SetRBACStatus_OK(t *testing.T) {
	health := NewHealthResponse("1.0.0", time.Now())

	rbac := RBACStatus{
		Status:            ComponentStatusOK,
		PermissionsTotal:  37,
		PermissionsOK:     37,
		PermissionsFailed: 0,
		CriticalOK:        true,
		Message:           "All permissions verified",
	}

	health.SetRBACStatus(rbac)

	assert.Equal(t, HealthStatusHealthy, health.Status)
	assert.True(t, health.RBAC.CriticalOK)
}

func TestHealthResponse_SetRBACStatus_CriticalMissing(t *testing.T) {
	health := NewHealthResponse("1.0.0", time.Now())

	rbac := RBACStatus{
		Status:            ComponentStatusDown,
		PermissionsTotal:  37,
		PermissionsOK:     34,
		PermissionsFailed: 3,
		CriticalOK:        false,
		Message:           "Critical permissions missing",
	}

	health.SetRBACStatus(rbac)

	assert.Equal(t, HealthStatusUnhealthy, health.Status)
	assert.False(t, health.RBAC.CriticalOK)
}

func TestHealthResponse_SetRBACStatus_SomePermissionsFailed(t *testing.T) {
	health := NewHealthResponse("1.0.0", time.Now())

	rbac := RBACStatus{
		Status:            ComponentStatusDegraded,
		PermissionsTotal:  37,
		PermissionsOK:     35,
		PermissionsFailed: 2,
		CriticalOK:        true,
		Message:           "Some non-critical permissions missing",
	}

	health.SetRBACStatus(rbac)

	// Critical permissions OK, so degraded not unhealthy
	assert.Equal(t, HealthStatusDegraded, health.Status)
	assert.True(t, health.RBAC.CriticalOK)
}

func TestHealthStatus_Constants(t *testing.T) {
	assert.Equal(t, HealthStatus("healthy"), HealthStatusHealthy)
	assert.Equal(t, HealthStatus("degraded"), HealthStatusDegraded)
	assert.Equal(t, HealthStatus("unhealthy"), HealthStatusUnhealthy)
}

func TestComponentStatus_Constants(t *testing.T) {
	assert.Equal(t, ComponentStatus("ok"), ComponentStatusOK)
	assert.Equal(t, ComponentStatus("degraded"), ComponentStatusDegraded)
	assert.Equal(t, ComponentStatus("down"), ComponentStatusDown)
}
