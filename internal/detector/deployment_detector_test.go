package detector

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

func TestNewDeploymentDetector(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	clientset := fake.NewSimpleClientset()

	detector := NewDeploymentDetector(clientset, log)

	assert.NotNil(t, detector)
	assert.NotNil(t, detector.clientset)
	assert.NotNil(t, detector.log)
	assert.NotNil(t, detector.cache)
	assert.Equal(t, 5*time.Minute, detector.cache.ttl)
}

func TestDetectDeploymentMethod_ArgoCD(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create deployment with ArgoCD annotations
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			Annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "test-app:apps/Deployment:default/test-app",
			},
			Labels: map[string]string{
				"app.kubernetes.io/instance": "test-app",
			},
		},
	}

	clientset := fake.NewSimpleClientset(deployment)
	detector := NewDeploymentDetector(clientset, log)

	info, err := detector.DetectDeploymentMethod(context.Background(), "default", "test-app")

	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, models.DeploymentMethodArgoCD, info.Method)
	assert.Equal(t, 0.95, info.Confidence)
	assert.Equal(t, "test-app:apps/Deployment:default/test-app", info.GetDetail("tracking_id"))
	assert.Equal(t, "test-app", info.GetDetail("argocd_app"))
	assert.False(t, info.DetectedAt.IsZero())
}

func TestDetectDeploymentMethod_Helm(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create deployment with Helm annotations
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			Annotations: map[string]string{
				"meta.helm.sh/release-name":      "my-release",
				"meta.helm.sh/release-namespace": "default",
			},
			Labels: map[string]string{
				"helm.sh/chart": "my-chart-1.0.0",
			},
		},
	}

	clientset := fake.NewSimpleClientset(deployment)
	detector := NewDeploymentDetector(clientset, log)

	info, err := detector.DetectDeploymentMethod(context.Background(), "default", "test-app")

	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, models.DeploymentMethodHelm, info.Method)
	assert.Equal(t, 0.90, info.Confidence)
	assert.Equal(t, "my-release", info.GetDetail("release_name"))
	assert.Equal(t, "default", info.GetDetail("release_namespace"))
	assert.Equal(t, "my-chart-1.0.0", info.GetDetail("chart"))
}

func TestDetectDeploymentMethod_Operator(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create deployment with Operator labels
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "prometheus-operator",
				"app.kubernetes.io/name":       "test-app",
			},
		},
	}

	clientset := fake.NewSimpleClientset(deployment)
	detector := NewDeploymentDetector(clientset, log)

	info, err := detector.DetectDeploymentMethod(context.Background(), "default", "test-app")

	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, models.DeploymentMethodOperator, info.Method)
	assert.Equal(t, 0.80, info.Confidence)
	assert.Equal(t, "prometheus-operator", info.GetDetail("managed_by"))
	assert.Equal(t, "test-app", info.GetDetail("operator_name"))
}

func TestDetectDeploymentMethod_Manual(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create deployment with no special annotations/labels
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			Labels: map[string]string{
				"app":     "test-app",
				"version": "1.0.0",
			},
		},
	}

	clientset := fake.NewSimpleClientset(deployment)
	detector := NewDeploymentDetector(clientset, log)

	info, err := detector.DetectDeploymentMethod(context.Background(), "default", "test-app")

	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, models.DeploymentMethodManual, info.Method)
	assert.Equal(t, 0.60, info.Confidence)
	assert.Equal(t, "test-app", info.GetDetail("app"))
	assert.Equal(t, "1.0.0", info.GetDetail("version"))
}

func TestDetectDeploymentMethod_HelmNotOperator(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create deployment with Helm managed-by label (should not be detected as operator)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			Annotations: map[string]string{
				"meta.helm.sh/release-name": "my-release",
			},
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
			},
		},
	}

	clientset := fake.NewSimpleClientset(deployment)
	detector := NewDeploymentDetector(clientset, log)

	info, err := detector.DetectDeploymentMethod(context.Background(), "default", "test-app")

	require.NoError(t, err)
	assert.Equal(t, models.DeploymentMethodHelm, info.Method)
	assert.NotEqual(t, models.DeploymentMethodOperator, info.Method)
}

func TestDetectDeploymentMethod_Priority(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create deployment with both ArgoCD and Helm annotations
	// ArgoCD should take priority
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			Annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "test-app:apps/Deployment:default/test-app",
				"meta.helm.sh/release-name":      "my-release",
			},
		},
	}

	clientset := fake.NewSimpleClientset(deployment)
	detector := NewDeploymentDetector(clientset, log)

	info, err := detector.DetectDeploymentMethod(context.Background(), "default", "test-app")

	require.NoError(t, err)
	assert.Equal(t, models.DeploymentMethodArgoCD, info.Method, "ArgoCD should take priority over Helm")
	assert.Equal(t, 0.95, info.Confidence)
}

func TestDetectDeploymentMethod_Cache(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			Annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "test-app:apps/Deployment:default/test-app",
			},
		},
	}

	clientset := fake.NewSimpleClientset(deployment)
	detector := NewDeploymentDetector(clientset, log)

	// First call - should hit Kubernetes API
	info1, err1 := detector.DetectDeploymentMethod(context.Background(), "default", "test-app")
	require.NoError(t, err1)

	// Second call - should hit cache
	info2, err2 := detector.DetectDeploymentMethod(context.Background(), "default", "test-app")
	require.NoError(t, err2)

	// Both should return same result
	assert.Equal(t, info1.Method, info2.Method)
	assert.Equal(t, info1.Confidence, info2.Confidence)
}

func TestDetectDeploymentMethod_NotFound(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewSimpleClientset()
	detector := NewDeploymentDetector(clientset, log)

	_, err := detector.DetectDeploymentMethod(context.Background(), "default", "nonexistent")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get deployment")
}

func TestClearCache(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			Annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "test-app:apps/Deployment:default/test-app",
			},
		},
	}

	clientset := fake.NewSimpleClientset(deployment)
	detector := NewDeploymentDetector(clientset, log)

	// Populate cache
	_, err := detector.DetectDeploymentMethod(context.Background(), "default", "test-app")
	require.NoError(t, err)

	// Verify cache has entry
	stats1 := detector.GetCacheStats()
	assert.Equal(t, 1, stats1["total_entries"])

	// Clear cache
	detector.ClearCache()

	// Verify cache is empty
	stats2 := detector.GetCacheStats()
	assert.Equal(t, 0, stats2["total_entries"])
}

func TestGetCacheStats(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewSimpleClientset()
	detector := NewDeploymentDetector(clientset, log)

	stats := detector.GetCacheStats()

	assert.NotNil(t, stats)
	assert.Contains(t, stats, "total_entries")
	assert.Contains(t, stats, "valid_entries")
	assert.Contains(t, stats, "expired_entries")
	assert.Contains(t, stats, "ttl_seconds")
	assert.Equal(t, float64(300), stats["ttl_seconds"]) // 5 minutes = 300 seconds
}

func TestDetectStatefulSetMethod(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts",
			Namespace: "default",
			Annotations: map[string]string{
				"meta.helm.sh/release-name": "my-release",
			},
		},
	}

	clientset := fake.NewSimpleClientset(sts)
	detector := NewDeploymentDetector(clientset, log)

	info, err := detector.DetectStatefulSetMethod(context.Background(), "default", "test-sts")

	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, models.DeploymentMethodHelm, info.Method)
	assert.Equal(t, "StatefulSet", info.ResourceKind)
}

func TestDetectDaemonSetMethod(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ds",
			Namespace: "kube-system",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "system-operator",
			},
		},
	}

	clientset := fake.NewSimpleClientset(ds)
	detector := NewDeploymentDetector(clientset, log)

	info, err := detector.DetectDaemonSetMethod(context.Background(), "kube-system", "test-ds")

	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, models.DeploymentMethodOperator, info.Method)
	assert.Equal(t, "DaemonSet", info.ResourceKind)
}
