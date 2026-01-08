package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tosin2013/openshift-coordination-engine/pkg/kserve"
)

func TestKServeProxyHandler_HandleDetect(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create mock KServe server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models/test-model:predict" && r.Method == "POST" {
			resp := map[string]interface{}{
				"predictions":   []int{-1, 1},
				"model_name":    "test-model",
				"model_version": "v1",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	// Create proxy client with mock server URL
	cfg := kserve.ProxyConfig{
		Namespace: "test-ns",
		Timeout:   30 * time.Second,
	}

	client, err := kserve.NewProxyClient(cfg, log)
	require.NoError(t, err)

	// We need to inject the model - use a workaround by creating a fresh client
	// and leveraging the test server URL through model info
	handler := NewKServeProxyHandler(client, log)

	// Test missing model field
	t.Run("missing model field", func(t *testing.T) {
		reqBody := `{"instances": [[0.5, 1.2]]}`
		req := httptest.NewRequest("POST", "/api/v1/detect", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandleDetect(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp ErrorResponse
		json.NewDecoder(w.Body).Decode(&resp)
		assert.Contains(t, resp.Error, "Missing 'model' field")
	})

	// Test missing instances field
	t.Run("missing instances field", func(t *testing.T) {
		reqBody := `{"model": "test-model"}`
		req := httptest.NewRequest("POST", "/api/v1/detect", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandleDetect(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp ErrorResponse
		json.NewDecoder(w.Body).Decode(&resp)
		assert.Contains(t, resp.Error, "Missing 'instances' field")
	})

	// Test invalid JSON
	t.Run("invalid JSON", func(t *testing.T) {
		reqBody := `{"model": invalid}`
		req := httptest.NewRequest("POST", "/api/v1/detect", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandleDetect(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	// Test model not found
	t.Run("model not found", func(t *testing.T) {
		reqBody := `{"model": "non-existent", "instances": [[0.5, 1.2]]}`
		req := httptest.NewRequest("POST", "/api/v1/detect", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandleDetect(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestKServeProxyHandler_ListModels(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Set up environment for models
	os.Setenv("KSERVE_MODEL_A_SERVICE", "service-a")
	os.Setenv("KSERVE_MODEL_B_SERVICE", "service-b")
	defer func() {
		os.Unsetenv("KSERVE_MODEL_A_SERVICE")
		os.Unsetenv("KSERVE_MODEL_B_SERVICE")
	}()

	cfg := kserve.ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := kserve.NewProxyClient(cfg, log)
	require.NoError(t, err)

	handler := NewKServeProxyHandler(client, log)

	req := httptest.NewRequest("GET", "/api/v1/models", http.NoBody)
	w := httptest.NewRecorder()

	handler.ListModels(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ModelsListResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)

	assert.Equal(t, 2, resp.Count)
	assert.Contains(t, resp.Models, "model-a")
	assert.Contains(t, resp.Models, "model-b")
}

func TestKServeProxyHandler_CheckModelHealth(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create mock server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models/healthy-model":
			w.WriteHeader(http.StatusOK)
		case "/v1/models/unhealthy-model":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	cfg := kserve.ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := kserve.NewProxyClient(cfg, log)
	require.NoError(t, err)

	handler := NewKServeProxyHandler(client, log)

	// Test model not found
	t.Run("model not found", func(t *testing.T) {
		router := mux.NewRouter()
		router.HandleFunc("/api/v1/models/{model}/health", handler.CheckModelHealth)

		req := httptest.NewRequest("GET", "/api/v1/models/non-existent/health", http.NoBody)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	// Test missing model parameter
	t.Run("missing model parameter", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/models//health", http.NoBody)
		w := httptest.NewRecorder()

		// Direct call without mux vars
		handler.CheckModelHealth(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestKServeProxyHandler_RegisterRoutes(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := kserve.ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := kserve.NewProxyClient(cfg, log)
	require.NoError(t, err)

	handler := NewKServeProxyHandler(client, log)

	router := mux.NewRouter()
	handler.RegisterRoutes(router)

	// Verify routes are registered
	routes := []struct {
		path   string
		method string
	}{
		{"/api/v1/detect", "POST"},
		{"/api/v1/models", "GET"},
		{"/api/v1/models/{model}/health", "GET"},
	}

	for _, route := range routes {
		t.Run(route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, http.NoBody)
			match := &mux.RouteMatch{}
			matched := router.Match(req, match)
			assert.True(t, matched, "Route %s %s should be registered", route.method, route.path)
		})
	}
}

func TestErrorResponse_JSON(t *testing.T) {
	resp := ErrorResponse{
		Error:   "test error",
		Success: false,
	}

	jsonData, err := json.Marshal(resp)
	require.NoError(t, err)

	expected := `{"error":"test error","success":false}`
	assert.JSONEq(t, expected, string(jsonData))
}

func TestModelsListResponse_JSON(t *testing.T) {
	resp := ModelsListResponse{
		Models: []string{"model-a", "model-b"},
		Count:  2,
	}

	jsonData, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded ModelsListResponse
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 2, decoded.Count)
	assert.Len(t, decoded.Models, 2)
}
