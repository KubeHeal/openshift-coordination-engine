// Package integrations provides clients for external service integrations (ArgoCD, ML service, MCO).
package integrations

import (
	"context"
	"fmt"
	"time"

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
	Name                 string `json:"name"`
	MachineCount         int32  `json:"machineCount"`
	UpdatedMachineCount  int32  `json:"updatedMachineCount"`
	ReadyMachineCount    int32  `json:"readyMachineCount"`
	DegradedMachineCount int32  `json:"degradedMachineCount"`
	Updating             bool   `json:"updating"`
	Degraded             bool   `json:"degraded"`
	CurrentConfiguration string `json:"currentConfiguration"`
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
		"ready":    status.ReadyMachineCount,
		"total":    status.MachineCount,
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

	// Parse machine counts
	mc.parseMachineCounts(statusMap, status)

	// Parse current configuration
	mc.parseConfiguration(statusMap, status)

	// Parse conditions
	mc.parseConditions(statusMap, status)

	return status, nil
}

// parseMachineCounts extracts machine count fields from status
func (mc *MCOClient) parseMachineCounts(statusMap map[string]interface{}, status *MachineConfigPoolStatus) {
	if count, found, err := unstructured.NestedInt64(statusMap, "machineCount"); err == nil && found {
		status.MachineCount = mc.safeInt32Convert(count)
	}
	if count, found, err := unstructured.NestedInt64(statusMap, "updatedMachineCount"); err == nil && found {
		status.UpdatedMachineCount = mc.safeInt32Convert(count)
	}
	if count, found, err := unstructured.NestedInt64(statusMap, "readyMachineCount"); err == nil && found {
		status.ReadyMachineCount = mc.safeInt32Convert(count)
	}
	if count, found, err := unstructured.NestedInt64(statusMap, "degradedMachineCount"); err == nil && found {
		status.DegradedMachineCount = mc.safeInt32Convert(count)
	}
}

// safeInt32Convert safely converts int64 to int32, clamping to int32 limits
func (mc *MCOClient) safeInt32Convert(val int64) int32 {
	const maxInt32 = 2147483647
	const minInt32 = -2147483648

	if val > maxInt32 {
		mc.log.WithField("value", val).Warn("Machine count exceeds int32 max, clamping to max")
		return maxInt32
	}
	if val < minInt32 {
		mc.log.WithField("value", val).Warn("Machine count below int32 min, clamping to min")
		return minInt32
	}
	return int32(val)
}

// parseConfiguration extracts current configuration name from status
func (mc *MCOClient) parseConfiguration(statusMap map[string]interface{}, status *MachineConfigPoolStatus) {
	if config, found, err := unstructured.NestedString(statusMap, "configuration", "name"); err == nil && found {
		status.CurrentConfiguration = config
	}
}

// parseConditions extracts updating and degraded flags from conditions
func (mc *MCOClient) parseConditions(statusMap map[string]interface{}, status *MachineConfigPoolStatus) {
	conditions, found, err := unstructured.NestedSlice(statusMap, "conditions")
	if err != nil || !found {
		return
	}

	for _, cond := range conditions {
		condMap, ok := cond.(map[string]interface{})
		if !ok {
			continue
		}

		condType, typeFound, typeErr := unstructured.NestedString(condMap, "type")
		condStatus, statusFound, statusErr := unstructured.NestedString(condMap, "status")

		if typeErr != nil || !typeFound || statusErr != nil || !statusFound {
			continue
		}

		if condType == "Updating" && condStatus == "True" {
			status.Updating = true
		}
		if condType == "Degraded" && condStatus == "True" {
			status.Degraded = true
		}
	}
}

// IsPoolStable returns true if pool is not updating and not degraded
func (mc *MCOClient) IsPoolStable(ctx context.Context, poolName string) (bool, error) {
	status, err := mc.GetPoolStatus(ctx, poolName)
	if err != nil {
		return false, err
	}

	// Pool is stable if:
	// 1. Not updating
	// 2. Not degraded
	// 3. All machines are updated
	stable := !status.Updating &&
		!status.Degraded &&
		status.UpdatedMachineCount == status.MachineCount

	mc.log.WithFields(logrus.Fields{
		"pool":    poolName,
		"stable":  stable,
		"updated": status.UpdatedMachineCount,
		"total":   status.MachineCount,
	}).Debug("Pool stability check")

	return stable, nil
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
			mc.log.WithError(err).Warn("Failed to check pool stability")
			// Continue polling instead of returning error
		} else if stable {
			mc.log.WithField("pool", poolName).Info("MachineConfigPool is stable")
			return nil
		}

		// Get current status for logging
		status, statusErr := mc.GetPoolStatus(ctx, poolName)
		if statusErr == nil {
			mc.log.WithFields(logrus.Fields{
				"pool":          poolName,
				"updating":      status.Updating,
				"degraded":      status.Degraded,
				"updated_count": status.UpdatedMachineCount,
				"machine_count": status.MachineCount,
			}).Debug("Waiting for pool to stabilize")
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for pool %s: %w", poolName, ctx.Err())
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

	poolNames := make([]string, 0, len(pools.Items))
	for _, pool := range pools.Items {
		poolNames = append(poolNames, pool.GetName())
	}

	mc.log.WithField("count", len(poolNames)).Debug("MachineConfigPools listed")

	return poolNames, nil
}

// WaitForAllPoolsStable waits for all MachineConfigPools to become stable
func (mc *MCOClient) WaitForAllPoolsStable(ctx context.Context, timeout time.Duration) error {
	mc.log.WithField("timeout", timeout).Info("Waiting for all MachineConfigPools to stabilize")

	pools, err := mc.ListMachineConfigPools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list pools: %w", err)
	}

	if len(pools) == 0 {
		mc.log.Warn("No MachineConfigPools found")
		return nil
	}

	// Wait for each pool sequentially
	for _, poolName := range pools {
		if err := mc.WaitForPoolStable(ctx, poolName, timeout); err != nil {
			return fmt.Errorf("pool %s failed to stabilize: %w", poolName, err)
		}
	}

	mc.log.Info("All MachineConfigPools are stable")
	return nil
}

// HealthCheck verifies MCO API is accessible by attempting to list pools
func (mc *MCOClient) HealthCheck(ctx context.Context) error {
	_, err := mc.ListMachineConfigPools(ctx)
	if err != nil {
		return fmt.Errorf("MCO health check failed: %w", err)
	}
	return nil
}
