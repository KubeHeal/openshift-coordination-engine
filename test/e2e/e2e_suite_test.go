//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// E2ETestSuite provides common setup for end-to-end tests
type E2ETestSuite struct {
	suite.Suite
	clientset *kubernetes.Clientset
	ctx       context.Context
	namespace string
}

// SetupSuite runs once before all tests
func (s *E2ETestSuite) SetupSuite() {
	s.ctx = context.Background()
	s.namespace = "coordination-engine-e2e"

	// Skip if KUBECONFIG is not set
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		s.T().Skip("Skipping e2e tests: KUBECONFIG not set")
	}

	// Build config from kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		s.T().Skipf("Skipping e2e tests: failed to build config: %v", err)
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		s.T().Skipf("Skipping e2e tests: failed to create clientset: %v", err)
	}

	s.clientset = clientset
}

// TestE2ESuite runs the e2e test suite
func TestE2ESuite(t *testing.T) {
	suite.Run(t, new(E2ETestSuite))
}

// TestOpenShiftClusterAccess verifies we can access OpenShift-specific resources
func (s *E2ETestSuite) TestOpenShiftClusterAccess() {
	if s.clientset == nil {
		s.T().Skip("Clientset not initialized")
	}

	// Try to get server version
	version, err := s.clientset.Discovery().ServerVersion()
	s.Require().NoError(err, "Failed to get server version")
	s.Require().NotEmpty(version.GitVersion, "Server version should not be empty")

	s.T().Logf("Connected to cluster version: %s", version.GitVersion)
}
