# ADR-004: ArgoCD/MCO Integration

## Status
ACCEPTED - 2025-12-18

## Context

This ADR defines the Go implementation of ArgoCD and Machine Config Operator (MCO) integration as outlined in platform ADR-038 (ArgoCD/MCO Integration Boundaries). The coordination engine must respect clear boundaries when interacting with GitOps-managed applications and infrastructure-layer configurations.

Platform ADR-038 establishes strict integration boundaries:
- **ArgoCD**: Trigger sync operations, don't bypass GitOps workflow
- **MCO**: Monitor status read-only, don't create MachineConfigs

Without these boundaries, the engine risks configuration drift, audit trail loss, and conflicts with cluster operators.

## Decision

Implement ArgoCD and MCO integration in Go with the following components:

### 1. ArgoCD Client

Package: `internal/integrations/argocd_client.go`

```go
package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// ArgoCDClient interacts with ArgoCD API
type ArgoCDClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
	log        *logrus.Logger
}

// NewArgoCDClient creates a new ArgoCD API client
func NewArgoCDClient(baseURL, token string, log *logrus.Logger) *ArgoCDClient {
	return &ArgoCDClient{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: log,
	}
}

// ApplicationSyncRequest represents an ArgoCD sync request
type ApplicationSyncRequest struct {
	Prune     bool     `json:"prune"`
	DryRun    bool     `json:"dryRun"`
	Resources []string `json:"resources,omitempty"`
}

// ApplicationSyncResponse represents ArgoCD sync response
type ApplicationSyncResponse struct {
	Status    string `json:"status"`
	Revision  string `json:"revision"`
	Message   string `json:"message,omitempty"`
}

// ApplicationInfo contains ArgoCD application metadata
type ApplicationInfo struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	GitRepo      string `json:"gitRepo"`
	GitRevision  string `json:"gitRevision"`
	SyncStatus   string `json:"syncStatus"`
	HealthStatus string `json:"healthStatus"`
}

// SyncApplication triggers ArgoCD application sync
func (ac *ArgoCDClient) SyncApplication(ctx context.Context, appName string, prune bool) (*ApplicationSyncResponse, error) {
	ac.log.WithFields(logrus.Fields{
		"application": appName,
		"prune":       prune,
	}).Info("Triggering ArgoCD application sync")

	url := fmt.Sprintf("%s/api/v1/applications/%s/sync", ac.baseURL, appName)

	reqBody := ApplicationSyncRequest{
		Prune:  prune,
		DryRun: false,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal sync request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ac.token))

	resp, err := ac.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute sync request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ArgoCD sync failed with status %d", resp.StatusCode)
	}

	var syncResp ApplicationSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		return nil, fmt.Errorf("failed to decode sync response: %w", err)
	}

	ac.log.WithFields(logrus.Fields{
		"application": appName,
		"status":      syncResp.Status,
		"revision":    syncResp.Revision,
	}).Info("ArgoCD application sync triggered")

	return &syncResp, nil
}

// RefreshApplication triggers ArgoCD application refresh
func (ac *ArgoCDClient) RefreshApplication(ctx context.Context, appName string) error {
	ac.log.WithField("application", appName).Info("Refreshing ArgoCD application")

	url := fmt.Sprintf("%s/api/v1/applications/%s", ac.baseURL, appName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ac.token))
	req.URL.Query().Add("refresh", "hard")

	resp, err := ac.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to refresh application: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ArgoCD refresh failed with status %d", resp.StatusCode)
	}

	ac.log.WithField("application", appName).Info("ArgoCD application refreshed")
	return nil
}

// GetApplicationInfo retrieves ArgoCD application metadata
func (ac *ArgoCDClient) GetApplicationInfo(ctx context.Context, appName string) (*ApplicationInfo, error) {
	ac.log.WithField("application", appName).Debug("Fetching ArgoCD application info")

	url := fmt.Sprintf("%s/api/v1/applications/%s", ac.baseURL, appName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ac.token))

	resp, err := ac.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get application info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ArgoCD API returned status %d", resp.StatusCode)
	}

	var appResp struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			Source struct {
				RepoURL        string `json:"repoURL"`
				TargetRevision string `json:"targetRevision"`
			} `json:"source"`
		} `json:"spec"`
		Status struct {
			Sync struct {
				Status string `json:"status"`
			} `json:"sync"`
			Health struct {
				Status string `json:"status"`
			} `json:"health"`
		} `json:"status"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&appResp); err != nil {
		return nil, fmt.Errorf("failed to decode application info: %w", err)
	}

	info := &ApplicationInfo{
		Name:         appResp.Metadata.Name,
		Namespace:    appResp.Metadata.Namespace,
		GitRepo:      appResp.Spec.Source.RepoURL,
		GitRevision:  appResp.Spec.Source.TargetRevision,
		SyncStatus:   appResp.Status.Sync.Status,
		HealthStatus: appResp.Status.Health.Status,
	}

	return info, nil
}

// WaitForSyncCompletion polls ArgoCD until sync completes
func (ac *ArgoCDClient) WaitForSyncCompletion(ctx context.Context, appName string, timeout time.Duration) error {
	ac.log.WithFields(logrus.Fields{
		"application": appName,
		"timeout":     timeout,
	}).Info("Waiting for ArgoCD sync completion")

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		info, err := ac.GetApplicationInfo(ctx, appName)
		if err != nil {
			return err
		}

		if info.SyncStatus == "Synced" && info.HealthStatus == "Healthy" {
			ac.log.WithField("application", appName).Info("ArgoCD sync completed successfully")
			return nil
		}

		ac.log.WithFields(logrus.Fields{
			"application": appName,
			"sync_status": info.SyncStatus,
			"health":      info.HealthStatus,
		}).Debug("Waiting for sync completion")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			// Continue polling
		}
	}

	return fmt.Errorf("ArgoCD sync timed out after %v", timeout)
}
```

### 2. MCO Client

Package: `internal/integrations/mco_client.go`

```go
package integrations

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// MCOClient monitors Machine Config Operator status (read-only)
type MCOClient struct {
	dynamicClient dynamic.Interface
	log           *logrus.Logger
}

// NewMCOClient creates a new MCO monitoring client
func NewMCOClient(dynamicClient dynamic.Interface, log *logrus.Logger) *MCOClient {
	return &MCOClient{
		dynamicClient: dynamicClient,
		log:           log,
	}
}

// MachineConfigPoolStatus represents MCO pool status
type MachineConfigPoolStatus struct {
	Name                  string `json:"name"`
	MachineCount          int32  `json:"machineCount"`
	UpdatedMachineCount   int32  `json:"updatedMachineCount"`
	ReadyMachineCount     int32  `json:"readyMachineCount"`
	DegradedMachineCount  int32  `json:"degradedMachineCount"`
	Updating              bool   `json:"updating"`
	Degraded              bool   `json:"degraded"`
	CurrentConfiguration  string `json:"currentConfiguration"`
}

var (
	mcpGVR = schema.GroupVersionResource{
		Group:    "machineconfiguration.openshift.io",
		Version:  "v1",
		Resource: "machineconfigpools",
	}
)

// GetPoolStatus retrieves MachineConfigPool status
func (mc *MCOClient) GetPoolStatus(ctx context.Context, poolName string) (*MachineConfigPoolStatus, error) {
	mc.log.WithField("pool", poolName).Debug("Fetching MachineConfigPool status")

	pool, err := mc.dynamicClient.Resource(mcpGVR).Get(ctx, poolName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get MachineConfigPool %s: %w", poolName, err)
	}

	status, err := mc.parsePoolStatus(pool)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pool status: %w", err)
	}

	mc.log.WithFields(logrus.Fields{
		"pool":     poolName,
		"updating": status.Updating,
		"degraded": status.Degraded,
	}).Debug("MachineConfigPool status retrieved")

	return status, nil
}

// parsePoolStatus extracts status from unstructured MachineConfigPool
func (mc *MCOClient) parsePoolStatus(pool *unstructured.Unstructured) (*MachineConfigPoolStatus, error) {
	status := &MachineConfigPoolStatus{
		Name: pool.GetName(),
	}

	// Extract status fields
	statusMap, found, err := unstructured.NestedMap(pool.Object, "status")
	if err != nil || !found {
		return nil, fmt.Errorf("status not found in MachineConfigPool")
	}

	// Machine counts
	if count, found, _ := unstructured.NestedInt64(statusMap, "machineCount"); found {
		status.MachineCount = int32(count)
	}
	if count, found, _ := unstructured.NestedInt64(statusMap, "updatedMachineCount"); found {
		status.UpdatedMachineCount = int32(count)
	}
	if count, found, _ := unstructured.NestedInt64(statusMap, "readyMachineCount"); found {
		status.ReadyMachineCount = int32(count)
	}
	if count, found, _ := unstructured.NestedInt64(statusMap, "degradedMachineCount"); found {
		status.DegradedMachineCount = int32(count)
	}

	// Current configuration
	if config, found, _ := unstructured.NestedString(statusMap, "configuration", "name"); found {
		status.CurrentConfiguration = config
	}

	// Parse conditions
	conditions, found, err := unstructured.NestedSlice(statusMap, "conditions")
	if err == nil && found {
		for _, cond := range conditions {
			condMap, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}

			condType, _, _ := unstructured.NestedString(condMap, "type")
			condStatus, _, _ := unstructured.NestedString(condMap, "status")

			if condType == "Updating" && condStatus == "True" {
				status.Updating = true
			}
			if condType == "Degraded" && condStatus == "True" {
				status.Degraded = true
			}
		}
	}

	return status, nil
}

// IsPoolStable returns true if pool is not updating and not degraded
func (mc *MCOClient) IsPoolStable(ctx context.Context, poolName string) (bool, error) {
	status, err := mc.GetPoolStatus(ctx, poolName)
	if err != nil {
		return false, err
	}

	return !status.Updating && !status.Degraded, nil
}

// WaitForPoolStable waits for MachineConfigPool to become stable
func (mc *MCOClient) WaitForPoolStable(ctx context.Context, poolName string, timeout time.Duration) error {
	mc.log.WithFields(logrus.Fields{
		"pool":    poolName,
		"timeout": timeout,
	}).Info("Waiting for MachineConfigPool to stabilize")

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		stable, err := mc.IsPoolStable(ctx, poolName)
		if err != nil {
			return err
		}

		if stable {
			mc.log.WithField("pool", poolName).Info("MachineConfigPool is stable")
			return nil
		}

		status, _ := mc.GetPoolStatus(ctx, poolName)
		mc.log.WithFields(logrus.Fields{
			"pool":            poolName,
			"updating":        status.Updating,
			"degraded":        status.Degraded,
			"updated_count":   status.UpdatedMachineCount,
			"machine_count":   status.MachineCount,
		}).Debug("Waiting for pool to stabilize")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
			// Continue polling
		}
	}

	return fmt.Errorf("MachineConfigPool %s did not stabilize within %v", poolName, timeout)
}

// ListMachineConfigPools lists all MachineConfigPools
func (mc *MCOClient) ListMachineConfigPools(ctx context.Context) ([]string, error) {
	mc.log.Debug("Listing MachineConfigPools")

	pools, err := mc.dynamicClient.Resource(mcpGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list MachineConfigPools: %w", err)
	}

	var poolNames []string
	for _, pool := range pools.Items {
		poolNames = append(poolNames, pool.GetName())
	}

	return poolNames, nil
}
```

### 3. ArgoCD Remediator

Package: `internal/remediation/argocd_remediator.go`

```go
package remediation

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"openshift-coordination-engine/internal/integrations"
	"openshift-coordination-engine/pkg/models"
)

// ArgoCDRemediator handles GitOps-managed application remediation
type ArgoCDRemediator struct {
	argocdClient *integrations.ArgoCDClient
	log          *logrus.Logger
}

// NewArgoCDRemediator creates a new ArgoCD remediator
func NewArgoCDRemediator(argocdClient *integrations.ArgoCDClient, log *logrus.Logger) *ArgoCDRemediator {
	return &ArgoCDRemediator{
		argocdClient: argocdClient,
		log:          log,
	}
}

// Remediate triggers ArgoCD sync for GitOps-managed applications
func (ar *ArgoCDRemediator) Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo) error {
	ar.log.WithFields(logrus.Fields{
		"managed_by": deploymentInfo.ManagedBy,
		"namespace":  deploymentInfo.Namespace,
	}).Info("Starting ArgoCD remediation")

	// Step 1: Refresh application to detect new commits
	if err := ar.argocdClient.RefreshApplication(ctx, deploymentInfo.ManagedBy); err != nil {
		ar.log.WithError(err).Error("Failed to refresh ArgoCD application")
		return fmt.Errorf("refresh failed: %w", err)
	}

	// Step 2: Get current application status
	appInfo, err := ar.argocdClient.GetApplicationInfo(ctx, deploymentInfo.ManagedBy)
	if err != nil {
		return fmt.Errorf("failed to get application info: %w", err)
	}

	ar.log.WithFields(logrus.Fields{
		"application":   appInfo.Name,
		"sync_status":   appInfo.SyncStatus,
		"health_status": appInfo.HealthStatus,
		"git_repo":      appInfo.GitRepo,
	}).Info("ArgoCD application status")

	// Step 3: Trigger sync operation
	syncResp, err := ar.argocdClient.SyncApplication(ctx, deploymentInfo.ManagedBy, false)
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	ar.log.WithFields(logrus.Fields{
		"application": deploymentInfo.ManagedBy,
		"status":      syncResp.Status,
		"revision":    syncResp.Revision,
	}).Info("ArgoCD sync triggered")

	// Step 4: Wait for sync completion
	if err := ar.argocdClient.WaitForSyncCompletion(ctx, deploymentInfo.ManagedBy, 10*time.Minute); err != nil {
		return fmt.Errorf("sync completion failed: %w", err)
	}

	ar.log.WithField("application", deploymentInfo.ManagedBy).Info("ArgoCD remediation completed successfully")
	return nil
}

// CanRemediate returns true if deployment is ArgoCD-managed
func (ar *ArgoCDRemediator) CanRemediate(deploymentInfo *models.DeploymentInfo) bool {
	return deploymentInfo.Method == models.DeploymentMethodArgoCD && deploymentInfo.Managed
}
```

### 4. Infrastructure Layer Remediation with MCO

Package: `internal/coordination/infrastructure_layer.go`

```go
package coordination

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"openshift-coordination-engine/internal/integrations"
	"openshift-coordination-engine/pkg/models"
)

// InfrastructureLayerHandler handles infrastructure layer coordination
type InfrastructureLayerHandler struct {
	mcoClient *integrations.MCOClient
	log       *logrus.Logger
}

// NewInfrastructureLayerHandler creates infrastructure layer handler
func NewInfrastructureLayerHandler(mcoClient *integrations.MCOClient, log *logrus.Logger) *InfrastructureLayerHandler {
	return &InfrastructureLayerHandler{
		mcoClient: mcoClient,
		log:       log,
	}
}

// WaitForMCOStability waits for all MachineConfigPools to stabilize
func (ilh *InfrastructureLayerHandler) WaitForMCOStability(ctx context.Context) error {
	ilh.log.Info("Checking MCO stability before proceeding")

	pools, err := ilh.mcoClient.ListMachineConfigPools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list pools: %w", err)
	}

	for _, pool := range pools {
		ilh.log.WithField("pool", pool).Info("Waiting for MachineConfigPool to stabilize")

		if err := ilh.mcoClient.WaitForPoolStable(ctx, pool, 10*time.Minute); err != nil {
			return fmt.Errorf("pool %s failed to stabilize: %w", pool, err)
		}
	}

	ilh.log.Info("All MachineConfigPools are stable")
	return nil
}

// CheckInfrastructureReady verifies infrastructure layer is ready
func (ilh *InfrastructureLayerHandler) CheckInfrastructureReady(ctx context.Context) error {
	pools, err := ilh.mcoClient.ListMachineConfigPools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list pools: %w", err)
	}

	for _, pool := range pools {
		status, err := ilh.mcoClient.GetPoolStatus(ctx, pool)
		if err != nil {
			return fmt.Errorf("failed to get pool %s status: %w", pool, err)
		}

		if status.Updating {
			return fmt.Errorf("pool %s is updating", pool)
		}

		if status.Degraded {
			return fmt.Errorf("pool %s is degraded", pool)
		}

		if status.UpdatedMachineCount != status.MachineCount {
			return fmt.Errorf("pool %s has %d/%d machines updated",
				pool, status.UpdatedMachineCount, status.MachineCount)
		}
	}

	return nil
}
```

### 5. Metrics

Package: `internal/integrations/metrics.go`

```go
package integrations

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ArgoCDSyncTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_argocd_sync_total",
			Help: "Total number of ArgoCD sync operations",
		},
		[]string{"application", "status"},
	)

	ArgoCDSyncDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_argocd_sync_duration_seconds",
			Help:    "Duration of ArgoCD sync operations",
			Buckets: prometheus.ExponentialBuckets(10, 2, 8), // 10s to 1280s
		},
		[]string{"application"},
	)

	MCOPoolStatusChecks = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_mco_pool_status_checks_total",
			Help: "Total number of MCO pool status checks",
		},
		[]string{"pool", "status"},
	)

	MCOWaitDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_mco_wait_duration_seconds",
			Help:    "Time spent waiting for MCO stability",
			Buckets: prometheus.ExponentialBuckets(30, 2, 10), // 30s to 15360s
		},
		[]string{"pool"},
	)
)
```

## Configuration

Environment variables:
```bash
ARGOCD_API_URL=https://argocd-server.openshift-gitops.svc  # ArgoCD API endpoint
ARGOCD_TOKEN=                                               # ArgoCD API token (from ServiceAccount)
ARGOCD_SYNC_TIMEOUT=10m                                     # Sync operation timeout
MCO_STABILITY_TIMEOUT=10m                                   # MCO pool stability timeout
MCO_POLL_INTERVAL=10s                                       # MCO status poll interval
```

## RBAC Requirements

Required permissions (references platform ADR-033):

```yaml
# ArgoCD integration
- apiGroups: ["argoproj.io"]
  resources: ["applications"]
  verbs: ["get", "list", "watch"]

# MCO monitoring (read-only)
- apiGroups: ["machineconfiguration.openshift.io"]
  resources: ["machineconfigs", "machineconfigpools"]
  verbs: ["get", "list", "watch"]
```

## Testing Strategy

### Unit Tests

```go
func TestArgoCDClient_SyncApplication(t *testing.T) {
	// Mock ArgoCD API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/applications/test-app/sync", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

		resp := ApplicationSyncResponse{
			Status:   "success",
			Revision: "abc123",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewArgoCDClient(server.URL, "test-token", logrus.New())
	syncResp, err := client.SyncApplication(context.Background(), "test-app", false)

	assert.NoError(t, err)
	assert.Equal(t, "success", syncResp.Status)
	assert.Equal(t, "abc123", syncResp.Revision)
}

func TestMCOClient_GetPoolStatus(t *testing.T) {
	// Create fake dynamic client with MachineConfigPool
	pool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "worker",
			},
			"status": map[string]interface{}{
				"machineCount":        int64(3),
				"updatedMachineCount": int64(3),
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Updating",
						"status": "False",
					},
				},
			},
		},
	}

	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), pool)
	mcoClient := NewMCOClient(dynamicClient, logrus.New())

	status, err := mcoClient.GetPoolStatus(context.Background(), "worker")

	assert.NoError(t, err)
	assert.Equal(t, "worker", status.Name)
	assert.Equal(t, int32(3), status.MachineCount)
	assert.False(t, status.Updating)
}
```

### Integration Tests

```go
func TestArgoCDRemediator_IntegrationWithArgoCD(t *testing.T) {
	// Requires real ArgoCD server or comprehensive mock
	// Test full remediation flow:
	// 1. Refresh application
	// 2. Get application info
	// 3. Trigger sync
	// 4. Wait for completion
}

func TestInfrastructureLayerHandler_MCOIntegration(t *testing.T) {
	// Requires OpenShift cluster or mock MCO
	// Test MCO monitoring:
	// 1. List pools
	// 2. Get pool status
	// 3. Wait for stability
}
```

## Performance Characteristics

- **ArgoCD Sync Latency**: 30s - 10m (depending on application size)
- **MCO Stability Wait**: 5m - 30m (depending on node count and config changes)
- **API Call Overhead**: <100ms per ArgoCD/MCO API call
- **Polling Interval**: 5-10s for sync status, 10s for MCO pool status

## Consequences

### Positive
- ✅ **GitOps Compliance**: ArgoCD-managed apps remain under Git control
- ✅ **Audit Trail**: All changes reflected in Git history
- ✅ **MCO Safety**: No conflicts with cluster operators
- ✅ **Rollback Capability**: Git-based rollback for ArgoCD apps
- ✅ **Infrastructure Awareness**: Waits for MCO stability before app remediation
- ✅ **Type Safety**: Go types prevent API misuse

### Negative
- ⚠️ **Increased Latency**: ArgoCD sync slower than direct K8s API
- ⚠️ **Dependency**: Requires ArgoCD API access and proper authentication
- ⚠️ **MCO Wait Times**: Infrastructure changes can delay app remediation significantly
- ⚠️ **Complexity**: Multiple integration points increase code complexity

### Mitigation
- **Latency**: Parallelize non-dependent operations, optimize poll intervals
- **Dependency**: Graceful degradation if ArgoCD unavailable, fallback to direct K8s API for non-GitOps apps
- **Wait Times**: Configurable timeouts, async notifications when MCO stable
- **Complexity**: Clear interface boundaries, comprehensive testing

## References

- Platform ADR-038: ArgoCD/MCO Integration Boundaries (overall strategy)
- Platform ADR-033: RBAC Permissions (ArgoCD and MCO permissions)
- ADR-001: Go Project Architecture (package organization)
- ADR-002: Deployment Detection Implementation (ArgoCD detection)
- ADR-003: Multi-Layer Coordination Implementation (infrastructure layer)
- ArgoCD API: https://argo-cd.readthedocs.io/en/stable/developer-guide/api-docs/
- MCO Documentation: https://docs.openshift.com/container-platform/latest/post_installation_configuration/machine-configuration-tasks.html

## Related ADRs

- Platform ADR-038: ArgoCD/MCO Integration Boundaries
- ADR-001: Go Project Architecture
- ADR-002: Deployment Detection Implementation
- ADR-003: Multi-Layer Coordination Implementation
- ADR-005: Remediation Strategies Implementation (strategy pattern)
