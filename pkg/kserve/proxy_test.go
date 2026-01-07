package kserve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProxyClient(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	tests := []struct {
		name        string
		cfg         ProxyConfig
		setupEnv    map[string]string
		expectError bool
		expectCount int
	}{
		{
			name: "valid config with models",
			cfg: ProxyConfig{
				Namespace: "test-namespace",
				Timeout:   10 * time.Second,
			},
			setupEnv: map[string]string{
				"KSERVE_ANOMALY_DETECTOR_SERVICE": "anomaly-detector-predictor",
			},
			expectError: false,
			expectCount: 1,
		},
		{
			name: "valid config without models",
			cfg: ProxyConfig{
				Namespace: "test-namespace",
			},
			setupEnv:    map[string]string{},
			expectError: false,
			expectCount: 0,
		},
		{
			name: "missing namespace",
			cfg: ProxyConfig{
				Namespace: "",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear and set environment variables
			for _, env := range os.Environ() {
				if len(env) > 7 && env[:7] == "KSERVE_" {
					key := env[:len(env)-len(env[len("KSERVE_"):])-1]
					if idx := len("KSERVE_"); idx < len(env) {
						for i := idx; i < len(env); i++ {
							if env[i] == '=' {
								key = env[:i]
								break
							}
						}
					}
					os.Unsetenv(key)
				}
			}

			for key, val := range tt.setupEnv {
				os.Setenv(key, val)
			}
			defer func() {
				for key := range tt.setupEnv {
					os.Unsetenv(key)
				}
			}()

			client, err := NewProxyClient(tt.cfg, log)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, client)
			assert.Equal(t, tt.expectCount, client.ModelCount())
		})
	}
}

func TestProxyClient_LoadModelsFromEnv(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Set up environment variables
	envVars := map[string]string{
		"KSERVE_ANOMALY_DETECTOR_SERVICE":     "anomaly-detector-predictor",
		"KSERVE_PREDICTIVE_ANALYTICS_SERVICE": "predictive-analytics-predictor",
		"KSERVE_DISK_FAILURE_PREDICTOR_SERVICE": "disk-failure-predictor-predictor",
		"KSERVE_NAMESPACE":                    "should-be-ignored", // Configuration variable
		"OTHER_ENV_VAR":                       "should-be-ignored",
		"KSERVE_EMPTY_SERVICE":                "", // Empty value should be skipped
	}

	for key, val := range envVars {
		os.Setenv(key, val)
	}
	defer func() {
		for key := range envVars {
			os.Unsetenv(key)
		}
	}()

	cfg := ProxyConfig{
		Namespace: "test-namespace",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	// Check expected models
	models := client.ListModels()
	assert.Len(t, models, 3)
	assert.Contains(t, models, "anomaly-detector")
	assert.Contains(t, models, "predictive-analytics")
	assert.Contains(t, models, "disk-failure-predictor")

	// Check model info
	anomalyDetector, exists := client.GetModel("anomaly-detector")
	assert.True(t, exists)
	assert.Equal(t, "anomaly-detector", anomalyDetector.Name)
	assert.Equal(t, "anomaly-detector-predictor", anomalyDetector.ServiceName)
	assert.Equal(t, "test-namespace", anomalyDetector.Namespace)
	assert.Equal(t, "http://anomaly-detector-predictor.test-namespace.svc.cluster.local", anomalyDetector.URL)
}

func TestProxyClient_GetModel(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	os.Setenv("KSERVE_TEST_MODEL_SERVICE", "test-service")
	defer os.Unsetenv("KSERVE_TEST_MODEL_SERVICE")

	cfg := ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	// Test existing model
	model, exists := client.GetModel("test-model")
	assert.True(t, exists)
	assert.Equal(t, "test-model", model.Name)
	assert.Equal(t, "test-service", model.ServiceName)

	// Test non-existent model
	_, exists = client.GetModel("non-existent")
	assert.False(t, exists)
}

func TestProxyClient_GetAllModels(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	os.Setenv("KSERVE_MODEL_ONE_SERVICE", "service-one")
	os.Setenv("KSERVE_MODEL_TWO_SERVICE", "service-two")
	defer func() {
		os.Unsetenv("KSERVE_MODEL_ONE_SERVICE")
		os.Unsetenv("KSERVE_MODEL_TWO_SERVICE")
	}()

	cfg := ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	models := client.GetAllModels()
	assert.Len(t, models, 2)
}

func TestProxyClient_Predict(t *testing.T) {
	// Create mock KServe server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models/test-model:predict", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Decode request
		var req map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&req)
		assert.NoError(t, err)
		assert.Contains(t, req, "instances")

		// Send KServe v1 response
		resp := map[string]interface{}{
			"predictions":   []int{-1, 1, -1},
			"model_name":    "test-model",
			"model_version": "v1",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create client with mock server
	cfg := ProxyConfig{
		Namespace: "test-ns",
		Timeout:   30 * time.Second,
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	// Manually add a model pointing to the test server
	client.models["test-model"] = &ModelInfo{
		Name:        "test-model",
		ServiceName: "test-service",
		Namespace:   "test-ns",
		URL:         server.URL,
	}

	// Make prediction
	instances := [][]float64{
		{0.5, 1.2, 0.8},
		{0.3, 0.9, 1.1},
		{2.5, 3.0, 4.0},
	}

	result, err := client.Predict(context.Background(), "test-model", instances)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Predictions, 3)
	assert.Equal(t, []int{-1, 1, -1}, result.Predictions)
	assert.Equal(t, "test-model", result.ModelName)
	assert.Equal(t, "v1", result.ModelVersion)
}

func TestProxyClient_Predict_ModelNotFound(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	_, err = client.Predict(context.Background(), "non-existent", [][]float64{{0.1, 0.2}})

	assert.Error(t, err)
	var notFoundErr *ModelNotFoundError
	assert.ErrorAs(t, err, &notFoundErr)
	assert.Equal(t, "non-existent", notFoundErr.ModelName)
}

func TestProxyClient_Predict_ServerError(t *testing.T) {
	// Create mock server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal server error"))
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	client.models["test-model"] = &ModelInfo{
		Name:        "test-model",
		ServiceName: "test-service",
		Namespace:   "test-ns",
		URL:         server.URL,
	}

	_, err = client.Predict(context.Background(), "test-model", [][]float64{{0.1, 0.2}})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestProxyClient_CheckModelHealth(t *testing.T) {
	// Create healthy mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models/test-model", r.URL.Path)
		assert.Equal(t, "GET", r.Method)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"name": "test-model"})
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	client.models["test-model"] = &ModelInfo{
		Name:        "test-model",
		ServiceName: "test-service",
		Namespace:   "test-ns",
		URL:         server.URL,
	}

	health, err := client.CheckModelHealth(context.Background(), "test-model")

	require.NoError(t, err)
	assert.Equal(t, "test-model", health.Model)
	assert.Equal(t, "ready", health.Status)
	assert.Equal(t, "test-service", health.Service)
	assert.Equal(t, "test-ns", health.Namespace)
}

func TestProxyClient_CheckModelHealth_Unhealthy(t *testing.T) {
	// Create unhealthy mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	client.models["test-model"] = &ModelInfo{
		Name:        "test-model",
		ServiceName: "test-service",
		Namespace:   "test-ns",
		URL:         server.URL,
	}

	health, err := client.CheckModelHealth(context.Background(), "test-model")

	require.NoError(t, err)
	assert.Equal(t, "unavailable", health.Status)
	assert.Contains(t, health.Message, "status 503")
}

func TestProxyClient_CheckModelHealth_NotFound(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	health, err := client.CheckModelHealth(context.Background(), "non-existent")

	assert.Error(t, err)
	var notFoundErr *ModelNotFoundError
	assert.ErrorAs(t, err, &notFoundErr)
	assert.Equal(t, "unknown", health.Status)
	assert.Equal(t, "Model not registered", health.Message)
}

func TestProxyClient_HealthCheck(t *testing.T) {
	// Create healthy mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	client.models["model-1"] = &ModelInfo{
		Name: "model-1",
		URL:  server.URL,
	}
	client.models["model-2"] = &ModelInfo{
		Name: "model-2",
		URL:  server.URL,
	}

	err = client.HealthCheck(context.Background())
	assert.NoError(t, err)
}

func TestProxyClient_HealthCheck_NoModels(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	err = client.HealthCheck(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no models registered")
}

func TestProxyClient_RefreshModels(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	os.Setenv("KSERVE_INITIAL_SERVICE", "initial-service")
	defer os.Unsetenv("KSERVE_INITIAL_SERVICE")

	cfg := ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	assert.Equal(t, 1, client.ModelCount())
	assert.Contains(t, client.ListModels(), "initial")

	// Add new env var and refresh
	os.Setenv("KSERVE_NEW_MODEL_SERVICE", "new-service")
	defer os.Unsetenv("KSERVE_NEW_MODEL_SERVICE")

	client.RefreshModels()

	assert.Equal(t, 2, client.ModelCount())
	assert.Contains(t, client.ListModels(), "new-model")
}

func TestProxyClient_Close(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := ProxyConfig{
		Namespace: "test-ns",
	}

	client, err := NewProxyClient(cfg, log)
	require.NoError(t, err)

	// Close should not panic
	assert.NotPanics(t, func() {
		client.Close()
	})
}

func TestModelNotFoundError(t *testing.T) {
	err := &ModelNotFoundError{ModelName: "test-model"}
	assert.Equal(t, "model not found: test-model", err.Error())
}

func TestModelUnavailableError(t *testing.T) {
	cause := assert.AnError
	err := &ModelUnavailableError{ModelName: "test-model", Cause: cause}
	assert.Contains(t, err.Error(), "model unavailable: test-model")
	assert.Contains(t, err.Error(), cause.Error())
	assert.Equal(t, cause, err.Unwrap())

	// Test without cause
	errNoCause := &ModelUnavailableError{ModelName: "test-model"}
	assert.Equal(t, "model unavailable: test-model", errNoCause.Error())
}

func TestDetectRequest_JSON(t *testing.T) {
	req := &DetectRequest{
		Model: "anomaly-detector",
		Instances: [][]float64{
			{0.5, 1.2, 0.8},
			{0.3, 0.9, 1.1},
		},
	}

	jsonData, err := json.Marshal(req)
	require.NoError(t, err)

	expected := `{"model":"anomaly-detector","instances":[[0.5,1.2,0.8],[0.3,0.9,1.1]]}`
	assert.JSONEq(t, expected, string(jsonData))
}

func TestDetectResponse_JSON(t *testing.T) {
	jsonData := `{
		"predictions": [-1, 1],
		"model_name": "anomaly-detector",
		"model_version": "v2"
	}`

	var resp DetectResponse
	err := json.Unmarshal([]byte(jsonData), &resp)
	require.NoError(t, err)

	assert.Equal(t, []int{-1, 1}, resp.Predictions)
	assert.Equal(t, "anomaly-detector", resp.ModelName)
	assert.Equal(t, "v2", resp.ModelVersion)
}

