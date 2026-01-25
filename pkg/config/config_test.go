package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear all relevant environment variables
	clearEnv(t)

	// Set minimum required KServe config for validation to pass
	os.Setenv("KSERVE_ANOMALY_DETECTOR_SERVICE", "anomaly-detector-predictor")
	defer os.Unsetenv("KSERVE_ANOMALY_DETECTOR_SERVICE")

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify defaults
	assert.Equal(t, DefaultPort, cfg.Port)
	assert.Equal(t, DefaultMetricsPort, cfg.MetricsPort)
	assert.Equal(t, DefaultLogLevel, cfg.LogLevel)
	assert.Equal(t, DefaultNamespace, cfg.Namespace)
	assert.Equal(t, DefaultMLServiceURL, cfg.MLServiceURL) // Empty by default
	assert.Equal(t, DefaultHTTPTimeout, cfg.HTTPTimeout)
	assert.Equal(t, float32(DefaultKubernetesQPS), cfg.KubernetesQPS)
	assert.Equal(t, DefaultKubernetesBurst, cfg.KubernetesBurst)
	assert.Equal(t, DefaultEnableCORS, cfg.EnableCORS)
	assert.Equal(t, []string{"*"}, cfg.CORSAllowOrigin)

	// Verify KServe defaults (ADR-039)
	assert.True(t, cfg.KServe.Enabled)
	assert.Equal(t, DefaultKServeNamespace, cfg.KServe.Namespace)
	assert.Equal(t, DefaultKServeTimeout, cfg.KServe.Timeout)
}

func TestLoad_FromEnvironment(t *testing.T) {
	clearEnv(t)

	// Set environment variables
	os.Setenv("PORT", "9000")
	os.Setenv("METRICS_PORT", "9091")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("NAMESPACE", "test-namespace")
	os.Setenv("ARGOCD_API_URL", "https://argocd:8080")
	os.Setenv("HTTP_TIMEOUT", "60s")
	os.Setenv("KUBERNETES_QPS", "100.0")
	os.Setenv("KUBERNETES_BURST", "200")
	os.Setenv("ENABLE_CORS", "true")
	os.Setenv("CORS_ALLOW_ORIGIN", "http://localhost:3000,https://example.com")

	// KServe configuration (ADR-039)
	os.Setenv("ENABLE_KSERVE_INTEGRATION", "true")
	os.Setenv("KSERVE_NAMESPACE", "ml-platform")
	os.Setenv("KSERVE_ANOMALY_DETECTOR_SERVICE", "anomaly-detector-predictor")
	os.Setenv("KSERVE_PREDICTIVE_ANALYTICS_SERVICE", "predictive-analytics-predictor")
	os.Setenv("KSERVE_TIMEOUT", "15s")
	defer clearEnv(t)

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, 9000, cfg.Port)
	assert.Equal(t, 9091, cfg.MetricsPort)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, "test-namespace", cfg.Namespace)
	assert.Equal(t, "https://argocd:8080", cfg.ArgocdAPIURL)
	assert.Equal(t, 60*time.Second, cfg.HTTPTimeout)
	assert.Equal(t, float32(100.0), cfg.KubernetesQPS)
	assert.Equal(t, 200, cfg.KubernetesBurst)
	assert.Equal(t, true, cfg.EnableCORS)
	assert.Equal(t, []string{"http://localhost:3000", "https://example.com"}, cfg.CORSAllowOrigin)

	// Verify KServe configuration (ADR-039)
	assert.True(t, cfg.KServe.Enabled)
	assert.Equal(t, "ml-platform", cfg.KServe.Namespace)
	assert.Equal(t, "anomaly-detector-predictor", cfg.KServe.Services.AnomalyDetector)
	assert.Equal(t, "predictive-analytics-predictor", cfg.KServe.Services.PredictiveAnalytics)
	assert.Equal(t, 15*time.Second, cfg.KServe.Timeout)
}

func TestLoad_FromEnvironment_LegacyML(t *testing.T) {
	clearEnv(t)

	// Set legacy ML_SERVICE_URL with KServe disabled
	os.Setenv("ENABLE_KSERVE_INTEGRATION", "false")
	os.Setenv("ML_SERVICE_URL", "http://test-ml:8080")
	defer clearEnv(t)

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.False(t, cfg.KServe.Enabled)
	assert.Equal(t, "http://test-ml:8080", cfg.MLServiceURL)
	assert.True(t, cfg.UseLegacyML())
	assert.False(t, cfg.UseKServe())
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := &Config{
		Port:            8080,
		MetricsPort:     9090,
		LogLevel:        "info",
		Namespace:       "default",
		HTTPTimeout:     30 * time.Second,
		KubernetesQPS:   50.0,
		KubernetesBurst: 100,
		KServe: KServeConfig{
			Enabled:   true,
			Namespace: "self-healing-platform",
			Services: KServeServices{
				AnomalyDetector: "anomaly-detector-predictor",
			},
			Timeout: 10 * time.Second,
		},
	}

	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestValidate_ValidConfig_LegacyML(t *testing.T) {
	cfg := &Config{
		Port:            8080,
		MetricsPort:     9090,
		LogLevel:        "info",
		Namespace:       "default",
		MLServiceURL:    "http://ml-service:8080",
		HTTPTimeout:     30 * time.Second,
		KubernetesQPS:   50.0,
		KubernetesBurst: 100,
		KServe: KServeConfig{
			Enabled: false, // KServe disabled, using legacy
		},
	}

	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestValidate_InvalidPort(t *testing.T) {
	tests := []struct {
		name        string
		port        int
		metricsPort int
		wantError   bool
	}{
		{"port too low", 0, 9090, true},
		{"port too high", 70000, 9090, true},
		{"metrics port too low", 8080, 0, true},
		{"metrics port too high", 8080, 70000, true},
		{"same ports", 8080, 8080, true},
		{"valid ports", 8080, 9090, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Port:            tt.port,
				MetricsPort:     tt.metricsPort,
				LogLevel:        "info",
				Namespace:       "default",
				HTTPTimeout:     30 * time.Second,
				KubernetesQPS:   50.0,
				KubernetesBurst: 100,
				KServe: KServeConfig{
					Enabled:   true,
					Namespace: "default",
					Services:  KServeServices{AnomalyDetector: "anomaly-detector"},
					Timeout:   10 * time.Second,
				},
			}
			err := cfg.Validate()
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	cfg := &Config{
		Port:            8080,
		MetricsPort:     9090,
		LogLevel:        "invalid",
		Namespace:       "default",
		HTTPTimeout:     30 * time.Second,
		KubernetesQPS:   50.0,
		KubernetesBurst: 100,
		KServe: KServeConfig{
			Enabled:   true,
			Namespace: "default",
			Services:  KServeServices{AnomalyDetector: "anomaly-detector"},
			Timeout:   10 * time.Second,
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid log_level")
}

func TestValidate_EmptyNamespace(t *testing.T) {
	cfg := &Config{
		Port:            8080,
		MetricsPort:     9090,
		LogLevel:        "info",
		Namespace:       "",
		HTTPTimeout:     30 * time.Second,
		KubernetesQPS:   50.0,
		KubernetesBurst: 100,
		KServe: KServeConfig{
			Enabled:   true,
			Namespace: "default",
			Services:  KServeServices{AnomalyDetector: "anomaly-detector"},
			Timeout:   10 * time.Second,
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "namespace cannot be empty")
}

func TestValidate_InvalidMLServiceURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantError bool
	}{
		// With KServe disabled, legacy ML_SERVICE_URL is validated
		{"no protocol", "ml-service:8080", true},
		{"ftp protocol", "ftp://ml-service:8080", true},
		{"http valid", "http://ml-service:8080", false},
		{"https valid", "https://ml-service:8080", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Port:            8080,
				MetricsPort:     9090,
				LogLevel:        "info",
				Namespace:       "default",
				MLServiceURL:    tt.url,
				HTTPTimeout:     30 * time.Second,
				KubernetesQPS:   50.0,
				KubernetesBurst: 100,
				KServe: KServeConfig{
					Enabled: false, // Using legacy ML
				},
			}
			err := cfg.Validate()
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_InvalidArgocdURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantError bool
	}{
		{"empty url (optional)", "", false},
		{"no protocol", "argocd:8080", true},
		{"http valid", "http://argocd:8080", false},
		{"https valid", "https://argocd:8080", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Port:            8080,
				MetricsPort:     9090,
				LogLevel:        "info",
				Namespace:       "default",
				ArgocdAPIURL:    tt.url,
				HTTPTimeout:     30 * time.Second,
				KubernetesQPS:   50.0,
				KubernetesBurst: 100,
				KServe: KServeConfig{
					Enabled:   true,
					Namespace: "default",
					Services:  KServeServices{AnomalyDetector: "anomaly-detector"},
					Timeout:   10 * time.Second,
				},
			}
			err := cfg.Validate()
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_InvalidHTTPTimeout(t *testing.T) {
	tests := []struct {
		name      string
		timeout   time.Duration
		wantError bool
	}{
		{"too short", 500 * time.Millisecond, true},
		{"minimum valid", 1 * time.Second, false},
		{"normal", 30 * time.Second, false},
		{"maximum valid", 5 * time.Minute, false},
		{"too long", 10 * time.Minute, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Port:            8080,
				MetricsPort:     9090,
				LogLevel:        "info",
				Namespace:       "default",
				HTTPTimeout:     tt.timeout,
				KubernetesQPS:   50.0,
				KubernetesBurst: 100,
				KServe: KServeConfig{
					Enabled:   true,
					Namespace: "default",
					Services:  KServeServices{AnomalyDetector: "anomaly-detector"},
					Timeout:   10 * time.Second,
				},
			}
			err := cfg.Validate()
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_InvalidKubernetesSettings(t *testing.T) {
	tests := []struct {
		name      string
		qps       float32
		burst     int
		wantError bool
	}{
		{"negative qps", -1.0, 100, true},
		{"zero qps", 0.0, 100, true},
		{"negative burst", 50.0, -1, true},
		{"zero burst", 50.0, 0, true},
		{"valid settings", 50.0, 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Port:            8080,
				MetricsPort:     9090,
				LogLevel:        "info",
				Namespace:       "default",
				HTTPTimeout:     30 * time.Second,
				KubernetesQPS:   tt.qps,
				KubernetesBurst: tt.burst,
				KServe: KServeConfig{
					Enabled:   true,
					Namespace: "default",
					Services:  KServeServices{AnomalyDetector: "anomaly-detector"},
					Timeout:   10 * time.Second,
				},
			}
			err := cfg.Validate()
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetEnvAsSlice(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected []string
	}{
		{"single value", "http://localhost:3000", []string{"http://localhost:3000"}},
		{"multiple values", "http://localhost:3000,https://example.com", []string{"http://localhost:3000", "https://example.com"}},
		{"with spaces", "http://localhost:3000 , https://example.com", []string{"http://localhost:3000", "https://example.com"}},
		{"empty string", "", []string{"*"}},
		{"only commas", ",,,", []string{"*"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("TEST_SLICE", tt.envValue)
			defer os.Unsetenv("TEST_SLICE")

			result := getEnvAsSlice("TEST_SLICE", []string{"*"})
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEnvAsInt(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected int
	}{
		{"valid int", "9000", 9000},
		{"invalid int", "abc", 8080},
		{"empty string", "", 8080},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv("TEST_INT", tt.envValue)
				defer os.Unsetenv("TEST_INT")
			}

			result := getEnvAsInt("TEST_INT", 8080)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEnvAsBool(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{"true lowercase", "true", true},
		{"true uppercase", "TRUE", true},
		{"true number", "1", true},
		{"false lowercase", "false", false},
		{"false uppercase", "FALSE", false},
		{"false number", "0", false},
		{"invalid", "abc", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv("TEST_BOOL", tt.envValue)
				defer os.Unsetenv("TEST_BOOL")
			}

			result := getEnvAsBool("TEST_BOOL", false)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEnvAsDuration(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected time.Duration
	}{
		{"seconds", "30s", 30 * time.Second},
		{"minutes", "2m", 2 * time.Minute},
		{"complex", "1m30s", 90 * time.Second},
		{"invalid", "abc", 30 * time.Second},
		{"empty", "", 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv("TEST_DURATION", tt.envValue)
				defer os.Unsetenv("TEST_DURATION")
			}

			result := getEnvAsDuration("TEST_DURATION", 30*time.Second)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// clearEnv removes all environment variables used by the config package
func clearEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"PORT", "METRICS_PORT", "LOG_LEVEL", "KUBECONFIG", "NAMESPACE",
		"ML_SERVICE_URL", "ARGOCD_API_URL", "HTTP_TIMEOUT",
		"ENABLE_CORS", "CORS_ALLOW_ORIGIN",
		"KUBERNETES_QPS", "KUBERNETES_BURST",
		// KServe environment variables (ADR-039)
		"ENABLE_KSERVE_INTEGRATION", "KSERVE_NAMESPACE", "KSERVE_PREDICTOR_PORT",
		"KSERVE_ANOMALY_DETECTOR_SERVICE", "KSERVE_PREDICTIVE_ANALYTICS_SERVICE",
		"KSERVE_TIMEOUT",
	}
	for _, key := range envVars {
		os.Unsetenv(key)
	}
}

// TestKServeConfig_Validation tests KServe-specific validation (ADR-039)
func TestKServeConfig_Validation(t *testing.T) {
	tests := []struct {
		name      string
		kserve    KServeConfig
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid kserve config with anomaly detector",
			kserve: KServeConfig{
				Enabled:   true,
				Namespace: "self-healing-platform",
				Services:  KServeServices{AnomalyDetector: "anomaly-detector-predictor"},
				Timeout:   10 * time.Second,
			},
			wantError: false,
		},
		{
			name: "valid kserve config with predictive analytics",
			kserve: KServeConfig{
				Enabled:   true,
				Namespace: "ml-platform",
				Services:  KServeServices{PredictiveAnalytics: "predictive-analytics-predictor"},
				Timeout:   15 * time.Second,
			},
			wantError: false,
		},
		{
			name: "valid kserve config with both services",
			kserve: KServeConfig{
				Enabled:   true,
				Namespace: "ml-platform",
				Services: KServeServices{
					AnomalyDetector:     "anomaly-detector-predictor",
					PredictiveAnalytics: "predictive-analytics-predictor",
				},
				Timeout: 10 * time.Second,
			},
			wantError: false,
		},
		{
			name: "kserve disabled - no validation",
			kserve: KServeConfig{
				Enabled: false,
			},
			wantError: false,
		},
		{
			name: "empty namespace with kserve enabled",
			kserve: KServeConfig{
				Enabled:   true,
				Namespace: "",
				Services:  KServeServices{AnomalyDetector: "anomaly-detector"},
				Timeout:   10 * time.Second,
			},
			wantError: true,
			errorMsg:  "kserve.namespace cannot be empty",
		},
		{
			name: "no services configured",
			kserve: KServeConfig{
				Enabled:   true,
				Namespace: "default",
				Services:  KServeServices{},
				Timeout:   10 * time.Second,
			},
			wantError: true,
			errorMsg:  "at least one KServe service must be configured",
		},
		{
			name: "timeout too short",
			kserve: KServeConfig{
				Enabled:   true,
				Namespace: "default",
				Services:  KServeServices{AnomalyDetector: "anomaly-detector"},
				Timeout:   500 * time.Millisecond,
			},
			wantError: true,
			errorMsg:  "kserve.timeout too short",
		},
		{
			name: "timeout too long",
			kserve: KServeConfig{
				Enabled:   true,
				Namespace: "default",
				Services:  KServeServices{AnomalyDetector: "anomaly-detector"},
				Timeout:   5 * time.Minute,
			},
			wantError: true,
			errorMsg:  "kserve.timeout too long",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Port:            8080,
				MetricsPort:     9090,
				LogLevel:        "info",
				Namespace:       "default",
				HTTPTimeout:     30 * time.Second,
				KubernetesQPS:   50.0,
				KubernetesBurst: 100,
				KServe:          tt.kserve,
			}
			err := cfg.Validate()
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestKServeConfig_GetURLs(t *testing.T) {
	kserve := KServeConfig{
		Enabled:       true,
		Namespace:     "self-healing-platform",
		PredictorPort: 8080,
		Services: KServeServices{
			AnomalyDetector:     "anomaly-detector-predictor",
			PredictiveAnalytics: "predictive-analytics-predictor",
		},
	}

	anomalyURL := kserve.GetAnomalyDetectorURL()
	assert.Equal(t, "http://anomaly-detector-predictor.self-healing-platform.svc.cluster.local:8080", anomalyURL)

	predictiveURL := kserve.GetPredictiveAnalyticsURL()
	assert.Equal(t, "http://predictive-analytics-predictor.self-healing-platform.svc.cluster.local:8080", predictiveURL)
}

func TestKServeConfig_GetURLs_DefaultPort(t *testing.T) {
	// Test that default port is used when PredictorPort is 0
	kserve := KServeConfig{
		Enabled:       true,
		Namespace:     "test-ns",
		PredictorPort: 0, // Should default to 8080
		Services: KServeServices{
			AnomalyDetector: "anomaly-detector-predictor",
		},
	}

	anomalyURL := kserve.GetAnomalyDetectorURL()
	assert.Equal(t, "http://anomaly-detector-predictor.test-ns.svc.cluster.local:8080", anomalyURL)
}

func TestKServeConfig_GetURLs_CustomPort(t *testing.T) {
	// Test that custom port is used when explicitly set
	kserve := KServeConfig{
		Enabled:       true,
		Namespace:     "test-ns",
		PredictorPort: 9000,
		Services: KServeServices{
			AnomalyDetector: "anomaly-detector-predictor",
		},
	}

	anomalyURL := kserve.GetAnomalyDetectorURL()
	assert.Equal(t, "http://anomaly-detector-predictor.test-ns.svc.cluster.local:9000", anomalyURL)
}

func TestKServeConfig_GetURLs_Empty(t *testing.T) {
	kserve := KServeConfig{
		Enabled:   true,
		Namespace: "default",
		Services:  KServeServices{},
	}

	assert.Equal(t, "", kserve.GetAnomalyDetectorURL())
	assert.Equal(t, "", kserve.GetPredictiveAnalyticsURL())
}

func TestConfig_UseKServe(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		expected bool
	}{
		{
			name: "kserve enabled with service",
			cfg: Config{
				KServe: KServeConfig{
					Enabled:  true,
					Services: KServeServices{AnomalyDetector: "anomaly-detector"},
				},
			},
			expected: true,
		},
		{
			name: "kserve disabled",
			cfg: Config{
				KServe: KServeConfig{
					Enabled:  false,
					Services: KServeServices{AnomalyDetector: "anomaly-detector"},
				},
			},
			expected: false,
		},
		{
			name: "kserve enabled but no services",
			cfg: Config{
				KServe: KServeConfig{
					Enabled:  true,
					Services: KServeServices{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.cfg.UseKServe())
		})
	}
}

func TestConfig_UseLegacyML(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		expected bool
	}{
		{
			name: "legacy ml with url",
			cfg: Config{
				KServe:       KServeConfig{Enabled: false},
				MLServiceURL: "http://ml-service:8080",
			},
			expected: true,
		},
		{
			name: "kserve enabled (legacy disabled)",
			cfg: Config{
				KServe:       KServeConfig{Enabled: true},
				MLServiceURL: "http://ml-service:8080",
			},
			expected: false,
		},
		{
			name: "no ml service url",
			cfg: Config{
				KServe:       KServeConfig{Enabled: false},
				MLServiceURL: "",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.cfg.UseLegacyML())
		})
	}
}

func TestConfig_HasMLIntegration(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		expected bool
	}{
		{
			name: "has kserve",
			cfg: Config{
				KServe: KServeConfig{
					Enabled:  true,
					Services: KServeServices{AnomalyDetector: "anomaly-detector"},
				},
			},
			expected: true,
		},
		{
			name: "has legacy ml",
			cfg: Config{
				KServe:       KServeConfig{Enabled: false},
				MLServiceURL: "http://ml-service:8080",
			},
			expected: true,
		},
		{
			name: "no ml integration",
			cfg: Config{
				KServe:       KServeConfig{Enabled: false},
				MLServiceURL: "",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.cfg.HasMLIntegration())
		})
	}
}

// TestDiscoverKServeServicesFromEnv tests dynamic service discovery (ADR-040)
func TestDiscoverKServeServicesFromEnv(t *testing.T) {
	clearEnv(t)

	// Set up environment variables for dynamic discovery
	os.Setenv("KSERVE_DISK_FAILURE_PREDICTOR_SERVICE", "disk-failure-predictor-predictor")
	os.Setenv("KSERVE_NETWORK_ANOMALY_SERVICE", "network-anomaly-predictor")
	os.Setenv("KSERVE_CUSTOM_MODEL_SERVICE", "custom-model-predictor")
	// These should be ignored (already handled by legacy config)
	os.Setenv("KSERVE_ANOMALY_DETECTOR_SERVICE", "anomaly-detector-predictor")
	os.Setenv("KSERVE_PREDICTIVE_ANALYTICS_SERVICE", "predictive-analytics-predictor")
	// These should be ignored (configuration variables)
	os.Setenv("KSERVE_NAMESPACE", "self-healing-platform")
	os.Setenv("KSERVE_TIMEOUT", "10s")
	defer func() {
		os.Unsetenv("KSERVE_DISK_FAILURE_PREDICTOR_SERVICE")
		os.Unsetenv("KSERVE_NETWORK_ANOMALY_SERVICE")
		os.Unsetenv("KSERVE_CUSTOM_MODEL_SERVICE")
		os.Unsetenv("KSERVE_ANOMALY_DETECTOR_SERVICE")
		os.Unsetenv("KSERVE_PREDICTIVE_ANALYTICS_SERVICE")
		os.Unsetenv("KSERVE_NAMESPACE")
		os.Unsetenv("KSERVE_TIMEOUT")
	}()

	services := discoverKServeServicesFromEnv()

	// Should have 3 dynamic services (legacy ones are filtered out)
	assert.Len(t, services, 3)
	assert.Equal(t, "disk-failure-predictor-predictor", services["disk-failure-predictor"])
	assert.Equal(t, "network-anomaly-predictor", services["network-anomaly"])
	assert.Equal(t, "custom-model-predictor", services["custom-model"])

	// Legacy services should not be in dynamic services
	_, hasLegacyAnomaly := services["anomaly-detector"]
	assert.False(t, hasLegacyAnomaly, "anomaly-detector should not be in dynamic services")
	_, hasLegacyPredictive := services["predictive-analytics"]
	assert.False(t, hasLegacyPredictive, "predictive-analytics should not be in dynamic services")
}

func TestKServeConfig_GetAllServices(t *testing.T) {
	kserve := KServeConfig{
		Enabled:   true,
		Namespace: "self-healing-platform",
		Services: KServeServices{
			AnomalyDetector:     "anomaly-detector-predictor",
			PredictiveAnalytics: "predictive-analytics-predictor",
		},
		DynamicServices: map[string]string{
			"disk-failure-predictor": "disk-failure-predictor-predictor",
			"network-anomaly":        "network-anomaly-predictor",
		},
	}

	services := kserve.GetAllServices()

	assert.Len(t, services, 4)
	assert.Equal(t, "anomaly-detector-predictor", services["anomaly-detector"])
	assert.Equal(t, "predictive-analytics-predictor", services["predictive-analytics"])
	assert.Equal(t, "disk-failure-predictor-predictor", services["disk-failure-predictor"])
	assert.Equal(t, "network-anomaly-predictor", services["network-anomaly"])
}

func TestKServeConfig_HasServices(t *testing.T) {
	tests := []struct {
		name     string
		kserve   KServeConfig
		expected bool
	}{
		{
			name: "has legacy services",
			kserve: KServeConfig{
				Services: KServeServices{AnomalyDetector: "anomaly-detector"},
			},
			expected: true,
		},
		{
			name: "has dynamic services",
			kserve: KServeConfig{
				DynamicServices: map[string]string{"custom-model": "custom-model-predictor"},
			},
			expected: true,
		},
		{
			name: "has both",
			kserve: KServeConfig{
				Services:        KServeServices{AnomalyDetector: "anomaly-detector"},
				DynamicServices: map[string]string{"custom-model": "custom-model-predictor"},
			},
			expected: true,
		},
		{
			name:     "no services",
			kserve:   KServeConfig{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.kserve.HasServices())
		})
	}
}

func TestKServeConfig_ServiceCount(t *testing.T) {
	tests := []struct {
		name     string
		kserve   KServeConfig
		expected int
	}{
		{
			name: "legacy only",
			kserve: KServeConfig{
				Services: KServeServices{
					AnomalyDetector:     "anomaly-detector",
					PredictiveAnalytics: "predictive-analytics",
				},
			},
			expected: 2,
		},
		{
			name: "dynamic only",
			kserve: KServeConfig{
				DynamicServices: map[string]string{
					"model-a": "service-a",
					"model-b": "service-b",
					"model-c": "service-c",
				},
			},
			expected: 3,
		},
		{
			name: "both",
			kserve: KServeConfig{
				Services:        KServeServices{AnomalyDetector: "anomaly-detector"},
				DynamicServices: map[string]string{"custom": "custom-svc"},
			},
			expected: 2,
		},
		{
			name:     "none",
			kserve:   KServeConfig{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.kserve.ServiceCount())
		})
	}
}

func TestLoad_WithDynamicKServeServices(t *testing.T) {
	clearEnv(t)

	// Set up a custom model via environment variable
	os.Setenv("KSERVE_CUSTOM_MODEL_SERVICE", "custom-model-predictor")
	defer os.Unsetenv("KSERVE_CUSTOM_MODEL_SERVICE")

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Should have the custom model in dynamic services
	assert.NotNil(t, cfg.KServe.DynamicServices)
	assert.Equal(t, "custom-model-predictor", cfg.KServe.DynamicServices["custom-model"])

	// Should be included in GetAllServices
	allServices := cfg.KServe.GetAllServices()
	assert.Contains(t, allServices, "custom-model")
}

func TestValidate_WithDynamicServicesOnly(t *testing.T) {
	// Test that validation passes with only dynamic services (no legacy services)
	cfg := &Config{
		Port:            8080,
		MetricsPort:     9090,
		LogLevel:        "info",
		Namespace:       "default",
		HTTPTimeout:     30 * time.Second,
		KubernetesQPS:   50.0,
		KubernetesBurst: 100,
		KServe: KServeConfig{
			Enabled:         true,
			Namespace:       "self-healing-platform",
			Services:        KServeServices{}, // No legacy services
			DynamicServices: map[string]string{"custom-model": "custom-model-predictor"},
			Timeout:         10 * time.Second,
		},
	}

	err := cfg.Validate()
	assert.NoError(t, err)
}
