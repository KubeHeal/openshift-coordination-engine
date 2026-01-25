package remediation

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

func TestNewHelmRemediator(t *testing.T) {
	log := logrus.New()
	remediator := NewHelmRemediator(log)

	assert.NotNil(t, remediator)
	assert.Equal(t, "helm", remediator.Name())
	assert.Equal(t, 5*time.Minute, remediator.helmTimeout)
}

func TestHelmRemediator_CanRemediate(t *testing.T) {
	log := logrus.New()
	remediator := NewHelmRemediator(log)

	tests := []struct {
		name     string
		method   models.DeploymentMethod
		expected bool
	}{
		{
			name:     "Helm deployment",
			method:   models.DeploymentMethodHelm,
			expected: true,
		},
		{
			name:     "ArgoCD deployment",
			method:   models.DeploymentMethodArgoCD,
			expected: false,
		},
		{
			name:     "Manual deployment",
			method:   models.DeploymentMethodManual,
			expected: false,
		},
		{
			name:     "Operator deployment",
			method:   models.DeploymentMethodOperator,
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

func TestHelmRemediator_SetHelmTimeout(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel) // Reduce noise in tests
	remediator := NewHelmRemediator(log)

	// Default timeout
	assert.Equal(t, 5*time.Minute, remediator.helmTimeout)

	// Set custom timeout
	customTimeout := 10 * time.Minute
	remediator.SetHelmTimeout(customTimeout)
	assert.Equal(t, customTimeout, remediator.helmTimeout)
}

func TestHelmRemediator_Name(t *testing.T) {
	log := logrus.New()
	remediator := NewHelmRemediator(log)

	assert.Equal(t, "helm", remediator.Name())
}

// Note: Full remediation testing with Helm CLI execution requires integration tests
// or more sophisticated mocking of exec.Command. These tests verify the structure
// and interface compliance. Integration tests should:
//
// 1. Test getReleaseStatus with real/mocked Helm CLI
// 2. Test rollbackRelease command construction and execution
// 3. Test upgradeRelease command construction and execution
// 4. Test Remediate workflow for different release statuses:
//    - failed release -> rollback
//    - deployed release -> upgrade
//    - superseded release -> rollback
//    - pending-upgrade release -> rollback
// 5. Test error handling when Helm commands fail
// 6. Test timeout handling
// 7. Test missing release_name in deployment info
// 8. Test chart name extraction and fallback logic

func TestHelmRemediator_RemediateValidation(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	remediator := NewHelmRemediator(log)

	tests := []struct {
		name           string
		deploymentInfo *models.DeploymentInfo
		issue          *models.Issue
		expectError    bool
		errorContains  string
	}{
		{
			name: "Missing release_name",
			deploymentInfo: func() *models.DeploymentInfo {
				info := models.NewDeploymentInfo("default", "test-app", "Deployment", models.DeploymentMethodHelm, 0.9)
				// Don't set release_name detail
				return info
			}(),
			issue: &models.Issue{
				ID:           "test-issue",
				Type:         "CrashLoopBackOff",
				Namespace:    "default",
				ResourceName: "test-app",
				ResourceType: "Deployment",
			},
			expectError:   true,
			errorContains: "helm release name not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := remediator.Remediate(context.TODO(), tt.deploymentInfo, tt.issue)
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
