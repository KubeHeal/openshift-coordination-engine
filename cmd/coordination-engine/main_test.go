package main

import (
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tosin2013/openshift-coordination-engine/pkg/config"
)

// testConfig creates a test configuration for unit tests
func testConfig() *config.Config {
	return &config.Config{
		Port:            8080,
		MetricsPort:     9090,
		LogLevel:        "info",
		Namespace:       "test-namespace",
		MLServiceURL:    "http://test-ml:8080",
		HTTPTimeout:     30 * time.Second,
		KubernetesQPS:   50.0,
		KubernetesBurst: 100,
	}
}

func TestInitKubernetesClient_NoConfig(t *testing.T) {
	// Setup test logger
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel) // Suppress logs during tests

	// Clear environment variables to simulate no config scenario
	os.Unsetenv("KUBECONFIG")
	os.Unsetenv("HOME")

	// Create config without kubeconfig path
	cfg := testConfig()
	cfg.Kubeconfig = ""

	// Test should fail when no config is available
	clients, err := initKubernetesClient(cfg, log)
	assert.Error(t, err, "Expected error when KUBECONFIG and HOME are not set")
	assert.Nil(t, clients, "Clients should be nil when initialization fails")
	assert.Contains(t, err.Error(), "KUBECONFIG not set and HOME directory not found",
		"Error message should indicate missing configuration")
}

func TestInitKubernetesClient_WithKubeconfig(t *testing.T) {
	// Skip if not running in a cluster or if kubeconfig doesn't exist
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		homeDir := os.Getenv("HOME")
		if homeDir != "" {
			kubeconfig = homeDir + "/.kube/config"
		}
	}

	if kubeconfig == "" {
		t.Skip("Skipping test: KUBECONFIG not set and HOME not found")
	}

	// Check if kubeconfig file exists
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		t.Skipf("Skipping test: kubeconfig file %s does not exist", kubeconfig)
	}

	// Setup test logger
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	// Create config with kubeconfig path
	cfg := testConfig()
	cfg.Kubeconfig = kubeconfig

	// Test client initialization with kubeconfig
	clients, err := initKubernetesClient(cfg, log)
	require.NoError(t, err, "Client initialization should succeed with valid kubeconfig")
	require.NotNil(t, clients, "Clients should not be nil")

	// Verify clientset was created
	assert.NotNil(t, clients.Clientset, "Standard clientset should be initialized")

	// Verify dynamic client was created
	assert.NotNil(t, clients.DynamicClient, "Dynamic client should be initialized")

	// Verify config was stored
	assert.NotNil(t, clients.Config, "REST config should be stored")
	assert.NotEmpty(t, clients.Config.Host, "Cluster host should be set")

	// Verify QPS and Burst settings
	assert.Equal(t, float32(50.0), clients.Config.QPS, "QPS should be set to 50.0")
	assert.Equal(t, 100, clients.Config.Burst, "Burst should be set to 100")
}

func TestInitKubernetesClient_InvalidKubeconfig(t *testing.T) {
	// Setup test logger
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create config with invalid kubeconfig path
	cfg := testConfig()
	cfg.Kubeconfig = "/non/existent/path/config"

	// Test should fail with invalid kubeconfig
	clients, err := initKubernetesClient(cfg, log)
	assert.Error(t, err, "Expected error with invalid kubeconfig path")
	assert.Nil(t, clients, "Clients should be nil when initialization fails")
	assert.Contains(t, err.Error(), "failed to load kubeconfig",
		"Error message should indicate kubeconfig loading failure")
}

func TestKubernetesClients_Structure(t *testing.T) {
	// Test that KubernetesClients struct has expected fields
	clients := &KubernetesClients{}

	// Verify struct can be instantiated
	assert.NotNil(t, clients, "KubernetesClients struct should be instantiable")

	// Verify nil fields are properly typed
	assert.Nil(t, clients.Clientset, "Uninitialized Clientset should be nil")
	assert.Nil(t, clients.DynamicClient, "Uninitialized DynamicClient should be nil")
	assert.Nil(t, clients.Config, "Uninitialized Config should be nil")
}

func TestInitKubernetesClient_ConfigSource(t *testing.T) {
	// This test verifies that the function properly determines config source
	// We can only test the kubeconfig path in unit tests (in-cluster requires pod environment)

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		homeDir := os.Getenv("HOME")
		if homeDir != "" {
			kubeconfig = homeDir + "/.kube/config"
		}
	}

	if kubeconfig == "" {
		t.Skip("Skipping test: No kubeconfig available")
	}

	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		t.Skipf("Skipping test: kubeconfig file %s does not exist", kubeconfig)
	}

	// Setup test logger to capture debug messages
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	// Create config with kubeconfig path
	cfg := testConfig()
	cfg.Kubeconfig = kubeconfig

	// Initialize clients
	clients, err := initKubernetesClient(cfg, log)
	require.NoError(t, err)
	require.NotNil(t, clients)

	// Verify that config was loaded from kubeconfig (not in-cluster)
	// This is implicit - if we're running tests locally, we're not in a pod
	assert.NotNil(t, clients.Config)
}
