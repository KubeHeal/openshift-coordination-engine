// Package rbac provides RBAC permission verification for the coordination engine.
package rbac

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Permission represents a Kubernetes permission to verify
type Permission struct {
	APIGroup  string
	Resource  string
	Verb      string
	Namespace string
	Name      string // Optional: specific resource name
}

// PermissionCheckResult holds the result of a permission check
type PermissionCheckResult struct {
	Permission Permission
	Allowed    bool
	Reason     string
	Error      error
}

// Verifier checks RBAC permissions for the ServiceAccount
type Verifier struct {
	clientset *kubernetes.Clientset
	namespace string
	log       *logrus.Logger
}

// NewVerifier creates a new RBAC verifier
func NewVerifier(clientset *kubernetes.Clientset, namespace string, log *logrus.Logger) *Verifier {
	return &Verifier{
		clientset: clientset,
		namespace: namespace,
		log:       log,
	}
}

// RequiredPermissions returns the list of permissions required by the coordination engine
func RequiredPermissions(namespace string) []Permission {
	return []Permission{
		// Core API resources - full access
		{APIGroup: "", Resource: "pods", Verb: "get", Namespace: namespace},
		{APIGroup: "", Resource: "pods", Verb: "list", Namespace: namespace},
		{APIGroup: "", Resource: "pods", Verb: "watch", Namespace: namespace},
		{APIGroup: "", Resource: "pods", Verb: "create", Namespace: namespace},
		{APIGroup: "", Resource: "pods", Verb: "update", Namespace: namespace},
		{APIGroup: "", Resource: "pods", Verb: "patch", Namespace: namespace},
		{APIGroup: "", Resource: "pods", Verb: "delete", Namespace: namespace},

		{APIGroup: "", Resource: "services", Verb: "get", Namespace: namespace},
		{APIGroup: "", Resource: "services", Verb: "list", Namespace: namespace},
		{APIGroup: "", Resource: "configmaps", Verb: "get", Namespace: namespace},
		{APIGroup: "", Resource: "secrets", Verb: "get", Namespace: namespace},
		{APIGroup: "", Resource: "events", Verb: "create", Namespace: namespace},

		// Core API resources - read-only
		{APIGroup: "", Resource: "namespaces", Verb: "get", Namespace: namespace},
		{APIGroup: "", Resource: "namespaces", Verb: "list", Namespace: namespace},
		{APIGroup: "", Resource: "nodes", Verb: "get", Namespace: namespace},
		{APIGroup: "", Resource: "nodes", Verb: "list", Namespace: namespace},

		// Apps API resources
		{APIGroup: "apps", Resource: "deployments", Verb: "get", Namespace: namespace},
		{APIGroup: "apps", Resource: "deployments", Verb: "list", Namespace: namespace},
		{APIGroup: "apps", Resource: "deployments", Verb: "watch", Namespace: namespace},
		{APIGroup: "apps", Resource: "deployments", Verb: "patch", Namespace: namespace},

		{APIGroup: "apps", Resource: "replicasets", Verb: "get", Namespace: namespace},
		{APIGroup: "apps", Resource: "replicasets", Verb: "list", Namespace: namespace},

		{APIGroup: "apps", Resource: "statefulsets", Verb: "get", Namespace: namespace},
		{APIGroup: "apps", Resource: "statefulsets", Verb: "list", Namespace: namespace},

		// Batch API resources
		{APIGroup: "batch", Resource: "jobs", Verb: "get", Namespace: namespace},
		{APIGroup: "batch", Resource: "jobs", Verb: "list", Namespace: namespace},

		// ArgoCD resources (deployment detection)
		{APIGroup: "argoproj.io", Resource: "applications", Verb: "get", Namespace: namespace},
		{APIGroup: "argoproj.io", Resource: "applications", Verb: "list", Namespace: namespace},
		{APIGroup: "argoproj.io", Resource: "applications", Verb: "watch", Namespace: namespace},

		// Machine configuration resources (read-only for MCO monitoring)
		{APIGroup: "machineconfiguration.openshift.io", Resource: "machineconfigs", Verb: "get", Namespace: namespace},
		{APIGroup: "machineconfiguration.openshift.io", Resource: "machineconfigs", Verb: "list", Namespace: namespace},
		{APIGroup: "machineconfiguration.openshift.io", Resource: "machineconfigpools", Verb: "get", Namespace: namespace},
		{APIGroup: "machineconfiguration.openshift.io", Resource: "machineconfigpools", Verb: "list", Namespace: namespace},

		// Monitoring resources
		{APIGroup: "monitoring.coreos.com", Resource: "servicemonitors", Verb: "get", Namespace: namespace},
		{APIGroup: "monitoring.coreos.com", Resource: "servicemonitors", Verb: "list", Namespace: namespace},
	}
}

// VerifyPermission checks if the current ServiceAccount has a specific permission
func (v *Verifier) VerifyPermission(ctx context.Context, perm *Permission) PermissionCheckResult {
	result := PermissionCheckResult{
		Permission: *perm,
	}

	// Create SelfSubjectAccessReview
	sar := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: perm.Namespace,
				Verb:      perm.Verb,
				Group:     perm.APIGroup,
				Resource:  perm.Resource,
			},
		},
	}

	if perm.Name != "" {
		sar.Spec.ResourceAttributes.Name = perm.Name
	}

	// Execute the access review
	response, err := v.clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
	if err != nil {
		result.Error = fmt.Errorf("failed to check permission: %w", err)
		result.Allowed = false
		return result
	}

	result.Allowed = response.Status.Allowed
	result.Reason = response.Status.Reason

	if !result.Allowed {
		v.log.WithFields(logrus.Fields{
			"api_group": perm.APIGroup,
			"resource":  perm.Resource,
			"verb":      perm.Verb,
			"namespace": perm.Namespace,
			"reason":    result.Reason,
		}).Warn("Permission check failed")
	}

	return result
}

// VerifyAllPermissions checks all required permissions and returns results
func (v *Verifier) VerifyAllPermissions(ctx context.Context) ([]PermissionCheckResult, error) {
	permissions := RequiredPermissions(v.namespace)
	results := make([]PermissionCheckResult, 0, len(permissions))

	v.log.WithField("total_checks", len(permissions)).Info("Starting RBAC permission verification")

	for i := range permissions {
		result := v.VerifyPermission(ctx, &permissions[i])
		results = append(results, result)
	}

	return results, nil
}

// CheckCriticalPermissions verifies critical permissions required for startup
// Returns error if any critical permission is missing
func (v *Verifier) CheckCriticalPermissions(ctx context.Context) error {
	criticalPerms := []Permission{
		// Must be able to read pods
		{APIGroup: "", Resource: "pods", Verb: "get", Namespace: v.namespace},
		{APIGroup: "", Resource: "pods", Verb: "list", Namespace: v.namespace},

		// Must be able to read deployments
		{APIGroup: "apps", Resource: "deployments", Verb: "get", Namespace: v.namespace},
		{APIGroup: "apps", Resource: "deployments", Verb: "list", Namespace: v.namespace},

		// Must be able to create events for logging
		{APIGroup: "", Resource: "events", Verb: "create", Namespace: v.namespace},
	}

	v.log.WithField("critical_checks", len(criticalPerms)).Info("Verifying critical RBAC permissions")

	failedPerms := []string{}
	for i := range criticalPerms {
		result := v.VerifyPermission(ctx, &criticalPerms[i])
		if !result.Allowed {
			failedPerms = append(failedPerms, fmt.Sprintf("%s/%s:%s", criticalPerms[i].APIGroup, criticalPerms[i].Resource, criticalPerms[i].Verb))
		}
	}

	if len(failedPerms) > 0 {
		return fmt.Errorf("missing critical permissions: %v", failedPerms)
	}

	v.log.Info("All critical RBAC permissions verified successfully")
	return nil
}

// GenerateReport creates a human-readable report of permission check results
func GenerateReport(results []PermissionCheckResult) string {
	report := "RBAC Permission Verification Report\n"
	report += "====================================\n\n"

	allowed := 0
	denied := 0
	errors := 0

	for _, result := range results {
		switch {
		case result.Error != nil:
			errors++
		case result.Allowed:
			allowed++
		default:
			denied++
		}
	}

	report += fmt.Sprintf("Total Permissions Checked: %d\n", len(results))
	report += fmt.Sprintf("Allowed: %d\n", allowed)
	report += fmt.Sprintf("Denied: %d\n", denied)
	report += fmt.Sprintf("Errors: %d\n\n", errors)

	if denied > 0 || errors > 0 {
		report += "Failed Permissions:\n"
		report += "-------------------\n"
		for _, result := range results {
			if !result.Allowed || result.Error != nil {
				apiGroup := result.Permission.APIGroup
				if apiGroup == "" {
					apiGroup = "core"
				}
				report += fmt.Sprintf("  ❌ %s/%s:%s (namespace: %s)\n",
					apiGroup,
					result.Permission.Resource,
					result.Permission.Verb,
					result.Permission.Namespace)
				if result.Error != nil {
					report += fmt.Sprintf("     Error: %v\n", result.Error)
				} else if result.Reason != "" {
					report += fmt.Sprintf("     Reason: %s\n", result.Reason)
				}
			}
		}
	} else {
		report += "✅ All permissions verified successfully!\n"
	}

	return report
}
