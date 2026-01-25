package remediation

import (
	"context"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// Remediator performs remediation for a specific deployment method
type Remediator interface {
	// Remediate executes remediation logic
	Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo, issue *models.Issue) error

	// CanRemediate returns true if this remediator can handle the deployment
	CanRemediate(deploymentInfo *models.DeploymentInfo) bool

	// Name returns the remediator's name
	Name() string
}

// RemediationResult contains the outcome of remediation
//
//nolint:revive // intentional naming for clarity in external package usage
type RemediationResult struct {
	Status   string `json:"status"` // "success", "failed", "recommendation"
	Method   string `json:"method"` // Remediation method used
	Message  string `json:"message,omitempty"`
	Duration string `json:"duration,omitempty"`
}
