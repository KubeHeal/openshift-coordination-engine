//go:build integration

package integration

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tosin2013/openshift-coordination-engine/internal/coordination"
	"github.com/tosin2013/openshift-coordination-engine/internal/detector"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// TestDeploymentDetector_Basic tests basic deployment detector functionality
func (s *IntegrationTestSuite) TestDeploymentDetector_Basic() {
	if s.clientset == nil {
		s.T().Skip("Clientset not initialized")
	}

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detectorSvc := detector.NewDeploymentDetector(s.clientset, log)

	s.Require().NotNil(detectorSvc, "Deployment detector should be created")
	s.T().Log("Deployment detector initialized successfully")
}

// TestDeploymentDetector_ArgoCD tests ArgoCD deployment detection
func (s *IntegrationTestSuite) TestDeploymentDetector_ArgoCD() {
	if s.clientset == nil {
		s.T().Skip("Clientset not initialized")
	}

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detectorSvc := detector.NewDeploymentDetector(s.clientset, log)

	// Create test namespace
	namespace := "test-argocd-" + time.Now().Format("20060102150405")
	_, err := s.clientset.CoreV1().Namespaces().Create(s.ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}, metav1.CreateOptions{})
	s.Require().NoError(err)
	defer s.cleanupNamespace(namespace)

	// Create deployment with ArgoCD tracking annotation
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-test-app",
			Namespace: namespace,
			Annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "test-app:apps/Deployment:default/argocd-test-app",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}

	_, err = s.clientset.AppsV1().Deployments(namespace).Create(s.ctx, deployment, metav1.CreateOptions{})
	s.Require().NoError(err)

	// Wait a bit for deployment to be created
	time.Sleep(2 * time.Second)

	// Detect deployment method
	info, err := detectorSvc.DetectDeploymentMethod(s.ctx, namespace, "argocd-test-app")
	s.Require().NoError(err)
	s.Require().NotNil(info)

	// Verify it was detected as ArgoCD
	s.Equal(models.DeploymentMethodArgoCD, info.Method, "Should detect ArgoCD deployment")
	s.T().Logf("Detected deployment method: %s with confidence: %.2f", info.Method, info.Confidence)
}

// TestLayerDetector_Basic tests basic layer detector functionality
func (s *IntegrationTestSuite) TestLayerDetector_Basic() {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	layerDetector := coordination.NewLayerDetector(log)

	s.Require().NotNil(layerDetector, "Layer detector should be created")

	// Test infrastructure layer detection
	resources := []models.Resource{
		{
			Kind:      "Node",
			Namespace: "",
			Name:      "test-node",
		},
	}

	layeredIssue := layerDetector.DetectLayers(s.ctx, "test-node-issue", "Node is not ready", resources)
	s.Require().NotNil(layeredIssue)
	s.Equal(models.LayerInfrastructure, layeredIssue.RootCauseLayer, "Node issues should be infrastructure layer")
	s.T().Logf("Detected layer: %s", layeredIssue.RootCauseLayer)
}

// TestLayerDetector_ApplicationLayer tests application layer detection
func (s *IntegrationTestSuite) TestLayerDetector_ApplicationLayer() {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	layerDetector := coordination.NewLayerDetector(log)

	// Test pod issue detection (application layer)
	resources := []models.Resource{
		{
			Kind:      "Pod",
			Namespace: "default",
			Name:      "test-app",
		},
	}

	layeredIssue := layerDetector.DetectLayers(s.ctx, "test-pod-issue", "Pod is in CrashLoopBackOff", resources)
	s.Require().NotNil(layeredIssue)
	s.Equal(models.LayerApplication, layeredIssue.RootCauseLayer, "Pod issues should be application layer")
	s.T().Logf("Detected application layer issue with %d affected layers", len(layeredIssue.AffectedLayers))
}

// cleanupNamespace deletes a namespace
func (s *IntegrationTestSuite) cleanupNamespace(namespace string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := s.clientset.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
	if err != nil {
		s.T().Logf("Warning: failed to cleanup namespace %s: %v", namespace, err)
	}
}

// int32Ptr returns a pointer to an int32
func int32Ptr(i int32) *int32 {
	return &i
}
