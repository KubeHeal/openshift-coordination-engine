//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// IntegrationTestSuite provides common setup for integration tests
type IntegrationTestSuite struct {
	suite.Suite
	clientset *kubernetes.Clientset
	ctx       context.Context
}

// SetupSuite runs once before all tests
func (s *IntegrationTestSuite) SetupSuite() {
	s.ctx = context.Background()

	// Skip if KUBECONFIG is not set
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		s.T().Skip("Skipping integration tests: KUBECONFIG not set")
	}

	// Build config from kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		s.T().Skipf("Skipping integration tests: failed to build config: %v", err)
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		s.T().Skipf("Skipping integration tests: failed to create clientset: %v", err)
	}

	s.clientset = clientset
}

// TestIntegrationSuite runs the integration test suite
func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}

// TestKubernetesConnection verifies we can connect to the cluster
func (s *IntegrationTestSuite) TestKubernetesConnection() {
	if s.clientset == nil {
		s.T().Skip("Clientset not initialized")
	}

	// Try to get server version
	version, err := s.clientset.Discovery().ServerVersion()
	s.Require().NoError(err, "Failed to get server version")
	s.Require().NotEmpty(version.GitVersion, "Server version should not be empty")

	s.T().Logf("Connected to Kubernetes cluster version: %s", version.GitVersion)
}
