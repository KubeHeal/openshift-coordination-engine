// Package config provides configuration management for the coordination engine.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration
type Config struct {
	// Server configuration
	Port        int    `json:"port"`
	MetricsPort int    `json:"metrics_port"`
	LogLevel    string `json:"log_level"`

	// Kubernetes configuration
	Kubeconfig string `json:"kubeconfig,omitempty"`
	Namespace  string `json:"namespace"`

	// External service URLs (deprecated: use KServe configuration instead)
	MLServiceURL string `json:"ml_service_url,omitempty"` // Deprecated: use KServe integration
	ArgocdAPIURL string `json:"argocd_api_url,omitempty"` // Optional, auto-detected

	// Prometheus configuration for metrics querying
	PrometheusURL string `json:"prometheus_url,omitempty"` // URL for Prometheus API queries

	// KServe Integration (ADR-039)
	KServe KServeConfig `json:"kserve"`

	// HTTP client configuration
	HTTPTimeout time.Duration `json:"http_timeout"`

	// Feature flags
	EnableCORS      bool     `json:"enable_cors"`
	CORSAllowOrigin []string `json:"cors_allow_origin,omitempty"`

	// Performance tuning
	KubernetesQPS   float32 `json:"kubernetes_qps"`
	KubernetesBurst int     `json:"kubernetes_burst"`
}

// KServeConfig holds configuration for KServe integration (ADR-039, ADR-040)
type KServeConfig struct {
	// Enabled enables KServe integration (replaces ML_SERVICE_URL)
	Enabled bool `json:"enabled"`

	// Namespace where KServe InferenceServices are deployed
	Namespace string `json:"namespace"`

	// PredictorPort is the port where KServe InferenceService predictors listen
	// In RawDeployment mode, predictors listen on 8080, not the default HTTP port 80
	PredictorPort int `json:"predictor_port"`

	// Services maps service types to KServe InferenceService names
	// Legacy hardcoded services for backward compatibility
	Services KServeServices `json:"services"`

	// DynamicServices maps model names to KServe InferenceService names
	// Discovered from KSERVE_*_SERVICE environment variables (ADR-040)
	DynamicServices map[string]string `json:"dynamic_services,omitempty"`

	// Timeout for KServe API calls
	Timeout time.Duration `json:"timeout"`
}

// KServeServices holds the names of KServe InferenceServices (legacy, for backward compatibility)
type KServeServices struct {
	// AnomalyDetector is the KServe service for anomaly detection
	AnomalyDetector string `json:"anomaly_detector"`

	// PredictiveAnalytics is the KServe service for predictive analytics
	PredictiveAnalytics string `json:"predictive_analytics"`
}

// GetAnomalyDetectorURL returns the full URL for the anomaly detector KServe service
func (k *KServeConfig) GetAnomalyDetectorURL() string {
	if k.Services.AnomalyDetector == "" {
		return ""
	}
	return k.buildServiceURL(k.Services.AnomalyDetector)
}

// GetPredictiveAnalyticsURL returns the full URL for the predictive analytics KServe service
func (k *KServeConfig) GetPredictiveAnalyticsURL() string {
	if k.Services.PredictiveAnalytics == "" {
		return ""
	}
	return k.buildServiceURL(k.Services.PredictiveAnalytics)
}

// buildServiceURL constructs the full service URL including the predictor port
func (k *KServeConfig) buildServiceURL(serviceName string) string {
	port := k.PredictorPort
	if port == 0 {
		port = DefaultKServePredictorPort
	}
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", serviceName, k.Namespace, port)
}

// GetAllServices returns all registered KServe services (legacy + dynamic)
func (k *KServeConfig) GetAllServices() map[string]string {
	services := make(map[string]string)

	// Add legacy services if configured
	if k.Services.AnomalyDetector != "" {
		services["anomaly-detector"] = k.Services.AnomalyDetector
	}
	if k.Services.PredictiveAnalytics != "" {
		services["predictive-analytics"] = k.Services.PredictiveAnalytics
	}

	// Add dynamic services
	for name, svc := range k.DynamicServices {
		services[name] = svc
	}

	return services
}

// HasServices returns true if any KServe services are configured
func (k *KServeConfig) HasServices() bool {
	return len(k.GetAllServices()) > 0
}

// ServiceCount returns the number of configured KServe services
func (k *KServeConfig) ServiceCount() int {
	return len(k.GetAllServices())
}

// Default configuration values
const (
	DefaultPort            = 8080
	DefaultMetricsPort     = 9090
	DefaultLogLevel        = "info"
	DefaultNamespace       = "self-healing-platform"
	DefaultMLServiceURL    = "" // Deprecated: use KServe integration
	DefaultHTTPTimeout     = 30 * time.Second
	DefaultKubernetesQPS   = 50.0
	DefaultKubernetesBurst = 100
	DefaultEnableCORS      = false

	// Prometheus defaults - empty means disabled
	// In OpenShift, typically: https://prometheus-k8s.openshift-monitoring.svc:9091
	DefaultPrometheusURL = ""

	// KServe defaults (ADR-039)
	DefaultKServeEnabled       = true
	DefaultKServeNamespace     = "self-healing-platform"
	DefaultKServeTimeout       = 10 * time.Second
	DefaultKServePredictorPort = 8080 // KServe predictors in RawDeployment mode listen on 8080
)

// Valid log levels
var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
	"fatal": true,
	"panic": true,
}

// Load loads configuration from environment variables with defaults
func Load() (*Config, error) {
	cfg := &Config{
		Port:            getEnvAsInt("PORT", DefaultPort),
		MetricsPort:     getEnvAsInt("METRICS_PORT", DefaultMetricsPort),
		LogLevel:        getEnv("LOG_LEVEL", DefaultLogLevel),
		Kubeconfig:      getEnv("KUBECONFIG", ""),
		Namespace:       getEnv("NAMESPACE", DefaultNamespace),
		MLServiceURL:    getEnv("ML_SERVICE_URL", DefaultMLServiceURL), // Deprecated
		ArgocdAPIURL:    getEnv("ARGOCD_API_URL", ""),
		PrometheusURL:   getEnv("PROMETHEUS_URL", DefaultPrometheusURL),
		HTTPTimeout:     getEnvAsDuration("HTTP_TIMEOUT", DefaultHTTPTimeout),
		EnableCORS:      getEnvAsBool("ENABLE_CORS", DefaultEnableCORS),
		CORSAllowOrigin: getEnvAsSlice("CORS_ALLOW_ORIGIN", []string{"*"}),
		KubernetesQPS:   getEnvAsFloat32("KUBERNETES_QPS", DefaultKubernetesQPS),
		KubernetesBurst: getEnvAsInt("KUBERNETES_BURST", DefaultKubernetesBurst),

		// KServe configuration (ADR-039, ADR-040)
		KServe: KServeConfig{
			Enabled:       getEnvAsBool("ENABLE_KSERVE_INTEGRATION", DefaultKServeEnabled),
			Namespace:     getEnv("KSERVE_NAMESPACE", DefaultKServeNamespace),
			PredictorPort: getEnvAsInt("KSERVE_PREDICTOR_PORT", DefaultKServePredictorPort),
			Services: KServeServices{
				AnomalyDetector:     getEnv("KSERVE_ANOMALY_DETECTOR_SERVICE", ""),
				PredictiveAnalytics: getEnv("KSERVE_PREDICTIVE_ANALYTICS_SERVICE", ""),
			},
			DynamicServices: discoverKServeServicesFromEnv(),
			Timeout:         getEnvAsDuration("KSERVE_TIMEOUT", DefaultKServeTimeout),
		},
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// Validate validates the configuration
//
//nolint:gocyclo // complexity acceptable for comprehensive config validation
func (c *Config) Validate() error {
	var errors []string

	// Validate port numbers
	if c.Port < 1 || c.Port > 65535 {
		errors = append(errors, fmt.Sprintf("invalid port: %d (must be 1-65535)", c.Port))
	}
	if c.MetricsPort < 1 || c.MetricsPort > 65535 {
		errors = append(errors, fmt.Sprintf("invalid metrics_port: %d (must be 1-65535)", c.MetricsPort))
	}
	if c.Port == c.MetricsPort {
		errors = append(errors, "port and metrics_port cannot be the same")
	}

	// Validate log level
	if !validLogLevels[strings.ToLower(c.LogLevel)] {
		errors = append(errors, fmt.Sprintf("invalid log_level: %s (must be debug, info, warn, error, fatal, or panic)", c.LogLevel))
	}

	// Validate namespace
	if c.Namespace == "" {
		errors = append(errors, "namespace cannot be empty")
	}

	// Validate ML integration: either KServe or legacy ML_SERVICE_URL must be configured
	if c.KServe.Enabled {
		// Validate KServe configuration (ADR-039, ADR-040)
		if c.KServe.Namespace == "" {
			errors = append(errors, "kserve.namespace cannot be empty when KServe is enabled")
		}
		// At least one service must be configured (legacy or dynamic)
		if !c.KServe.HasServices() {
			errors = append(errors, "at least one KServe service must be configured via KSERVE_*_SERVICE environment variables")
		}
		if c.KServe.Timeout < 1*time.Second {
			errors = append(errors, fmt.Sprintf("kserve.timeout too short: %s (must be >= 1s)", c.KServe.Timeout))
		}
		if c.KServe.Timeout > 2*time.Minute {
			errors = append(errors, fmt.Sprintf("kserve.timeout too long: %s (must be <= 2m)", c.KServe.Timeout))
		}
	} else if c.MLServiceURL != "" {
		// Legacy ML_SERVICE_URL validation (deprecated but still supported)
		if !strings.HasPrefix(c.MLServiceURL, "http://") && !strings.HasPrefix(c.MLServiceURL, "https://") {
			errors = append(errors, fmt.Sprintf("ml_service_url must start with http:// or https://: %s", c.MLServiceURL))
		}
	}
	// Note: Both KServe and ML_SERVICE_URL can be disabled - ML features will be unavailable

	// Validate ArgoCD URL if provided
	if c.ArgocdAPIURL != "" {
		if !strings.HasPrefix(c.ArgocdAPIURL, "http://") && !strings.HasPrefix(c.ArgocdAPIURL, "https://") {
			errors = append(errors, fmt.Sprintf("argocd_api_url must start with http:// or https://: %s", c.ArgocdAPIURL))
		}
	}

	// Validate Prometheus URL if provided
	if c.PrometheusURL != "" {
		if !strings.HasPrefix(c.PrometheusURL, "http://") && !strings.HasPrefix(c.PrometheusURL, "https://") {
			errors = append(errors, fmt.Sprintf("prometheus_url must start with http:// or https://: %s", c.PrometheusURL))
		}
	}

	// Validate HTTP timeout
	if c.HTTPTimeout < 1*time.Second {
		errors = append(errors, fmt.Sprintf("http_timeout too short: %s (must be >= 1s)", c.HTTPTimeout))
	}
	if c.HTTPTimeout > 5*time.Minute {
		errors = append(errors, fmt.Sprintf("http_timeout too long: %s (must be <= 5m)", c.HTTPTimeout))
	}

	// Validate Kubernetes client settings
	if c.KubernetesQPS <= 0 {
		errors = append(errors, fmt.Sprintf("kubernetes_qps must be positive: %f", c.KubernetesQPS))
	}
	if c.KubernetesBurst <= 0 {
		errors = append(errors, fmt.Sprintf("kubernetes_burst must be positive: %d", c.KubernetesBurst))
	}

	if len(errors) > 0 {
		return fmt.Errorf("configuration validation failed:\n  - %s", strings.Join(errors, "\n  - "))
	}

	return nil
}

// UseKServe returns true if KServe integration should be used
func (c *Config) UseKServe() bool {
	return c.KServe.Enabled && (c.KServe.Services.AnomalyDetector != "" || c.KServe.Services.PredictiveAnalytics != "")
}

// UseLegacyML returns true if legacy ML_SERVICE_URL should be used
func (c *Config) UseLegacyML() bool {
	return !c.KServe.Enabled && c.MLServiceURL != ""
}

// HasMLIntegration returns true if any ML integration is available
func (c *Config) HasMLIntegration() bool {
	return c.UseKServe() || c.UseLegacyML()
}

// getEnv gets an environment variable or returns a default value
func getEnv(key, defaultVal string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultVal
}

// getEnvAsInt gets an environment variable as an integer or returns a default value
func getEnvAsInt(key string, defaultVal int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultVal
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultVal
	}
	return value
}

// getEnvAsFloat32 gets an environment variable as a float32 or returns a default value
func getEnvAsFloat32(key string, defaultVal float32) float32 {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultVal
	}
	value, err := strconv.ParseFloat(valueStr, 32)
	if err != nil {
		return defaultVal
	}
	return float32(value)
}

// getEnvAsBool gets an environment variable as a boolean or returns a default value
func getEnvAsBool(key string, defaultVal bool) bool {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultVal
	}
	value, err := strconv.ParseBool(valueStr)
	if err != nil {
		return defaultVal
	}
	return value
}

// getEnvAsDuration gets an environment variable as a duration or returns a default value
func getEnvAsDuration(key string, defaultVal time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultVal
	}
	value, err := time.ParseDuration(valueStr)
	if err != nil {
		return defaultVal
	}
	return value
}

// getEnvAsSlice gets an environment variable as a comma-separated slice or returns a default value
func getEnvAsSlice(key string, defaultVal []string) []string {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultVal
	}
	parts := strings.Split(valueStr, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return defaultVal
	}
	return result
}

// discoverKServeServicesFromEnv discovers KServe services from environment variables.
// Pattern: KSERVE_<MODEL_NAME>_SERVICE = service-name
// Example: KSERVE_DISK_FAILURE_PREDICTOR_SERVICE = disk-failure-predictor-predictor
// This enables users to add custom models via values-hub.yaml without code changes (ADR-040).
func discoverKServeServicesFromEnv() map[string]string {
	services := make(map[string]string)

	for _, env := range os.Environ() {
		// Skip non-KServe environment variables
		if !strings.HasPrefix(env, "KSERVE_") {
			continue
		}

		// Skip known configuration variables
		if strings.HasPrefix(env, "KSERVE_NAMESPACE") ||
			strings.HasPrefix(env, "KSERVE_TIMEOUT") ||
			strings.HasPrefix(env, "KSERVE_PREDICTOR_PORT") ||
			strings.HasPrefix(env, "KSERVE_ANOMALY_DETECTOR_SERVICE") ||
			strings.HasPrefix(env, "KSERVE_PREDICTIVE_ANALYTICS_SERVICE") {
			continue
		}

		// Check for _SERVICE suffix
		if !strings.Contains(env, "_SERVICE=") {
			continue
		}

		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}

		envKey := parts[0]
		serviceName := parts[1]

		// Convert KSERVE_DISK_FAILURE_PREDICTOR_SERVICE → disk-failure-predictor
		modelName := strings.TrimPrefix(envKey, "KSERVE_")
		modelName = strings.TrimSuffix(modelName, "_SERVICE")
		modelName = strings.ToLower(strings.ReplaceAll(modelName, "_", "-"))

		services[modelName] = serviceName
	}

	return services
}
