package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/tosin2013/openshift-coordination-engine/pkg/capacity"
)

func TestCapacityHandler_NamespaceWithQuota(t *testing.T) {
	// Create fake Kubernetes client with a namespace and resource quota
	objects := []runtime.Object{
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-namespace",
			},
		},
		&corev1.ResourceQuota{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "default",
				Namespace: "test-namespace",
			},
			Status: corev1.ResourceQuotaStatus{
				Hard: corev1.ResourceList{
					corev1.ResourceLimitsCPU:    resource.MustParse("10"),
					corev1.ResourceLimitsMemory: resource.MustParse("10Gi"),
					corev1.ResourcePods:         resource.MustParse("50"),
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-1",
				Namespace: "test-namespace",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-2",
				Namespace: "test-namespace",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(objects...)
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	handler := NewCapacityHandler(fakeClient, nil, logger)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v1/capacity/namespace/test-namespace?include_trending=false", http.NoBody)
	req = mux.SetURLVars(req, map[string]string{"namespace": "test-namespace"})

	// Create response recorder
	rr := httptest.NewRecorder()

	// Call handler
	handler.NamespaceCapacity(rr, req)

	// Assertions
	assert.Equal(t, http.StatusOK, rr.Code)

	var response NamespaceCapacityResponse
	err := json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "success", response.Status)
	assert.Equal(t, "test-namespace", response.Namespace)
	assert.True(t, response.Quota.HasQuota)
	assert.NotNil(t, response.Quota.CPU)
	assert.Equal(t, float64(10), response.Quota.CPU.LimitNumeric)
	assert.NotNil(t, response.Quota.Memory)
	assert.Equal(t, int64(50), response.Quota.PodCountLimit)
	assert.Equal(t, 2, response.CurrentUsage.PodCount)
}

func TestCapacityHandler_NamespaceWithoutQuota(t *testing.T) {
	// Create fake Kubernetes client with a namespace but no quota
	objects := []runtime.Object{
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "no-quota-namespace",
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "no-quota-namespace",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(objects...)
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	handler := NewCapacityHandler(fakeClient, nil, logger)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v1/capacity/namespace/no-quota-namespace?include_trending=false", http.NoBody)
	req = mux.SetURLVars(req, map[string]string{"namespace": "no-quota-namespace"})

	// Create response recorder
	rr := httptest.NewRecorder()

	// Call handler
	handler.NamespaceCapacity(rr, req)

	// Assertions
	assert.Equal(t, http.StatusOK, rr.Code)

	var response NamespaceCapacityResponse
	err := json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "success", response.Status)
	assert.Equal(t, "no-quota-namespace", response.Namespace)
	assert.False(t, response.Quota.HasQuota)
	assert.Equal(t, 1, response.CurrentUsage.PodCount)
}

func TestCapacityHandler_ClusterWide(t *testing.T) {
	// Create fake Kubernetes client with nodes and namespaces
	objects := []runtime.Object{
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "worker-1",
			},
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("16"),
					corev1.ResourceMemory: resource.MustParse("64Gi"),
				},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("15"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				},
			},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "worker-2",
			},
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("16"),
					corev1.ResourceMemory: resource.MustParse("64Gi"),
				},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("15"),
					corev1.ResourceMemory: resource.MustParse("60Gi"),
				},
			},
		},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-namespace",
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "test-namespace",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(objects...)
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	handler := NewCapacityHandler(fakeClient, nil, logger)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v1/capacity/cluster", http.NoBody)

	// Create response recorder
	rr := httptest.NewRecorder()

	// Call handler
	handler.ClusterCapacity(rr, req)

	// Assertions
	assert.Equal(t, http.StatusOK, rr.Code)

	var response ClusterCapacityResponse
	err := json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "success", response.Status)
	assert.Equal(t, "cluster", response.Scope)
	assert.NotNil(t, response.ClusterCapacity)
	assert.Equal(t, "32", response.ClusterCapacity.TotalCPU)
	assert.Equal(t, "30", response.ClusterCapacity.AllocatableCPU)
	assert.NotNil(t, response.ClusterUsage)
	assert.Equal(t, 1, response.ClusterUsage.PodCount)
}

func TestCapacityHandler_InvalidNamespace(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	handler := NewCapacityHandler(fakeClient, nil, logger)

	// Create request with empty namespace
	req := httptest.NewRequest(http.MethodGet, "/api/v1/capacity/namespace/", http.NoBody)
	req = mux.SetURLVars(req, map[string]string{"namespace": ""})

	// Create response recorder
	rr := httptest.NewRecorder()

	// Call handler
	handler.NamespaceCapacity(rr, req)

	// Assertions
	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var response CapacityErrorResponse
	err := json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "error", response.Status)
	assert.Contains(t, response.Message, "namespace is required")
}

func TestTrendingAnalysis_LinearRegression(t *testing.T) {
	// Test linear regression with known data
	dataPoints := []capacity.DataPoint{
		{Timestamp: time.Now().Add(-7 * 24 * time.Hour), Value: 10},
		{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 12},
		{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 14},
		{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 16},
		{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 18},
		{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 20},
		{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 22},
	}

	slope, intercept, rSquared := capacity.LinearRegression(dataPoints)

	// Perfect linear data should have slope of 2 per day
	assert.InDelta(t, 2.0, slope, 0.1)
	assert.InDelta(t, 10.0, intercept, 0.1)
	assert.InDelta(t, 1.0, rSquared, 0.01) // Should be nearly perfect fit
}

func TestTrendingAnalysis_DaysUntilThreshold(t *testing.T) {
	tests := []struct {
		name               string
		current            float64
		limit              float64
		dailyChangePercent float64
		threshold          float64
		expectedDays       int
	}{
		{
			name:               "Increasing usage",
			current:            60,
			limit:              100,
			dailyChangePercent: 5,
			threshold:          0.85,
			expectedDays:       9, // Need to go from 60 to 85 (25 units), 5% of 60 = 3 per day
		},
		{
			name:               "Already at threshold",
			current:            90,
			limit:              100,
			dailyChangePercent: 5,
			threshold:          0.85,
			expectedDays:       0,
		},
		{
			name:               "Decreasing usage",
			current:            60,
			limit:              100,
			dailyChangePercent: -5,
			threshold:          0.85,
			expectedDays:       -1, // Stable
		},
		{
			name:               "No limit",
			current:            60,
			limit:              0,
			dailyChangePercent: 5,
			threshold:          0.85,
			expectedDays:       -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days := capacity.DaysUntilThreshold(tt.current, tt.limit, tt.dailyChangePercent, tt.threshold)
			assert.Equal(t, tt.expectedDays, days)
		})
	}
}

func TestTrendingAnalysis_CalculateConfidence(t *testing.T) {
	// Test with small dataset
	smallData := []capacity.DataPoint{
		{Timestamp: time.Now().Add(-1 * time.Hour), Value: 10},
		{Timestamp: time.Now(), Value: 12},
	}
	smallConfidence := capacity.CalculateConfidence(smallData, 0.95)
	assert.Less(t, smallConfidence, 0.5) // Low confidence with little data

	// Test with larger dataset
	largeData := make([]capacity.DataPoint, 168)
	for i := 0; i < 168; i++ {
		largeData[i] = capacity.DataPoint{
			Timestamp: time.Now().Add(-time.Duration(168-i) * time.Hour),
			Value:     float64(10 + i),
		}
	}
	largeConfidence := capacity.CalculateConfidence(largeData, 0.99)
	assert.Greater(t, largeConfidence, 0.8) // High confidence with good data
}

func TestTrendDirection(t *testing.T) {
	tests := []struct {
		dailyChangePercent float64
		expectedDirection  capacity.TrendDirection
	}{
		{5.0, capacity.TrendDirectionIncreasing},
		{-5.0, capacity.TrendDirectionDecreasing},
		{0.0, capacity.TrendDirectionStable},
		{0.3, capacity.TrendDirectionStable}, // Below threshold
		{0.6, capacity.TrendDirectionIncreasing},
		{-0.6, capacity.TrendDirectionDecreasing},
	}

	for _, tt := range tests {
		direction := capacity.DetermineTrendDirection(tt.dailyChangePercent)
		assert.Equal(t, tt.expectedDirection, direction)
	}
}

func TestFormatCPU(t *testing.T) {
	tests := []struct {
		cores    float64
		expected string
	}{
		{1.0, "1000m"},
		{0.5, "500m"},
		{2.5, "2500m"},
		{0.001, "1m"},
	}

	for _, tt := range tests {
		result := formatCPU(tt.cores)
		assert.Equal(t, tt.expected, result)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{1024 * 1024 * 1024, "1.0Gi"},
		{1024 * 1024 * 512, "512.0Mi"},
		{1024 * 512, "512.0Ki"},
		{512, "512"},
	}

	for _, tt := range tests {
		result := formatBytes(tt.bytes)
		assert.Equal(t, tt.expected, result)
	}
}

func TestIsSystemNamespace(t *testing.T) {
	tests := []struct {
		namespace string
		isSystem  bool
	}{
		{"kube-system", true},
		{"openshift-monitoring", true},
		{"default", true},
		{"my-app", false},
		{"test-namespace", false},
		{"kube", false},
	}

	for _, tt := range tests {
		result := isSystemNamespace(tt.namespace)
		assert.Equal(t, tt.isSystem, result, "namespace: %s", tt.namespace)
	}
}

func TestParseBoolParam(t *testing.T) {
	tests := []struct {
		name         string
		queryString  string
		paramName    string
		defaultValue bool
		expected     bool
	}{
		{"true value", "param=true", "param", false, true},
		{"false value", "param=false", "param", true, false},
		{"missing value", "", "param", true, true},
		{"invalid value", "param=invalid", "param", true, true},
		{"1 value", "param=1", "param", false, true},
		{"0 value", "param=0", "param", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.queryString, http.NoBody)
			result := parseBoolParam(req, tt.paramName, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateAvailableCapacity(t *testing.T) {
	quota := &capacity.NamespaceQuota{
		CPU: &capacity.CPUQuota{
			Limit:        "10",
			LimitNumeric: 10.0,
		},
		Memory: &capacity.MemoryQuota{
			Limit:      "10Gi",
			LimitBytes: 10737418240,
		},
		PodCountLimit: 50,
		HasQuota:      true,
	}

	usage := &capacity.ResourceUsage{
		CPU: &capacity.CPUUsage{
			Used:        "6000m",
			UsedNumeric: 6.0,
			Percent:     60.0,
		},
		Memory: &capacity.MemoryUsage{
			Used:      "6Gi",
			UsedBytes: 6442450944,
			Percent:   60.0,
		},
		PodCount: 20,
	}

	available := capacity.CalculateAvailableCapacity(quota, usage)

	assert.NotNil(t, available.CPU)
	assert.InDelta(t, 4.0, available.CPU.AvailableNumeric, 0.1)
	assert.InDelta(t, 40.0, available.CPU.Percent, 0.1)

	assert.NotNil(t, available.Memory)
	assert.InDelta(t, 4294967296, available.Memory.AvailableBytes, 1000)
	assert.InDelta(t, 40.0, available.Memory.Percent, 0.1)

	assert.Equal(t, int64(30), available.PodSlots)
}

func TestAnalyzeTrend(t *testing.T) {
	// Create test data showing increasing CPU and memory usage
	cpuDataPoints := []capacity.DataPoint{
		{Timestamp: time.Now().Add(-7 * 24 * time.Hour), Value: 2.0},
		{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 2.2},
		{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 2.4},
		{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 2.6},
		{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 2.8},
		{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 3.0},
		{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 3.2},
	}

	memDataPoints := []capacity.DataPoint{
		{Timestamp: time.Now().Add(-7 * 24 * time.Hour), Value: 4000000000},
		{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 4200000000},
		{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 4400000000},
		{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 4600000000},
		{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 4800000000},
		{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 5000000000},
		{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 5200000000},
	}

	result := capacity.AnalyzeTrend(cpuDataPoints, memDataPoints, 3.2, 10.0, 5200000000, 10000000000)

	assert.NotNil(t, result)
	assert.NotNil(t, result.CPU)
	assert.Equal(t, capacity.TrendDirectionIncreasing, result.CPU.Direction)
	assert.Greater(t, result.CPU.DailyChangePercent, 0.0)

	assert.NotNil(t, result.Memory)
	assert.Equal(t, capacity.TrendDirectionIncreasing, result.Memory.Direction)

	assert.Greater(t, result.Confidence, 0.0)
}

func TestCapacityHandler_RegisterRoutes(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	logger := logrus.New()

	handler := NewCapacityHandler(fakeClient, nil, logger)
	router := mux.NewRouter()

	// Register routes
	handler.RegisterRoutes(router)

	// Test that routes are registered by making requests
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/capacity/namespace/test"},
		{http.MethodGet, "/api/v1/capacity/cluster"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, http.NoBody)
			match := &mux.RouteMatch{}
			matched := router.Match(req, match)
			assert.True(t, matched, "Route %s should be registered", tt.path)
		})
	}
}

// BenchmarkLinearRegression benchmarks the linear regression calculation
func BenchmarkLinearRegression(b *testing.B) {
	dataPoints := make([]capacity.DataPoint, 168)
	for i := 0; i < 168; i++ {
		dataPoints[i] = capacity.DataPoint{
			Timestamp: time.Now().Add(-time.Duration(168-i) * time.Hour),
			Value:     float64(10 + i),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		capacity.LinearRegression(dataPoints)
	}
}

// BenchmarkCalculateDailyChangePercent benchmarks the daily change calculation
func BenchmarkCalculateDailyChangePercent(b *testing.B) {
	dataPoints := make([]capacity.DataPoint, 168)
	for i := 0; i < 168; i++ {
		dataPoints[i] = capacity.DataPoint{
			Timestamp: time.Now().Add(-time.Duration(168-i) * time.Hour),
			Value:     float64(10 + i),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		capacity.CalculateDailyChangePercent(dataPoints)
	}
}
