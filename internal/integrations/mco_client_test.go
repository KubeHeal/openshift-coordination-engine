package integrations

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

// createMachineConfigPool creates a fake MachineConfigPool for testing
func createMachineConfigPool(name string, machineCount, updatedCount, readyCount, degradedCount int32, updating, degraded bool) *unstructured.Unstructured {
	pool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "machineconfiguration.openshift.io/v1",
			"kind":       "MachineConfigPool",
			"metadata": map[string]interface{}{
				"name": name,
			},
			"status": map[string]interface{}{
				"machineCount":         int64(machineCount),
				"updatedMachineCount":  int64(updatedCount),
				"readyMachineCount":    int64(readyCount),
				"degradedMachineCount": int64(degradedCount),
				"configuration": map[string]interface{}{
					"name": "rendered-" + name + "-abc123",
				},
				"conditions": []interface{}{},
			},
		},
	}

	// Add conditions
	conditions := []interface{}{}
	if updating {
		conditions = append(conditions, map[string]interface{}{
			"type":   "Updating",
			"status": "True",
		})
	} else {
		conditions = append(conditions, map[string]interface{}{
			"type":   "Updating",
			"status": "False",
		})
	}

	if degraded {
		conditions = append(conditions, map[string]interface{}{
			"type":   "Degraded",
			"status": "True",
		})
	} else {
		conditions = append(conditions, map[string]interface{}{
			"type":   "Degraded",
			"status": "False",
		})
	}

	status := pool.Object["status"].(map[string]interface{})
	status["conditions"] = conditions

	return pool
}

func TestNewMCOClient(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	dynamicClient := fake.NewSimpleDynamicClient(runtime.NewScheme())
	client := NewMCOClient(dynamicClient, log)

	assert.NotNil(t, client)
	assert.Equal(t, dynamicClient, client.dynamicClient)
	assert.Equal(t, log, client.log)
}

func TestMCOClient_GetPoolStatus(t *testing.T) {
	tests := []struct {
		name           string
		pool           *unstructured.Unstructured
		expectedStatus *MachineConfigPoolStatus
		expectError    bool
		errorContains  string
	}{
		{
			name: "stable pool",
			pool: createMachineConfigPool("worker", 3, 3, 3, 0, false, false),
			expectedStatus: &MachineConfigPoolStatus{
				Name:                 "worker",
				MachineCount:         3,
				UpdatedMachineCount:  3,
				ReadyMachineCount:    3,
				DegradedMachineCount: 0,
				Updating:             false,
				Degraded:             false,
				CurrentConfiguration: "rendered-worker-abc123",
			},
			expectError: false,
		},
		{
			name: "updating pool",
			pool: createMachineConfigPool("master", 3, 2, 2, 0, true, false),
			expectedStatus: &MachineConfigPoolStatus{
				Name:                 "master",
				MachineCount:         3,
				UpdatedMachineCount:  2,
				ReadyMachineCount:    2,
				DegradedMachineCount: 0,
				Updating:             true,
				Degraded:             false,
				CurrentConfiguration: "rendered-master-abc123",
			},
			expectError: false,
		},
		{
			name: "degraded pool",
			pool: createMachineConfigPool("worker", 5, 5, 4, 1, false, true),
			expectedStatus: &MachineConfigPoolStatus{
				Name:                 "worker",
				MachineCount:         5,
				UpdatedMachineCount:  5,
				ReadyMachineCount:    4,
				DegradedMachineCount: 1,
				Updating:             false,
				Degraded:             true,
				CurrentConfiguration: "rendered-worker-abc123",
			},
			expectError: false,
		},
		{
			name: "updating and degraded pool",
			pool: createMachineConfigPool("custom", 10, 7, 8, 2, true, true),
			expectedStatus: &MachineConfigPoolStatus{
				Name:                 "custom",
				MachineCount:         10,
				UpdatedMachineCount:  7,
				ReadyMachineCount:    8,
				DegradedMachineCount: 2,
				Updating:             true,
				Degraded:             true,
				CurrentConfiguration: "rendered-custom-abc123",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := logrus.New()
			log.SetLevel(logrus.FatalLevel) // Suppress logs during tests

			scheme := runtime.NewScheme()
			dynamicClient := fake.NewSimpleDynamicClient(scheme, tt.pool)
			client := NewMCOClient(dynamicClient, log)

			status, err := client.GetPoolStatus(context.Background(), tt.pool.GetName())

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus.Name, status.Name)
			assert.Equal(t, tt.expectedStatus.MachineCount, status.MachineCount)
			assert.Equal(t, tt.expectedStatus.UpdatedMachineCount, status.UpdatedMachineCount)
			assert.Equal(t, tt.expectedStatus.ReadyMachineCount, status.ReadyMachineCount)
			assert.Equal(t, tt.expectedStatus.DegradedMachineCount, status.DegradedMachineCount)
			assert.Equal(t, tt.expectedStatus.Updating, status.Updating)
			assert.Equal(t, tt.expectedStatus.Degraded, status.Degraded)
			assert.Equal(t, tt.expectedStatus.CurrentConfiguration, status.CurrentConfiguration)
		})
	}
}

func TestMCOClient_GetPoolStatus_NotFound(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)

	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme)
	client := NewMCOClient(dynamicClient, log)

	_, err := client.GetPoolStatus(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get MachineConfigPool")
}

func TestMCOClient_IsPoolStable(t *testing.T) {
	tests := []struct {
		name           string
		pool           *unstructured.Unstructured
		expectedStable bool
	}{
		{
			name:           "stable pool - all machines updated",
			pool:           createMachineConfigPool("worker", 3, 3, 3, 0, false, false),
			expectedStable: true,
		},
		{
			name:           "unstable pool - updating",
			pool:           createMachineConfigPool("worker", 3, 2, 2, 0, true, false),
			expectedStable: false,
		},
		{
			name:           "unstable pool - degraded",
			pool:           createMachineConfigPool("worker", 3, 3, 2, 1, false, true),
			expectedStable: false,
		},
		{
			name:           "unstable pool - machines not updated",
			pool:           createMachineConfigPool("worker", 5, 3, 5, 0, false, false),
			expectedStable: false,
		},
		{
			name:           "unstable pool - updating and degraded",
			pool:           createMachineConfigPool("worker", 3, 2, 2, 1, true, true),
			expectedStable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := logrus.New()
			log.SetLevel(logrus.FatalLevel)

			scheme := runtime.NewScheme()
			dynamicClient := fake.NewSimpleDynamicClient(scheme, tt.pool)
			client := NewMCOClient(dynamicClient, log)

			stable, err := client.IsPoolStable(context.Background(), tt.pool.GetName())

			require.NoError(t, err)
			assert.Equal(t, tt.expectedStable, stable)
		})
	}
}

func TestMCOClient_ListMachineConfigPools(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)

	pool1 := createMachineConfigPool("worker", 3, 3, 3, 0, false, false)
	pool2 := createMachineConfigPool("master", 3, 3, 3, 0, false, false)
	pool3 := createMachineConfigPool("custom", 5, 5, 5, 0, false, false)

	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, pool1, pool2, pool3)
	client := NewMCOClient(dynamicClient, log)

	pools, err := client.ListMachineConfigPools(context.Background())

	require.NoError(t, err)
	assert.Len(t, pools, 3)
	assert.Contains(t, pools, "worker")
	assert.Contains(t, pools, "master")
	assert.Contains(t, pools, "custom")
}

func TestMCOClient_ListMachineConfigPools_Empty(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)

	scheme := runtime.NewScheme()
	// Use custom list kinds to avoid panic when listing with no resources
	gvrToListKind := map[schema.GroupVersionResource]string{
		mcpGVR: "MachineConfigPoolList",
	}
	dynamicClient := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind)
	client := NewMCOClient(dynamicClient, log)

	pools, err := client.ListMachineConfigPools(context.Background())

	require.NoError(t, err)
	assert.Len(t, pools, 0)
}

func TestMCOClient_WaitForPoolStable_AlreadyStable(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)

	pool := createMachineConfigPool("worker", 3, 3, 3, 0, false, false)

	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, pool)
	client := NewMCOClient(dynamicClient, log)

	err := client.WaitForPoolStable(context.Background(), "worker", 1*time.Minute)

	assert.NoError(t, err)
}

func TestMCOClient_WaitForPoolStable_Timeout(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)

	// Pool that is updating (never becomes stable in this test)
	pool := createMachineConfigPool("worker", 3, 2, 2, 0, true, false)

	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, pool)
	client := NewMCOClient(dynamicClient, log)

	// Use a very short timeout for testing
	err := client.WaitForPoolStable(context.Background(), "worker", 100*time.Millisecond)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "did not stabilize")
}

func TestMCOClient_WaitForPoolStable_ContextCancelled(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)

	pool := createMachineConfigPool("worker", 3, 2, 2, 0, true, false)

	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, pool)
	client := NewMCOClient(dynamicClient, log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := client.WaitForPoolStable(ctx, "worker", 1*time.Minute)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context cancelled")
}

func TestMCOClient_WaitForAllPoolsStable(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)

	pool1 := createMachineConfigPool("worker", 3, 3, 3, 0, false, false)
	pool2 := createMachineConfigPool("master", 3, 3, 3, 0, false, false)

	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, pool1, pool2)
	client := NewMCOClient(dynamicClient, log)

	err := client.WaitForAllPoolsStable(context.Background(), 1*time.Minute)

	assert.NoError(t, err)
}

func TestMCOClient_WaitForAllPoolsStable_OneFails(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)

	pool1 := createMachineConfigPool("worker", 3, 3, 3, 0, false, false)
	pool2 := createMachineConfigPool("master", 3, 2, 2, 0, true, false) // Updating

	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, pool1, pool2)
	client := NewMCOClient(dynamicClient, log)

	err := client.WaitForAllPoolsStable(context.Background(), 100*time.Millisecond)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "master")
	assert.Contains(t, err.Error(), "failed to stabilize")
}

func TestMCOClient_WaitForAllPoolsStable_NoPools(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)

	scheme := runtime.NewScheme()
	// Use custom list kinds to avoid panic when listing with no resources
	gvrToListKind := map[schema.GroupVersionResource]string{
		mcpGVR: "MachineConfigPoolList",
	}
	dynamicClient := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind)
	client := NewMCOClient(dynamicClient, log)

	err := client.WaitForAllPoolsStable(context.Background(), 1*time.Minute)

	assert.NoError(t, err) // Should not error when no pools exist
}

func TestMCOClient_HealthCheck(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)

	pool := createMachineConfigPool("worker", 3, 3, 3, 0, false, false)

	scheme := runtime.NewScheme()
	dynamicClient := fake.NewSimpleDynamicClient(scheme, pool)
	client := NewMCOClient(dynamicClient, log)

	err := client.HealthCheck(context.Background())

	assert.NoError(t, err)
}

func TestMCOClient_ParsePoolStatus_MissingStatus(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)

	// Pool without status field
	pool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "machineconfiguration.openshift.io/v1",
			"kind":       "MachineConfigPool",
			"metadata": map[string]interface{}{
				"name": "invalid",
			},
			// No status field
		},
	}

	client := NewMCOClient(fake.NewSimpleDynamicClient(runtime.NewScheme()), log)

	_, err := client.parsePoolStatus(pool)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status not found")
}
