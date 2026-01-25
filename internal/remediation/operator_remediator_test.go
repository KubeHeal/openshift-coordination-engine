package remediation

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

func TestNewOperatorRemediator(t *testing.T) {
	clientset := kubefake.NewSimpleClientset()
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	log := logrus.New()

	remediator := NewOperatorRemediator(clientset, dynamicClient, log)

	assert.NotNil(t, remediator)
	assert.Equal(t, "operator", remediator.Name())
}

func TestOperatorRemediator_CanRemediate(t *testing.T) {
	clientset := kubefake.NewSimpleClientset()
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	log := logrus.New()
	remediator := NewOperatorRemediator(clientset, dynamicClient, log)

	tests := []struct {
		name     string
		method   models.DeploymentMethod
		expected bool
	}{
		{
			name:     "Operator deployment",
			method:   models.DeploymentMethodOperator,
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
			name:     "Manual deployment",
			method:   models.DeploymentMethodManual,
			expected: false,
		},
		{
			name:     "Unknown deployment",
			method:   models.DeploymentMethodUnknown,
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

func TestOperatorRemediator_Name(t *testing.T) {
	clientset := kubefake.NewSimpleClientset()
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	log := logrus.New()
	remediator := NewOperatorRemediator(clientset, dynamicClient, log)

	assert.Equal(t, "operator", remediator.Name())
}

func TestOperatorRemediator_IsBuiltInKubernetesResource(t *testing.T) {
	clientset := kubefake.NewSimpleClientset()
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	remediator := NewOperatorRemediator(clientset, dynamicClient, log)

	tests := []struct {
		kind     string
		expected bool
	}{
		{"Pod", true},
		{"Deployment", true},
		{"ReplicaSet", true},
		{"StatefulSet", true},
		{"DaemonSet", true},
		{"Service", true},
		{"ConfigMap", true},
		{"Secret", true},
		{"MyCustomResource", false},
		{"Database", false},
		{"Application", false},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			result := remediator.isBuiltInKubernetesResource(tt.kind)
			assert.Equal(t, tt.expected, result, "isBuiltInKubernetesResource(%s) should be %v", tt.kind, tt.expected)
		})
	}
}

func TestOperatorRemediator_ExtractCRFromOwnerRefs(t *testing.T) {
	clientset := kubefake.NewSimpleClientset()
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	remediator := NewOperatorRemediator(clientset, dynamicClient, log)

	tests := []struct {
		name      string
		ownerRefs []metav1.OwnerReference
		expectNil bool
		expectCR  *CustomResourceInfo
	}{
		{
			name: "Custom Resource owner",
			ownerRefs: []metav1.OwnerReference{
				{
					Kind:       "Database",
					Name:       "my-database",
					APIVersion: "databases.example.com/v1",
				},
			},
			expectNil: false,
			expectCR: &CustomResourceInfo{
				Kind:       "Database",
				Name:       "my-database",
				APIVersion: "databases.example.com/v1",
				Group:      "databases.example.com",
				Version:    "v1",
				Resource:   "databases",
			},
		},
		{
			name: "Only built-in resources",
			ownerRefs: []metav1.OwnerReference{
				{
					Kind:       "ReplicaSet",
					Name:       "my-app-12345",
					APIVersion: "apps/v1",
				},
			},
			expectNil: true,
		},
		{
			name: "Mixed owners - should find CR",
			ownerRefs: []metav1.OwnerReference{
				{
					Kind:       "ReplicaSet",
					Name:       "my-app-12345",
					APIVersion: "apps/v1",
				},
				{
					Kind:       "Application",
					Name:       "my-app",
					APIVersion: "apps.example.com/v1beta1",
				},
			},
			expectNil: false,
			expectCR: &CustomResourceInfo{
				Kind:       "Application",
				Name:       "my-app",
				APIVersion: "apps.example.com/v1beta1",
				Group:      "apps.example.com",
				Version:    "v1beta1",
				Resource:   "applications",
			},
		},
		{
			name:      "No owner references",
			ownerRefs: []metav1.OwnerReference{},
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := remediator.extractCRFromOwnerRefs(tt.ownerRefs)
			if tt.expectNil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, tt.expectCR.Kind, result.Kind)
				assert.Equal(t, tt.expectCR.Name, result.Name)
				assert.Equal(t, tt.expectCR.APIVersion, result.APIVersion)
				assert.Equal(t, tt.expectCR.Group, result.Group)
				assert.Equal(t, tt.expectCR.Version, result.Version)
				assert.Equal(t, tt.expectCR.Resource, result.Resource)
			}
		})
	}
}

func TestParseAPIVersion(t *testing.T) {
	tests := []struct {
		apiVersion      string
		expectedGroup   string
		expectedVersion string
	}{
		{
			apiVersion:      "databases.example.com/v1",
			expectedGroup:   "databases.example.com",
			expectedVersion: "v1",
		},
		{
			apiVersion:      "apps.example.com/v1beta1",
			expectedGroup:   "apps.example.com",
			expectedVersion: "v1beta1",
		},
		{
			apiVersion:      "v1",
			expectedGroup:   "",
			expectedVersion: "v1",
		},
		{
			apiVersion:      "apps/v1",
			expectedGroup:   "apps",
			expectedVersion: "v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.apiVersion, func(t *testing.T) {
			group, version := parseAPIVersion(tt.apiVersion)
			assert.Equal(t, tt.expectedGroup, group)
			assert.Equal(t, tt.expectedVersion, version)
		})
	}
}

func TestInferResourceName(t *testing.T) {
	tests := []struct {
		kind     string
		expected string
	}{
		{"Database", "databases"},
		{"Application", "applications"},
		{"Service", "services"},
		{"MyApp", "myapps"},
		{"CRD", "crds"},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			result := inferResourceName(tt.kind)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Note: Full remediation testing with dynamic client and CR patching requires
// integration tests or more sophisticated mocking. These tests verify the structure
// and helper functions. Integration tests should:
//
// 1. Test findOwningCR with real Deployment and Pod resources
// 2. Test triggerReconciliation with mocked dynamic client
// 3. Test full Remediate workflow with operator-managed resources
// 4. Test error handling when CR not found
// 5. Test error handling when dynamic client operations fail
// 6. Test annotation patch format and content
// 7. Test with various CR types (Database, Application, etc.)
