//go:build e2e

package e2e

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	helmReleaseName = "ce-e2e"
	helmChartPath   = "../../charts/coordination-engine"
	testNamespace   = "coordination-engine-e2e"
	deploymentName  = "ce-e2e-coordination-engine"
)

// TestHelmDeployAndHealthCheck is the core acceptance test.
// It deploys the coordination-engine via Helm, waits for the pod to be ready,
// and verifies the /health endpoint returns HTTP 200 with {"status":"ok"}.
func (s *E2ETestSuite) TestHelmDeployAndHealthCheck() {
	if s.clientset == nil {
		s.T().Skip("Clientset not initialized")
	}

	// Cleanup from any previous failed run
	_ = s.helmUninstall(helmReleaseName, testNamespace)
	s.deleteNamespace(testNamespace)

	image := getE2EImage()
	s.T().Logf("Using E2E image: %s", image)

	// Parse image into repository:tag
	repo, tag := parseImage(image)

	// Install the Helm chart
	err := s.helmInstall(helmChartPath, helmReleaseName, testNamespace, map[string]string{
		"image.repository": repo,
		"image.tag":        tag,
		"image.pullPolicy": "IfNotPresent",
		"replicaCount":     "1",
		"kserve.enabled":   "false",
		"env[0].name":      "LOG_LEVEL",
		"env[0].value":     "debug",
		"env[1].name":      "PORT",
		"env[1].value":     "8080",
		"env[2].name":      "ENABLE_KSERVE_INTEGRATION",
		"env[2].value":     "false",
	})
	s.Require().NoError(err, "Helm install failed")

	// Ensure cleanup runs even if test fails
	defer func() {
		s.T().Log("Cleaning up Helm release and namespace...")
		_ = s.helmUninstall(helmReleaseName, testNamespace)
		s.deleteNamespace(testNamespace)
	}()

	// Wait for deployment to be ready
	err = s.waitForDeployment(testNamespace, deploymentName, 3*time.Minute)
	s.Require().NoError(err, "Deployment did not become ready")

	// Find a running pod
	pod, err := s.getFirstPod(testNamespace,
		fmt.Sprintf("app.kubernetes.io/instance=%s", helmReleaseName))
	s.Require().NoError(err, "No running pod found")
	s.T().Logf("Found running pod: %s", pod.Name)

	// Verify health endpoint
	s.assertHealthEndpoint(testNamespace, pod.Name)
}

// TestHelmDeployRBACResources verifies that the Helm chart creates
// the expected RBAC resources (ServiceAccount, Role, RoleBinding).
func (s *E2ETestSuite) TestHelmDeployRBACResources() {
	if s.clientset == nil {
		s.T().Skip("Clientset not initialized")
	}

	// Cleanup + install
	_ = s.helmUninstall(helmReleaseName, testNamespace)
	s.deleteNamespace(testNamespace)

	image := getE2EImage()
	repo, tag := parseImage(image)

	err := s.helmInstall(helmChartPath, helmReleaseName, testNamespace, map[string]string{
		"image.repository": repo,
		"image.tag":        tag,
		"image.pullPolicy": "IfNotPresent",
		"kserve.enabled":   "false",
		"env[0].name":      "ENABLE_KSERVE_INTEGRATION",
		"env[0].value":     "false",
	})
	s.Require().NoError(err, "Helm install failed")

	defer func() {
		_ = s.helmUninstall(helmReleaseName, testNamespace)
		s.deleteNamespace(testNamespace)
	}()

	// Verify ServiceAccount exists
	saName := fmt.Sprintf("%s-coordination-engine", helmReleaseName)
	sa, err := s.clientset.CoreV1().ServiceAccounts(testNamespace).Get(s.ctx, saName, metav1.GetOptions{})
	s.Require().NoError(err, "ServiceAccount not found: %s", saName)
	s.T().Logf("ServiceAccount found: %s", sa.Name)

	// Verify Role exists
	roleName := fmt.Sprintf("%s-coordination-engine", helmReleaseName)
	role, err := s.clientset.RbacV1().Roles(testNamespace).Get(s.ctx, roleName, metav1.GetOptions{})
	s.Require().NoError(err, "Role not found: %s", roleName)
	s.T().Logf("Role found: %s (rules: %d)", role.Name, len(role.Rules))

	// Verify RoleBinding exists
	rbName := fmt.Sprintf("%s-coordination-engine", helmReleaseName)
	rb, err := s.clientset.RbacV1().RoleBindings(testNamespace).Get(s.ctx, rbName, metav1.GetOptions{})
	s.Require().NoError(err, "RoleBinding not found: %s", rbName)
	s.T().Logf("RoleBinding found: %s -> %s", rb.Name, rb.RoleRef.Name)
}

// TestHelmDeployMetricsPort verifies the metrics port (9090) is accessible.
func (s *E2ETestSuite) TestHelmDeployMetricsPort() {
	if s.clientset == nil {
		s.T().Skip("Clientset not initialized")
	}

	// Cleanup + install
	_ = s.helmUninstall(helmReleaseName, testNamespace)
	s.deleteNamespace(testNamespace)

	image := getE2EImage()
	repo, tag := parseImage(image)

	err := s.helmInstall(helmChartPath, helmReleaseName, testNamespace, map[string]string{
		"image.repository": repo,
		"image.tag":        tag,
		"image.pullPolicy": "IfNotPresent",
		"kserve.enabled":   "false",
		"env[0].name":      "ENABLE_KSERVE_INTEGRATION",
		"env[0].value":     "false",
	})
	s.Require().NoError(err, "Helm install failed")

	defer func() {
		_ = s.helmUninstall(helmReleaseName, testNamespace)
		s.deleteNamespace(testNamespace)
	}()

	// Wait for deployment
	err = s.waitForDeployment(testNamespace, deploymentName, 3*time.Minute)
	s.Require().NoError(err, "Deployment did not become ready")

	// Find running pod
	pod, err := s.getFirstPod(testNamespace,
		fmt.Sprintf("app.kubernetes.io/instance=%s", helmReleaseName))
	s.Require().NoError(err, "No running pod found")

	// Verify metrics endpoint is reachable on port 9090
	statusCode, body, err := s.portForwardAndGet(testNamespace, pod.Name, 9090, "/metrics")
	s.Require().NoError(err, "Failed to reach metrics endpoint on port 9090")
	s.Require().Equal(200, statusCode, "Expected HTTP 200 from /metrics")
	s.Require().Contains(string(body), "go_", "Expected Prometheus Go metrics in response")

	s.T().Logf("Metrics endpoint accessible, response contains %d bytes", len(body))
}

// parseImage splits an image string into repository and tag.
func parseImage(image string) (string, string) {
	parts := splitLast(image, ":")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return image, "latest"
}

func splitLast(s, sep string) []string {
	idx := lastIndex(s, sep)
	if idx < 0 {
		return []string{s}
	}
	return []string{s[:idx], s[idx+len(sep):]}
}

func lastIndex(s, sep string) int {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}
