package remediation

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/KubeHeal/openshift-coordination-engine/pkg/models"
)

func TestNewManualRemediator(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	log := logrus.New()

	remediator := NewManualRemediator(clientset, log)

	assert.NotNil(t, remediator)
	assert.Equal(t, "manual", remediator.Name())
}

func TestManualRemediator_CanRemediate(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	log := logrus.New()
	remediator := NewManualRemediator(clientset, log)

	tests := []struct {
		name     string
		method   models.DeploymentMethod
		expected bool
	}{
		{
			name:     "Manual deployment",
			method:   models.DeploymentMethodManual,
			expected: true,
		},
		{
			name:     "Unknown deployment",
			method:   models.DeploymentMethodUnknown,
			expected: true,
		},
		{
			name:     "ArgoCD deployment",
			method:   models.DeploymentMethodArgoCD,
			expected: false,
		},
		{
			name:     "Helm deployment",
			method:   models.DeploymentMethodHelm,
			expected: false,
		},
		{
			name:     "Operator deployment",
			method:   models.DeploymentMethodOperator,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := models.NewDeploymentInfo("default", "test-app", "Deployment", tt.method, 0.9)
			result := remediator.CanRemediate(info)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestManualRemediator_RemediateCrashLoop(t *testing.T) {
	// Create fake clientset with a pod
	clientset := fake.NewSimpleClientset()
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel) // Reduce noise in tests

	// Create test pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "test:latest",
				},
			},
		},
	}
	_, err := clientset.CoreV1().Pods("default").Create(context.Background(), pod, metav1.CreateOptions{})
	assert.NoError(t, err)

	remediator := NewManualRemediator(clientset, log)
	deploymentInfo := models.NewDeploymentInfo("default", "test-pod", "Pod", models.DeploymentMethodManual, 0.6)

	issue := &models.Issue{
		ID:           "issue-1",
		Type:         "CrashLoopBackOff",
		Severity:     "high",
		Namespace:    "default",
		ResourceType: "pod",
		ResourceName: "test-pod",
		Description:  "Pod is crash looping",
		DetectedAt:   time.Now(),
	}

	// Execute remediation
	err = remediator.Remediate(context.Background(), deploymentInfo, issue)
	assert.NoError(t, err)

	// Verify pod was deleted
	_, err = clientset.CoreV1().Pods("default").Get(context.Background(), "test-pod", metav1.GetOptions{})
	assert.Error(t, err) // Pod should not exist anymore
}

func TestManualRemediator_RemediateImagePull(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create test pod with ImagePullBackOff
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "image-pull-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "nonexistent/image:latest",
				},
			},
		},
	}
	_, err := clientset.CoreV1().Pods("default").Create(context.Background(), pod, metav1.CreateOptions{})
	assert.NoError(t, err)

	remediator := NewManualRemediator(clientset, log)
	deploymentInfo := models.NewDeploymentInfo("default", "image-pull-pod", "Pod", models.DeploymentMethodManual, 0.6)

	issue := &models.Issue{
		ID:           "issue-2",
		Type:         "ImagePullBackOff",
		Severity:     "high",
		Namespace:    "default",
		ResourceType: "pod",
		ResourceName: "image-pull-pod",
		Description:  "Cannot pull image",
		DetectedAt:   time.Now(),
	}

	// Execute remediation - should return error for ImagePullBackOff
	err = remediator.Remediate(context.Background(), deploymentInfo, issue)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "manual intervention")
}

func TestManualRemediator_RemediateGeneric(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create test pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "generic-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "test:latest",
				},
			},
		},
	}
	_, err := clientset.CoreV1().Pods("default").Create(context.Background(), pod, metav1.CreateOptions{})
	assert.NoError(t, err)

	remediator := NewManualRemediator(clientset, log)
	deploymentInfo := models.NewDeploymentInfo("default", "generic-pod", "Pod", models.DeploymentMethodManual, 0.6)

	issue := &models.Issue{
		ID:           "issue-3",
		Type:         "UnknownError",
		Severity:     "medium",
		Namespace:    "default",
		ResourceType: "pod",
		ResourceName: "generic-pod",
		Description:  "Unknown error occurred",
		DetectedAt:   time.Now(),
	}

	// Execute remediation
	err = remediator.Remediate(context.Background(), deploymentInfo, issue)
	assert.NoError(t, err)

	// Verify pod was deleted
	_, err = clientset.CoreV1().Pods("default").Get(context.Background(), "generic-pod", metav1.GetOptions{})
	assert.Error(t, err)
}

// --- OOM remediation tests (Issue #62) ---

// createOOMTestFixture builds a Deployment -> ReplicaSet -> Pod ownership chain
// in the fake clientset and returns the remediator, deployment info, and issue.
func createOOMTestFixture(t *testing.T, memoryLimit string) (
	*ManualRemediator, *models.DeploymentInfo, *models.Issue, *fake.Clientset,
) {
	t.Helper()

	replicas := int32(1)
	trueVal := true

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "broken-app",
			Namespace: "default",
			UID:       "deploy-uid-1",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "broken-app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "broken-app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "web",
							Image: "test:latest",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse(memoryLimit),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
							},
						},
					},
				},
			},
		},
	}

	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "broken-app-rs1",
			Namespace: "default",
			UID:       "rs-uid-1",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "broken-app",
					UID:        "deploy-uid-1",
					Controller: &trueVal,
				},
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "broken-app-pod-abc",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "ReplicaSet",
					Name:       "broken-app-rs1",
					UID:        "rs-uid-1",
					Controller: &trueVal,
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "web",
					Image: "test:latest",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse(memoryLimit),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "web",
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
					},
				},
			},
		},
	}

	clientset := fake.NewSimpleClientset(deploy, rs, pod)
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	remediator := NewManualRemediator(clientset, log)

	deploymentInfo := models.NewDeploymentInfo("default", "broken-app-pod-abc", "Pod", models.DeploymentMethodManual, 0.6)
	issue := &models.Issue{
		ID:           "issue-oom",
		Type:         "OOMKilled",
		Severity:     "high",
		Namespace:    "default",
		ResourceType: "pod",
		ResourceName: "broken-app-pod-abc",
		Description:  "OOMKilled",
		DetectedAt:   time.Now(),
	}

	return remediator, deploymentInfo, issue, clientset
}

func TestRemediateOOM_PatchesDeploymentLimits(t *testing.T) {
	remediator, deploymentInfo, issue, clientset := createOOMTestFixture(t, "96Mi")

	err := remediator.Remediate(context.Background(), deploymentInfo, issue)
	require.NoError(t, err)

	// Fetch the patched deployment.
	deploy, err := clientset.AppsV1().Deployments("default").Get(
		context.Background(), "broken-app", metav1.GetOptions{})
	require.NoError(t, err)

	// 96Mi * 2.5 = 240Mi (251658240 bytes).
	newLimit := deploy.Spec.Template.Spec.Containers[0].Resources.Limits.Memory()
	assert.Equal(t, int64(251658240), newLimit.Value(), "expected 96Mi * 2.5 = 240Mi")
}

func TestRemediateOOM_RespectsMaxLimit(t *testing.T) {
	remediator, deploymentInfo, issue, clientset := createOOMTestFixture(t, "1Gi")

	// With max=2Gi (default) and multiplier 2.5, 1Gi*2.5=2.5Gi > 2Gi, so cap at 2Gi.
	err := remediator.Remediate(context.Background(), deploymentInfo, issue)
	require.NoError(t, err)

	deploy, err := clientset.AppsV1().Deployments("default").Get(
		context.Background(), "broken-app", metav1.GetOptions{})
	require.NoError(t, err)

	newLimit := deploy.Spec.Template.Spec.Containers[0].Resources.Limits.Memory()
	twoGi := resource.MustParse("2Gi")
	assert.Equal(t, twoGi.Value(), newLimit.Value(), "expected limit capped at 2Gi")
}

func TestRemediateOOM_AddsAnnotations(t *testing.T) {
	remediator, deploymentInfo, issue, clientset := createOOMTestFixture(t, "96Mi")

	err := remediator.Remediate(context.Background(), deploymentInfo, issue)
	require.NoError(t, err)

	deploy, err := clientset.AppsV1().Deployments("default").Get(
		context.Background(), "broken-app", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Contains(t, deploy.Annotations, "self-healing.kubeheal.io/last-remediation")
	assert.Contains(t, deploy.Annotations, "self-healing.kubeheal.io/memory-increase")
	assert.Contains(t, deploy.Annotations["self-healing.kubeheal.io/memory-increase"], "->")
}

func TestRemediateOOM_FallbackWhenNoDeployment(t *testing.T) {
	// Pod with no owner references — should fall back to pod delete.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orphan-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "web",
					Image: "test:latest",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("96Mi"),
						},
					},
				},
			},
		},
	}

	clientset := fake.NewSimpleClientset(pod)
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	remediator := NewManualRemediator(clientset, log)
	deploymentInfo := models.NewDeploymentInfo("default", "orphan-pod", "Pod", models.DeploymentMethodManual, 0.6)
	issue := &models.Issue{
		ID:           "issue-orphan",
		Type:         "OOMKilled",
		Severity:     "high",
		Namespace:    "default",
		ResourceType: "pod",
		ResourceName: "orphan-pod",
		Description:  "OOMKilled orphan pod",
		DetectedAt:   time.Now(),
	}

	err := remediator.Remediate(context.Background(), deploymentInfo, issue)
	assert.NoError(t, err)

	// Pod should be deleted (fallback behaviour).
	_, err = clientset.CoreV1().Pods("default").Get(context.Background(), "orphan-pod", metav1.GetOptions{})
	assert.Error(t, err, "orphan pod should have been deleted")
}

func TestResolveOwnerDeployment_TraversesPodToDeployment(t *testing.T) {
	_, _, _, clientset := createOOMTestFixture(t, "96Mi")
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	remediator := NewManualRemediator(clientset, log)

	pod, err := clientset.CoreV1().Pods("default").Get(
		context.Background(), "broken-app-pod-abc", metav1.GetOptions{})
	require.NoError(t, err)

	deploy, err := remediator.resolveOwnerDeployment(context.Background(), "default", pod)
	require.NoError(t, err)
	require.NotNil(t, deploy)
	assert.Equal(t, "broken-app", deploy.Name)
}
