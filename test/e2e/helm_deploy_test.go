//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

	image := getE2EImage()
	s.T().Logf("Using E2E image: %s", image)

	// Parse image into repository:tag
	repo, tag := parseImage(image)

	// Install (or upgrade) the Helm chart — upgrade --install is idempotent
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
		"env[3].name":      "NAMESPACE",
		"env[3].value":     testNamespace,
	})
	s.Require().NoError(err, "Helm install failed")

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

	image := getE2EImage()
	repo, tag := parseImage(image)

	// Override serviceAccount.name to empty so the template uses fullname
	err := s.helmInstall(helmChartPath, helmReleaseName, testNamespace, map[string]string{
		"image.repository":    repo,
		"image.tag":           tag,
		"image.pullPolicy":    "IfNotPresent",
		"kserve.enabled":      "false",
		"serviceAccount.name": "",
		"env[0].name":         "ENABLE_KSERVE_INTEGRATION",
		"env[0].value":        "false",
		"env[1].name":         "NAMESPACE",
		"env[1].value":        testNamespace,
	})
	s.Require().NoError(err, "Helm install failed")

	// ServiceAccount name follows fullname template: {release}-coordination-engine
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

	image := getE2EImage()
	repo, tag := parseImage(image)

	// Use same values as TestHelmDeployAndHealthCheck to avoid triggering a rollout
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
		"env[3].name":      "NAMESPACE",
		"env[3].value":     testNamespace,
	})
	s.Require().NoError(err, "Helm install failed")

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

// TestDryRunDoesNotChangeTargets posts dry_run against a Pod and a Deployment and
// checks that neither object is mutated.
func (s *E2ETestSuite) TestDryRunDoesNotChangeTargets() {
	if s.clientset == nil {
		s.T().Skip("Clientset not initialized")
	}

	repo, tag := parseImage(getE2EImage())
	err := s.helmInstall(helmChartPath, helmReleaseName, testNamespace, map[string]string{
		"image.repository": repo,
		"image.tag":        tag,
		"image.pullPolicy": "IfNotPresent",
		"replicaCount":     "1",
		"kserve.enabled":   "false",
	})
	s.Require().NoError(err, "Helm install failed")
	s.Require().NoError(s.waitForDeployment(testNamespace, deploymentName, 3*time.Minute))

	pod, err := s.clientset.CoreV1().Pods(testNamespace).Create(s.ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "dry-run-pod", Labels: map[string]string{"app": "dry-run-pod"}},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways,
			Containers:    []corev1.Container{{Name: "pause", Image: "registry.k8s.io/pause:3.10"}},
		},
	}, metav1.CreateOptions{})
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		_ = s.clientset.CoreV1().Pods(testNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
	})

	replicas := int32(1)
	deployment, err := s.clientset.AppsV1().Deployments(testNamespace).Create(s.ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dry-run-deploy"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "dry-run-deploy"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "dry-run-deploy"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "pause", Image: "registry.k8s.io/pause:3.10"}},
				},
			},
		},
	}, metav1.CreateOptions{})
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		_ = s.clientset.AppsV1().Deployments(testNamespace).Delete(context.Background(), deployment.Name, metav1.DeleteOptions{})
	})

	s.Require().NoError(s.waitForPodRunning(testNamespace, pod.Name, time.Minute))
	s.Require().NoError(s.waitForDeployment(testNamespace, deployment.Name, 2*time.Minute))

	enginePod, err := s.getFirstPod(testNamespace, fmt.Sprintf("app.kubernetes.io/instance=%s", helmReleaseName))
	s.Require().NoError(err)

	err = s.withPortForward(testNamespace, enginePod.Name, 8080, func(client *http.Client, baseURL string) error {
		currentPod, getErr := s.clientset.CoreV1().Pods(testNamespace).Get(s.ctx, pod.Name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("get dry-run pod: %w", getErr)
		}
		currentDeployment, getErr := s.clientset.AppsV1().Deployments(testNamespace).Get(s.ctx, deployment.Name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("get dry-run deployment: %w", getErr)
		}
		if postErr := s.postDryRun(client, baseURL, "Pod", currentPod.Name, "pod_crash"); postErr != nil {
			return postErr
		}
		if postErr := s.postDryRun(client, baseURL, "Deployment", currentDeployment.Name, "pod_crash_loop"); postErr != nil {
			return postErr
		}
		afterPod, getErr := s.clientset.CoreV1().Pods(testNamespace).Get(s.ctx, currentPod.Name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("get dry-run pod after request: %w", getErr)
		}
		afterDeployment, getErr := s.clientset.AppsV1().Deployments(testNamespace).Get(s.ctx, currentDeployment.Name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("get dry-run deployment after request: %w", getErr)
		}
		if afterPod.DeletionTimestamp != nil {
			return fmt.Errorf("dry run deleted pod %s", afterPod.Name)
		}
		if afterPod.ResourceVersion != currentPod.ResourceVersion {
			return fmt.Errorf("pod resourceVersion changed from %s to %s", currentPod.ResourceVersion, afterPod.ResourceVersion)
		}
		if afterDeployment.ResourceVersion != currentDeployment.ResourceVersion {
			return fmt.Errorf("deployment resourceVersion changed from %s to %s", currentDeployment.ResourceVersion, afterDeployment.ResourceVersion)
		}
		return nil
	})
	s.Require().NoError(err)
}

func (s *E2ETestSuite) postDryRun(client *http.Client, baseURL, kind, name, issueType string) error {
	payload := []byte(fmt.Sprintf(`{
		"incident_id": "inc-dry-run",
		"namespace": %q,
		"resource": {"kind": %q, "name": %q},
		"issue": {"type": %q, "severity": "high", "description": "dry run"},
		"dry_run": true
	}`, testNamespace, kind, name, issueType))
	resp, err := client.Post(baseURL+"/api/v1/remediation/trigger", "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("post dry run: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read dry run response: %w", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("dry run returned %d: %s", resp.StatusCode, body)
	}
	var response struct {
		WorkflowID     string   `json:"workflow_id"`
		PlannedActions []string `json:"planned_actions"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("decode dry run response: %w", err)
	}
	if response.WorkflowID != "" {
		return fmt.Errorf("dry run stored workflow %s", response.WorkflowID)
	}
	if len(response.PlannedActions) == 0 {
		return fmt.Errorf("dry run returned no planned actions: %s", body)
	}
	return nil
}

func (s *E2ETestSuite) waitForPodRunning(namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pod, err := s.clientset.CoreV1().Pods(namespace).Get(s.ctx, name, metav1.GetOptions{})
		if err == nil && pod.Status.Phase == corev1.PodRunning {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("pod %s/%s did not start", namespace, name)
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
