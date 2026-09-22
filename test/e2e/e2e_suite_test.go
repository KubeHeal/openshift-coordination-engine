//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		// Try default location
		home, _ := os.UserHomeDir()
		kubeconfig = home + "/.kube/config"
		if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
			s.T().Skip("Skipping e2e tests: KUBECONFIG not set and default not found")
		}
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		s.T().Skipf("Skipping e2e tests: failed to build config: %v", err)
	}

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

// TestOpenShiftClusterAccess verifies we can access cluster resources
func (s *E2ETestSuite) TestOpenShiftClusterAccess() {
	if s.clientset == nil {
		s.T().Skip("Clientset not initialized")
	}

	version, err := s.clientset.Discovery().ServerVersion()
	s.Require().NoError(err, "Failed to get server version")
	s.Require().NotEmpty(version.GitVersion, "Server version should not be empty")

	s.T().Logf("Connected to cluster version: %s", version.GitVersion)
}

// getE2EImage returns the container image to use for E2E tests.
// Reads E2E_IMAGE env var; defaults to coordination-engine:e2e-test.
func getE2EImage() string {
	if img := os.Getenv("E2E_IMAGE"); img != "" {
		return img
	}
	return "coordination-engine:e2e-test"
}

// helmInstall runs helm install with the given parameters.
func (s *E2ETestSuite) helmInstall(chart, release, namespace string, setValues map[string]string) error {
	args := make([]string, 0, 9+2*len(setValues))
	args = append(args,
		"install", release, chart,
		"--namespace", namespace,
		"--create-namespace",
		"--wait",
		"--timeout", "3m",
	)
	for k, v := range setValues {
		flag := "--set"
		if strings.HasSuffix(k, ".value") && strings.Contains(k, "env[") {
			flag = "--set-string"
		}
		args = append(args, flag, fmt.Sprintf("%s=%s", k, v))
	}

	s.T().Logf("Running: helm %s", strings.Join(args, " "))
	cmd := exec.CommandContext(s.ctx, "helm", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm install failed: %w\nOutput: %s", err, string(out))
	}
	s.T().Logf("Helm install output:\n%s", string(out))
	return nil
}

// helmUninstall runs helm uninstall for the given release.
func (s *E2ETestSuite) helmUninstall(release, namespace string) error { //nolint:unparam // params kept for reuse across test files
	cmd := exec.CommandContext(s.ctx, "helm", "uninstall", release, "--namespace", namespace)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm uninstall failed: %w\nOutput: %s", err, string(out))
	}
	s.T().Logf("Helm uninstall output:\n%s", string(out))
	return nil
}

// waitForDeployment polls until the named Deployment has the desired ready replicas.
func (s *E2ETestSuite) waitForDeployment(namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dep, err := s.clientset.AppsV1().Deployments(namespace).Get(s.ctx, name, metav1.GetOptions{})
		if err == nil && deploymentReady(dep) {
			s.T().Logf("Deployment %s/%s is ready (%d/%d replicas)",
				namespace, name, dep.Status.ReadyReplicas, *dep.Spec.Replicas)
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("deployment %s/%s not ready within %v", namespace, name, timeout)
}

func deploymentReady(dep *appsv1.Deployment) bool {
	if dep.Spec.Replicas == nil {
		return dep.Status.ReadyReplicas >= 1
	}
	return dep.Status.ReadyReplicas >= *dep.Spec.Replicas
}

// portForwardAndGet starts a kubectl port-forward, makes an HTTP GET, and returns the result.
func (s *E2ETestSuite) portForwardAndGet(namespace, podName string, containerPort int, path string) (int, []byte, error) {
	localPort := 18080 + containerPort%1000

	// Start port-forward in background
	pfCmd := exec.CommandContext(s.ctx, "kubectl", "port-forward",
		fmt.Sprintf("pod/%s", podName),
		fmt.Sprintf("%d:%d", localPort, containerPort),
		"-n", namespace,
	)
	pfCmd.Stdout = io.Discard
	pfCmd.Stderr = io.Discard
	if err := pfCmd.Start(); err != nil {
		return 0, nil, fmt.Errorf("failed to start port-forward: %w", err)
	}
	defer func() {
		_ = pfCmd.Process.Kill()
		_ = pfCmd.Wait()
	}()

	// Wait for port-forward to be ready
	time.Sleep(2 * time.Second)

	url := fmt.Sprintf("http://localhost:%d%s", localPort, path)
	client := &http.Client{Timeout: 10 * time.Second}

	var lastErr error
	for i := 0; i < 5; i++ {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(1 * time.Second)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, body, nil
	}
	return 0, nil, fmt.Errorf("GET %s failed after retries: %w", url, lastErr)
}

// getFirstPod returns the first running pod matching the label selector.
func (s *E2ETestSuite) getFirstPod(namespace, labelSelector string) (*corev1.Pod, error) {
	pods, err := s.clientset.CoreV1().Pods(namespace).List(s.ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("listing pods with selector %s in %s: %w", labelSelector, namespace, err)
	}
	for i := range pods.Items {
		if pods.Items[i].Status.Phase == corev1.PodRunning {
			return &pods.Items[i], nil
		}
	}
	return nil, fmt.Errorf("no running pod found with selector %s in %s", labelSelector, namespace)
}

// deleteNamespace removes a namespace and waits for it to be fully gone.
func (s *E2ETestSuite) deleteNamespace(namespace string) { //nolint:unparam // param kept for reuse across test files
	err := s.clientset.CoreV1().Namespaces().Delete(s.ctx, namespace, metav1.DeleteOptions{})
	if err != nil {
		return // namespace likely doesn't exist
	}
	// Wait up to 60 seconds for the namespace to be fully terminated
	for i := 0; i < 30; i++ {
		_, getErr := s.clientset.CoreV1().Namespaces().Get(s.ctx, namespace, metav1.GetOptions{})
		if getErr != nil {
			return // namespace is gone
		}
		time.Sleep(2 * time.Second)
	}
	s.T().Logf("Warning: namespace %s still terminating after 60s", namespace)
}

// healthResponse is the expected shape of the /health endpoint response.
type healthResponse struct {
	Status string `json:"status"`
}

// assertHealthEndpoint verifies the /health endpoint returns 200 with status "ok".
func (s *E2ETestSuite) assertHealthEndpoint(namespace, podName string) {
	statusCode, body, err := s.portForwardAndGet(namespace, podName, 8080, "/health")
	s.Require().NoError(err, "Failed to reach /health endpoint")
	s.Require().Equal(http.StatusOK, statusCode, "Expected HTTP 200 from /health")

	var hr healthResponse
	err = json.Unmarshal(body, &hr)
	s.Require().NoError(err, "Failed to parse /health JSON response")
	s.Require().Equal("ok", hr.Status, "Expected status 'ok' in health response")

	s.T().Logf("Health check passed: %s", string(body))
}
