package remediation

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
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
