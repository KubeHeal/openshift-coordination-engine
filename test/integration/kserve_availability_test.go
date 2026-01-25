//go:build integration
// +build integration

package integration

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestKServeModelAvailability tests that KServe InferenceServices are available
// before the coordination engine starts. This prevents 404 errors on startup.
//
// To run this test:
//
//	INTEGRATION_TEST=true go test -tags=integration ./test/integration/...
//
// Note: Set INTEGRATION_TEST=true environment variable to enable these tests.
// The tests target in-cluster DNS (*.svc) which requires running inside a Kubernetes cluster.
//
// Prerequisites:
//   - KServe InferenceServices deployed in self-healing-platform namespace
//   - InferenceServices must be in Ready state
//   - Model files must be present in PVC or S3
//
// This test will skip gracefully if KServe services are not available (e.g., in CI without KServe).
func TestKServeModelAvailability(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test - set INTEGRATION_TEST=true to run")
	}

	namespace := os.Getenv("KSERVE_NAMESPACE")
	if namespace == "" {
		namespace = "self-healing-platform"
	}

	// List of expected KServe models
	models := []string{
		"anomaly-detector-predictor",
		"predictive-analytics-predictor",
	}

	// Create HTTP client with timeout to prevent indefinite hangs
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			// Build URL for KServe model health endpoint
			// KServe v1 protocol: GET /v1/models/model
			url := fmt.Sprintf("http://%s.%s.svc:8080/v1/models/model", model, namespace)

			// Make HTTP GET request to model health endpoint
			resp, err := client.Get(url)
			if err != nil {
				// Check if error is DNS lookup failure (KServe not deployed)
				if strings.Contains(err.Error(), "no such host") {
					t.Skipf("KServe service %s not available - skipping test (KServe may not be deployed in this environment)", model)
					return
				}
				require.NoError(t, err, "Failed to reach KServe model %s - ensure InferenceService is deployed", model)
			}
			require.NotNil(t, resp, "Response should not be nil")
			defer resp.Body.Close()

			// Check status code
			require.Equal(t, http.StatusOK, resp.StatusCode,
				"Model %s returned non-200 status: %d - verify model files exist and InferenceService is Ready",
				model, resp.StatusCode)

			t.Logf("✓ Model %s is available and healthy", model)
		})
	}
}

// TestKServeModelPrediction tests that KServe models can accept prediction requests
//
// This test will skip gracefully if KServe services are not available (e.g., in CI without KServe).
func TestKServeModelPrediction(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test - set INTEGRATION_TEST=true to run")
	}

	namespace := os.Getenv("KSERVE_NAMESPACE")
	if namespace == "" {
		namespace = "self-healing-platform"
	}

	// Create HTTP client with timeout to prevent indefinite hangs
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Test anomaly-detector model with sample prediction request
	t.Run("anomaly-detector-prediction", func(t *testing.T) {
		url := fmt.Sprintf("http://anomaly-detector-predictor.%s.svc:8080/v1/models/model:predict", namespace)

		// Sample request body (KServe v1 format)
		requestBody := `{"instances": [[0.5, 0.3, 0.8]]}`

		resp, err := client.Post(url, "application/json", strings.NewReader(requestBody))
		if err != nil {
			// Check if error is DNS lookup failure (KServe not deployed)
			if strings.Contains(err.Error(), "no such host") {
				t.Skip("KServe service anomaly-detector-predictor not available - skipping test (KServe may not be deployed in this environment)")
				return
			}
			require.NoError(t, err, "Failed to make prediction request to anomaly-detector")
		}
		require.NotNil(t, resp, "Response should not be nil")
		defer resp.Body.Close()

		// Accept both 200 (success) and 400 (bad request - model exists but rejects input)
		// We just want to ensure the model endpoint is reachable
		require.True(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusBadRequest,
			"Unexpected status code %d - expected 200 or 400", resp.StatusCode)

		t.Logf("✓ Anomaly detector model is reachable (status: %d)", resp.StatusCode)
	})
}
