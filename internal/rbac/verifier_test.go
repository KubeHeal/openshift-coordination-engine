package rbac

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestRequiredPermissions(t *testing.T) {
	namespace := "test-namespace"
	perms := RequiredPermissions(namespace)

	// Verify we have permissions defined
	assert.NotEmpty(t, perms, "Should have required permissions defined")

	// Verify all permissions have the correct namespace
	for _, perm := range perms {
		assert.Equal(t, namespace, perm.Namespace, "All permissions should have the specified namespace")
	}

	// Verify critical permissions are included
	hasPodsGet := false
	hasDeploymentsGet := false
	hasArgoCD := false
	hasMCO := false

	for _, perm := range perms {
		if perm.Resource == "pods" && perm.Verb == "get" {
			hasPodsGet = true
		}
		if perm.Resource == "deployments" && perm.Verb == "get" {
			hasDeploymentsGet = true
		}
		if perm.Resource == "applications" && perm.APIGroup == "argoproj.io" {
			hasArgoCD = true
		}
		if perm.Resource == "machineconfigpools" && perm.APIGroup == "machineconfiguration.openshift.io" {
			hasMCO = true
		}
	}

	assert.True(t, hasPodsGet, "Should include pods:get permission")
	assert.True(t, hasDeploymentsGet, "Should include deployments:get permission")
	assert.True(t, hasArgoCD, "Should include ArgoCD applications permission")
	assert.True(t, hasMCO, "Should include MachineConfigPool permission")
}

func TestPermission_Structure(t *testing.T) {
	perm := Permission{
		APIGroup:  "apps",
		Resource:  "deployments",
		Verb:      "get",
		Namespace: "test-ns",
		Name:      "test-deployment",
	}

	assert.Equal(t, "apps", perm.APIGroup)
	assert.Equal(t, "deployments", perm.Resource)
	assert.Equal(t, "get", perm.Verb)
	assert.Equal(t, "test-ns", perm.Namespace)
	assert.Equal(t, "test-deployment", perm.Name)
}

func TestPermissionCheckResult_Structure(t *testing.T) {
	perm := Permission{
		APIGroup:  "",
		Resource:  "pods",
		Verb:      "get",
		Namespace: "default",
	}

	result := PermissionCheckResult{
		Permission: perm,
		Allowed:    true,
		Reason:     "test reason",
		Error:      nil,
	}

	assert.Equal(t, perm, result.Permission)
	assert.True(t, result.Allowed)
	assert.Equal(t, "test reason", result.Reason)
	assert.Nil(t, result.Error)
}

func TestGenerateReport_AllAllowed(t *testing.T) {
	results := []PermissionCheckResult{
		{
			Permission: Permission{APIGroup: "", Resource: "pods", Verb: "get", Namespace: "test"},
			Allowed:    true,
		},
		{
			Permission: Permission{APIGroup: "apps", Resource: "deployments", Verb: "get", Namespace: "test"},
			Allowed:    true,
		},
	}

	report := GenerateReport(results)

	assert.Contains(t, report, "Total Permissions Checked: 2")
	assert.Contains(t, report, "Allowed: 2")
	assert.Contains(t, report, "Denied: 0")
	assert.Contains(t, report, "✅ All permissions verified successfully!")
}

func TestGenerateReport_WithDenied(t *testing.T) {
	results := []PermissionCheckResult{
		{
			Permission: Permission{APIGroup: "", Resource: "pods", Verb: "get", Namespace: "test"},
			Allowed:    true,
		},
		{
			Permission: Permission{APIGroup: "apps", Resource: "deployments", Verb: "delete", Namespace: "test"},
			Allowed:    false,
			Reason:     "forbidden",
		},
	}

	report := GenerateReport(results)

	assert.Contains(t, report, "Total Permissions Checked: 2")
	assert.Contains(t, report, "Allowed: 1")
	assert.Contains(t, report, "Denied: 1")
	assert.Contains(t, report, "Failed Permissions:")
	assert.Contains(t, report, "apps/deployments:delete")
	assert.Contains(t, report, "Reason: forbidden")
}

func TestNewVerifier(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	verifier := NewVerifier(nil, "test-namespace", log)

	assert.NotNil(t, verifier)
	assert.Equal(t, "test-namespace", verifier.namespace)
	assert.Equal(t, log, verifier.log)
}
