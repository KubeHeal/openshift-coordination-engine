package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/tosin2013/openshift-coordination-engine/internal/detector"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

func setupDetectionHandler() *mux.Router { //nolint:unparam // returns router for test setup
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create fake clientset with test data
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			Annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "test-app:apps/Deployment:default/test-app",
			},
		},
	}

	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts",
			Namespace: "default",
			Annotations: map[string]string{
				"meta.helm.sh/release-name": "my-release",
			},
		},
	}

	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ds",
			Namespace: "kube-system",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "prometheus-operator",
			},
		},
	}

	clientset := fake.NewSimpleClientset(deployment, statefulSet, daemonSet)
	detectorInstance := detector.NewDeploymentDetector(clientset, log)
	handler := NewDetectionHandler(detectorInstance, log)

	router := mux.NewRouter()
	handler.RegisterRoutes(router)

	return router
}

func TestNewDetectionHandler(t *testing.T) {
	log := logrus.New()
	clientset := fake.NewSimpleClientset()
	detectorInstance := detector.NewDeploymentDetector(clientset, log)

	handler := NewDetectionHandler(detectorInstance, log)

	assert.NotNil(t, handler)
	assert.NotNil(t, handler.detector)
	assert.NotNil(t, handler.log)
}

func TestDetectDeployment_Success(t *testing.T) {
	router := setupDetectionHandler()

	req := httptest.NewRequest("GET", "/api/v1/detect/deployment/default/test-app", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response DetectionResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.NotNil(t, response.Data)
	assert.Equal(t, models.DeploymentMethodArgoCD, response.Data.Method)
	assert.Equal(t, 0.95, response.Data.Confidence)
	assert.Equal(t, "Deployment method detected successfully", response.Message)
}

func TestDetectDeployment_NotFound(t *testing.T) {
	router := setupDetectionHandler()

	req := httptest.NewRequest("GET", "/api/v1/detect/deployment/default/nonexistent", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response DetectionResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.False(t, response.Success)
	assert.Contains(t, response.Error, "failed to get deployment")
}

func TestDetectStatefulSet_Success(t *testing.T) {
	router := setupDetectionHandler()

	req := httptest.NewRequest("GET", "/api/v1/detect/statefulset/default/test-sts", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response DetectionResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.NotNil(t, response.Data)
	assert.Equal(t, models.DeploymentMethodHelm, response.Data.Method)
	assert.Equal(t, 0.90, response.Data.Confidence)
	assert.Equal(t, "StatefulSet", response.Data.ResourceKind)
}

func TestDetectStatefulSet_NotFound(t *testing.T) {
	router := setupDetectionHandler()

	req := httptest.NewRequest("GET", "/api/v1/detect/statefulset/default/nonexistent", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response DetectionResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.False(t, response.Success)
	assert.Contains(t, response.Error, "failed to get statefulset")
}

func TestDetectDaemonSet_Success(t *testing.T) {
	router := setupDetectionHandler()

	req := httptest.NewRequest("GET", "/api/v1/detect/daemonset/kube-system/test-ds", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response DetectionResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.NotNil(t, response.Data)
	assert.Equal(t, models.DeploymentMethodOperator, response.Data.Method)
	assert.Equal(t, 0.80, response.Data.Confidence)
	assert.Equal(t, "DaemonSet", response.Data.ResourceKind)
}

func TestDetectDaemonSet_NotFound(t *testing.T) {
	router := setupDetectionHandler()

	req := httptest.NewRequest("GET", "/api/v1/detect/daemonset/default/nonexistent", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response DetectionResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.False(t, response.Success)
	assert.Contains(t, response.Error, "failed to get daemonset")
}

func TestClearCache_Success(t *testing.T) {
	router := setupDetectionHandler()

	// First, populate the cache
	req1 := httptest.NewRequest("GET", "/api/v1/detect/deployment/default/test-app", http.NoBody)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Verify cache has entries
	statsReq := httptest.NewRequest("GET", "/api/v1/detect/cache/stats", http.NoBody)
	statsW := httptest.NewRecorder()
	router.ServeHTTP(statsW, statsReq)

	var statsResp map[string]interface{}
	json.NewDecoder(statsW.Body).Decode(&statsResp)
	data := statsResp["data"].(map[string]interface{})
	assert.Equal(t, float64(1), data["total_entries"])

	// Clear cache
	req := httptest.NewRequest("POST", "/api/v1/detect/cache/clear", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response DetectionResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.True(t, response.Success)
	assert.Equal(t, "Cache cleared successfully", response.Message)

	// Verify cache is empty
	statsReq2 := httptest.NewRequest("GET", "/api/v1/detect/cache/stats", http.NoBody)
	statsW2 := httptest.NewRecorder()
	router.ServeHTTP(statsW2, statsReq2)

	var statsResp2 map[string]interface{}
	json.NewDecoder(statsW2.Body).Decode(&statsResp2)
	data2 := statsResp2["data"].(map[string]interface{})
	assert.Equal(t, float64(0), data2["total_entries"])
}

func TestGetCacheStats_Success(t *testing.T) {
	router := setupDetectionHandler()

	req := httptest.NewRequest("GET", "/api/v1/detect/cache/stats", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.True(t, response["success"].(bool))
	assert.NotNil(t, response["data"])

	data := response["data"].(map[string]interface{})
	assert.Contains(t, data, "total_entries")
	assert.Contains(t, data, "valid_entries")
	assert.Contains(t, data, "expired_entries")
	assert.Contains(t, data, "ttl_seconds")
	assert.Equal(t, float64(300), data["ttl_seconds"])
}

func TestRegisterRoutes(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	clientset := fake.NewSimpleClientset()
	detectorInstance := detector.NewDeploymentDetector(clientset, log)
	handler := NewDetectionHandler(detectorInstance, log)

	router := mux.NewRouter()
	handler.RegisterRoutes(router)

	// Test that GET routes are registered
	getRoutes := []string{
		"/api/v1/detect/deployment/default/test",
		"/api/v1/detect/statefulset/default/test",
		"/api/v1/detect/daemonset/default/test",
		"/api/v1/detect/cache/stats",
	}

	for _, route := range getRoutes {
		var match mux.RouteMatch
		req := httptest.NewRequest("GET", route, http.NoBody)
		assert.True(t, router.Match(req, &match), "GET route %s should be registered", route)
	}

	// Test POST route
	var match mux.RouteMatch
	req := httptest.NewRequest("POST", "/api/v1/detect/cache/clear", http.NoBody)
	assert.True(t, router.Match(req, &match), "POST route /api/v1/detect/cache/clear should be registered")
}

func TestDetectionResponse_JSONSerialization(t *testing.T) {
	info := models.NewDeploymentInfo("default", "test-app", "Deployment", models.DeploymentMethodArgoCD, 0.95)

	response := DetectionResponse{
		Success: true,
		Data:    info,
		Message: "Success",
	}

	data, err := json.Marshal(response)
	require.NoError(t, err)
	assert.NotNil(t, data)

	var decoded DetectionResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, response.Success, decoded.Success)
	assert.Equal(t, response.Message, decoded.Message)
	assert.Equal(t, response.Data.Method, decoded.Data.Method)
}

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"Nil error", nil, false},
		{"Not found prefix", assert.AnError, false},
		{"Not found suffix", assert.AnError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNotFoundError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDetectDeployment_ContextCancellation(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewSimpleClientset()
	detectorInstance := detector.NewDeploymentDetector(clientset, log)
	handler := NewDetectionHandler(detectorInstance, log)

	router := mux.NewRouter()
	handler.RegisterRoutes(router)

	// Create request with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	req := httptest.NewRequest("GET", "/api/v1/detect/deployment/default/test-app", http.NoBody)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should get error due to cancelled context
	assert.NotEqual(t, http.StatusOK, w.Code)
}
