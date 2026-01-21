// Package main implements the coordination engine HTTP server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/tosin2013/openshift-coordination-engine/internal/coordination"
	"github.com/tosin2013/openshift-coordination-engine/internal/detector"
	"github.com/tosin2013/openshift-coordination-engine/internal/integrations"
	"github.com/tosin2013/openshift-coordination-engine/internal/rbac"
	"github.com/tosin2013/openshift-coordination-engine/internal/remediation"
	v1 "github.com/tosin2013/openshift-coordination-engine/pkg/api/v1"
	"github.com/tosin2013/openshift-coordination-engine/pkg/config"
	"github.com/tosin2013/openshift-coordination-engine/pkg/kserve"
	"github.com/tosin2013/openshift-coordination-engine/pkg/middleware"
)

var (
	// Version is set during build with -ldflags
	Version = "dev"
	// StartTime records when the application started
	startTime time.Time
)

// KubernetesClients holds both standard and dynamic Kubernetes clients
type KubernetesClients struct {
	Clientset     *kubernetes.Clientset
	DynamicClient dynamic.Interface
	Config        *rest.Config
}

func main() {
	// Record start time for uptime tracking
	startTime = time.Now()

	// Load configuration from environment
	cfg, err := config.Load()
	if err != nil {
		// Use basic logger for configuration errors
		log := logrus.New()
		log.SetFormatter(&logrus.JSONFormatter{})
		log.WithError(err).Fatal("Failed to load configuration")
	}

	// Initialize logger with configured log level
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})

	level, err := logrus.ParseLevel(cfg.LogLevel)
	if err != nil {
		log.Warnf("Invalid log level '%s', defaulting to info", cfg.LogLevel)
		level = logrus.InfoLevel
	}
	log.SetLevel(level)

	log.WithFields(logrus.Fields{
		"version":   Version,
		"namespace": cfg.Namespace,
		"port":      cfg.Port,
	}).Info("Starting OpenShift Coordination Engine")

	// Initialize Kubernetes clients (standard + dynamic)
	k8sClients, err := initKubernetesClient(cfg, log)
	if err != nil {
		log.WithError(err).Fatal("Failed to initialize Kubernetes clients")
	}
	log.WithFields(logrus.Fields{
		"cluster_host": k8sClients.Config.Host,
		"has_dynamic":  k8sClients.DynamicClient != nil,
	}).Info("Kubernetes clients initialized")

	// Verify RBAC permissions
	rbacVerifier := rbac.NewVerifier(k8sClients.Clientset, cfg.Namespace, log)
	if err := rbacVerifier.CheckCriticalPermissions(context.Background()); err != nil {
		log.WithError(err).Fatal("Critical RBAC permissions missing - cannot start")
	}
	log.Info("RBAC permissions verified successfully")

	// Initialize ML service client
	mlClient := integrations.NewMLClient(cfg.MLServiceURL, cfg.HTTPTimeout, log)
	defer mlClient.Close()

	log.WithField("ml_service_url", cfg.MLServiceURL).Info("ML service client initialized")

	// Initialize MCO client for infrastructure layer monitoring
	mcoClient := integrations.NewMCOClient(k8sClients.DynamicClient, log)
	log.Info("MCO client initialized for infrastructure layer monitoring")

	// Initialize deployment detector
	deploymentDetector := detector.NewDetector(k8sClients.Clientset, log)
	log.Info("Deployment detector initialized")

	// Initialize multi-layer coordination components (Phase 3)
	layerDetector := coordination.NewLayerDetector(log)
	log.Info("Layer detector initialized")

	multiLayerPlanner := coordination.NewMultiLayerPlanner(log)
	log.Info("Multi-layer planner initialized")

	healthChecker := coordination.NewHealthChecker(k8sClients.Clientset, k8sClients.DynamicClient, log)
	log.Info("Health checker initialized")

	// Initialize remediation components using helper function
	orchestrator, strategySelector := initRemediationComponents(cfg, k8sClients, deploymentDetector, log)

	// Initialize multi-layer orchestrator with remediation integration (Phase 4)
	multiLayerOrchestrator := coordination.NewMultiLayerOrchestrator(
		healthChecker,
		deploymentDetector,
		strategySelector,
		k8sClients.Clientset,
		log,
	)
	log.Info("Multi-layer orchestrator initialized with remediation integration")

	// Setup HTTP router with middleware
	router := mux.NewRouter()

	// Apply global middleware
	router.Use(middleware.Recovery(log))
	router.Use(middleware.RequestLogger(log))

	// Initialize KServe proxy client if enabled (ADR-039, ADR-040)
	kserveProxyHandler := initKServeProxy(cfg, log)

	// Verify KServe model availability on startup
	verifyKServeModelsOnStartup(cfg, kserveProxyHandler, log)

	// Create API handlers
	healthHandler := v1.NewHealthHandler(log, k8sClients.Clientset, rbacVerifier, cfg.MLServiceURL, Version, startTime)
	// TODO: Add MCO health monitoring to health handler in future enhancement
	_ = mcoClient // MCO client available for infrastructure layer operations
	remediationHandler := v1.NewRemediationHandler(orchestrator, log)
	detectionHandler := v1.NewDetectionHandler(deploymentDetector, log)
	coordinationHandler := v1.NewCoordinationHandler(layerDetector, multiLayerPlanner, multiLayerOrchestrator, log)
	log.Info("Coordination handler initialized")

	// Initialize Prometheus client for metrics querying (optional)
	prometheusClient := initPrometheusClient(cfg, log)

	// Create recommendations handler with KServe integration for ML predictions
	var recommendationsHandler *v1.RecommendationsHandler
	var predictionHandler *v1.PredictionHandler
	if kserveProxyHandler != nil {
		recommendationsHandler = v1.NewRecommendationsHandler(
			orchestrator,
			remediationHandler.GetIncidentStore(),
			kserveProxyHandler.GetProxyClient(),
			log,
		)
		predictionHandler = v1.NewPredictionHandler(
			kserveProxyHandler.GetProxyClient(),
			prometheusClient,
			log,
		)
	} else {
		recommendationsHandler = v1.NewRecommendationsHandler(
			orchestrator,
			remediationHandler.GetIncidentStore(),
			nil, // No KServe client
			log,
		)
		predictionHandler = v1.NewPredictionHandler(
			nil, // No KServe client
			prometheusClient,
			log,
		)
	}

	// Configure Prometheus client for real metrics if available
	if prometheusClient != nil {
		recommendationsHandler.SetPrometheusClient(prometheusClient)
		log.WithField("prometheus_url", cfg.PrometheusURL).Info("Prometheus client configured for ML predictions")
	}
	log.Info("Recommendations handler initialized")

	// API v1 routes
	apiV1 := router.PathPrefix("/api/v1").Subrouter()

	// Health check
	apiV1.Handle("/health", healthHandler).Methods("GET")

	// Remediation endpoints
	apiV1.HandleFunc("/remediation/trigger", remediationHandler.TriggerRemediation).Methods("POST")
	apiV1.HandleFunc("/workflows/{id}", remediationHandler.GetWorkflow).Methods("GET")
	apiV1.HandleFunc("/incidents", remediationHandler.ListIncidents).Methods("GET")
	apiV1.HandleFunc("/incidents", remediationHandler.CreateIncident).Methods("POST")

	// Recommendations endpoint (ML-powered remediation predictions)
	apiV1.HandleFunc("/recommendations", recommendationsHandler.GetRecommendations).Methods("POST")
	log.Info("Recommendations API endpoint registered: POST /api/v1/recommendations")

	// Prediction endpoint (time-specific resource predictions)
	predictionHandler.RegisterRoutes(router)
	log.Info("Prediction API endpoint registered: POST /api/v1/predict")

	// Detection endpoints
	detectionHandler.RegisterRoutes(router)
	log.Info("Detection API endpoints registered")

	// Coordination endpoints (multi-layer remediation)
	coordinationHandler.RegisterRoutes(router)
	log.Info("Coordination API endpoints registered")

	// Capacity analysis endpoints (Issue #27)
	capacityHandler := v1.NewCapacityHandler(k8sClients.Clientset, prometheusClient, log)
	capacityHandler.RegisterRoutes(router)
	log.Info("Capacity API endpoints registered: /api/v1/capacity/namespace/{namespace}, /api/v1/capacity/cluster")

	// Anomaly analysis endpoints (Issue #30)
	anomalyHandler := initAnomalyHandler(kserveProxyHandler, prometheusClient, log)
	anomalyHandler.RegisterRoutes(router)
	log.Info("Anomaly analysis API endpoint registered: POST /api/v1/anomalies/analyze")

	// KServe proxy endpoints (ADR-039, ADR-040)
	if kserveProxyHandler != nil {
		kserveProxyHandler.RegisterRoutes(router)
		log.Info("✅ KServe proxy endpoints registered: /api/v1/detect, /api/v1/models")
	}

	// Add simple /health endpoint for backward compatibility with deployments
	// This provides a lightweight health check for liveness/readiness probes
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		response := map[string]string{
			"status":  "ok",
			"version": Version,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.WithError(err).Error("Failed to encode simple health response")
		}
	}).Methods("GET")
	log.Info("Simple /health endpoint registered for backward compatibility")

	// Metrics server (separate port)
	metricsRouter := mux.NewRouter()
	metricsRouter.Handle("/metrics", promhttp.Handler())

	// Start metrics server
	metricsServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler:           metricsRouter,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.WithField("port", cfg.MetricsPort).Info("Starting metrics server")
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("Metrics server failed")
		}
	}()

	// Start main API server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.WithField("port", cfg.Port).Info("Starting API server")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("API server failed")
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down servers...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.WithError(err).Error("API server shutdown error")
	}

	if err := metricsServer.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Metrics server shutdown error")
	}

	log.Info("Servers stopped")
}

// initKServeProxy initializes the KServe proxy client if enabled (ADR-039, ADR-040)
func initKServeProxy(cfg *config.Config, log *logrus.Logger) *v1.KServeProxyHandler {
	if !cfg.KServe.Enabled {
		log.Info("KServe integration disabled")
		return nil
	}

	kserveProxyConfig := kserve.ProxyConfig{
		Namespace: cfg.KServe.Namespace,
		Timeout:   cfg.KServe.Timeout,
	}

	kserveProxyClient, err := kserve.NewProxyClient(kserveProxyConfig, log)
	if err != nil {
		log.WithError(err).Warn("Failed to initialize KServe proxy client")
		return nil
	}

	handler := v1.NewKServeProxyHandler(kserveProxyClient, log)
	log.WithFields(logrus.Fields{
		"models":    kserveProxyClient.ListModels(),
		"namespace": cfg.KServe.Namespace,
	}).Info("✅ KServe proxy client initialized")

	return handler
}

// initRemediationComponents initializes all remediation-related components
func initRemediationComponents(
	cfg *config.Config,
	k8sClients *KubernetesClients,
	deploymentDetector *detector.DeploymentDetector,
	log *logrus.Logger,
) (*remediation.Orchestrator, *remediation.StrategySelector) {
	// Initialize remediation components
	manualRemediator := remediation.NewManualRemediator(k8sClients.Clientset, log)
	log.Info("Manual remediator initialized")

	helmRemediator := remediation.NewHelmRemediator(log)
	log.Info("Helm remediator initialized")

	operatorRemediator := remediation.NewOperatorRemediator(k8sClients.Clientset, k8sClients.DynamicClient, log)
	log.Info("Operator remediator initialized")

	// Initialize strategy selector for multi-remediator routing
	strategySelector := remediation.NewStrategySelector(log)
	strategySelector.SetFallbackRemediator(manualRemediator)
	strategySelector.RegisterRemediator(helmRemediator)
	strategySelector.RegisterRemediator(operatorRemediator)

	// Initialize ArgoCD client and remediator (if ArgoCD URL configured)
	if cfg.ArgocdAPIURL != "" {
		argocdToken := os.Getenv("ARGOCD_TOKEN")
		argocdClient := integrations.NewArgoCDClient(cfg.ArgocdAPIURL, argocdToken, log)
		argocdRemediator := remediation.NewArgoCDRemediator(argocdClient, log)
		strategySelector.RegisterRemediator(argocdRemediator)
		log.WithField("argocd_url", cfg.ArgocdAPIURL).Info("ArgoCD remediator initialized")
	} else {
		log.Warn("ARGOCD_API_URL not set, ArgoCD remediation disabled")
	}

	// Initialize remediation orchestrator
	orchestrator := remediation.NewOrchestrator(deploymentDetector, strategySelector, log)
	log.WithField("remediators", strategySelector.GetRegisteredRemediators()).Info("Remediation orchestrator initialized")

	return orchestrator, strategySelector
}

// initPrometheusClient creates a Prometheus query client if configured
func initPrometheusClient(cfg *config.Config, log *logrus.Logger) *integrations.PrometheusClient {
	if cfg.PrometheusURL == "" {
		log.Info("PROMETHEUS_URL not set, ML predictions will use default metric values")
		return nil
	}

	client := integrations.NewPrometheusClient(cfg.PrometheusURL, cfg.HTTPTimeout, log)
	if client == nil {
		log.Warn("Failed to create Prometheus client")
		return nil
	}

	log.WithField("prometheus_url", cfg.PrometheusURL).Info("Prometheus client initialized for metrics querying")
	return client
}

// initAnomalyHandler creates the anomaly analysis handler (Issue #30)
func initAnomalyHandler(
	kserveProxyHandler *v1.KServeProxyHandler,
	prometheusClient *integrations.PrometheusClient,
	log *logrus.Logger,
) *v1.AnomalyHandler {
	if kserveProxyHandler != nil {
		return v1.NewAnomalyHandler(
			kserveProxyHandler.GetProxyClient(),
			prometheusClient,
			log,
		)
	}
	return v1.NewAnomalyHandler(
		nil, // No KServe client
		prometheusClient,
		log,
	)
}

// verifyKServeModelsOnStartup validates KServe model availability on startup and logs warnings
// if models are not ready. This helps operators diagnose deployment issues early.
// Note: This function logs warnings but does not prevent startup - the coordination engine
// will continue to start even if models are unavailable, allowing it to serve other endpoints.
func verifyKServeModelsOnStartup(cfg *config.Config, kserveProxyHandler *v1.KServeProxyHandler, log *logrus.Logger) {
	if !cfg.KServe.Enabled || kserveProxyHandler == nil {
		return
	}

	log.Info("Verifying KServe model availability...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	modelsHealthy := true
	for _, modelName := range kserveProxyHandler.GetProxyClient().ListModels() {
		health, err := kserveProxyHandler.GetProxyClient().CheckModelHealth(ctx, modelName)
		if err != nil || health == nil || health.Status != "ready" {
			log.WithFields(logrus.Fields{
				"model": modelName,
				"error": err,
			}).Warn("KServe model not ready on startup - ML predictions will fail until models are deployed")
			modelsHealthy = false
		} else {
			log.WithField("model", modelName).Info("KServe model is healthy and ready")
		}
	}

	if !modelsHealthy {
		log.Warn("Some KServe models are not ready. Deploy InferenceServices and ensure model files exist.")
	}
}

// initKubernetesClient creates both standard and dynamic Kubernetes clients
// It tries in-cluster config first, then falls back to KUBECONFIG from configuration
func initKubernetesClient(cfg *config.Config, log *logrus.Logger) (*KubernetesClients, error) {
	var restConfig *rest.Config
	var err error
	var configSource string

	// Try in-cluster config first (when running inside a pod)
	restConfig, err = rest.InClusterConfig()
	if err != nil {
		configSource = "kubeconfig"
		// Fall back to kubeconfig file
		kubeconfig := cfg.Kubeconfig
		if kubeconfig == "" {
			homeDir := os.Getenv("HOME")
			if homeDir == "" {
				return nil, fmt.Errorf("KUBECONFIG not set and HOME directory not found")
			}
			kubeconfig = homeDir + "/.kube/config"
		}

		log.WithField("kubeconfig", kubeconfig).Debug("Using kubeconfig file")
		restConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig from %s: %w", kubeconfig, err)
		}
	} else {
		configSource = "in-cluster"
		log.Debug("Using in-cluster Kubernetes configuration")
	}

	// Set client configuration from config
	restConfig.QPS = cfg.KubernetesQPS
	restConfig.Burst = cfg.KubernetesBurst

	// Create standard Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	// Create dynamic client for CRD access (ArgoCD Applications, MachineConfigPools, etc.)
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	log.WithFields(logrus.Fields{
		"config_source": configSource,
		"cluster_host":  restConfig.Host,
		"qps":           cfg.KubernetesQPS,
		"burst":         cfg.KubernetesBurst,
	}).Debug("Kubernetes clients created successfully")

	return &KubernetesClients{
		Clientset:     clientset,
		DynamicClient: dynamicClient,
		Config:        restConfig,
	}, nil
}
