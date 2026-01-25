package remediation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// HelmRemediator handles Helm-managed application remediation
type HelmRemediator struct {
	log         *logrus.Logger
	helmTimeout time.Duration
}

// HelmStatus represents the Helm release status JSON response
type HelmStatus struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Info      struct {
		Status        string    `json:"status"`
		FirstDeployed time.Time `json:"first_deployed"`
		LastDeployed  time.Time `json:"last_deployed"`
		Description   string    `json:"description"`
	} `json:"info"`
	Version int `json:"version"`
}

// NewHelmRemediator creates a new Helm remediator
func NewHelmRemediator(log *logrus.Logger) *HelmRemediator {
	return &HelmRemediator{
		log:         log,
		helmTimeout: 5 * time.Minute, // Default 5 minute timeout for Helm operations
	}
}

// Remediate triggers Helm upgrade or rollback based on release status
func (hr *HelmRemediator) Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo, issue *models.Issue) error {
	// Extract Helm release information from deployment info
	releaseName := deploymentInfo.GetDetail("release_name")
	if releaseName == "" {
		return fmt.Errorf("helm release name not found in deployment info")
	}

	releaseNamespace := deploymentInfo.GetDetail("release_namespace")
	if releaseNamespace == "" {
		// Fall back to resource namespace
		releaseNamespace = deploymentInfo.Namespace
	}

	hr.log.WithFields(logrus.Fields{
		"release":    releaseName,
		"namespace":  releaseNamespace,
		"issue_type": issue.Type,
		"resource":   issue.ResourceName,
		"method":     "helm",
	}).Info("Starting Helm remediation")

	// Check release status
	releaseStatus, err := hr.getReleaseStatus(ctx, releaseName, releaseNamespace)
	if err != nil {
		return fmt.Errorf("failed to get release status: %w", err)
	}

	hr.log.WithFields(logrus.Fields{
		"release": releaseName,
		"status":  releaseStatus.Info.Status,
		"version": releaseStatus.Version,
	}).Info("Current Helm release status")

	// Determine remediation strategy based on status
	status := releaseStatus.Info.Status

	// If release is in failed state, rollback to previous version
	if status == "failed" || status == "superseded" || status == "pending-upgrade" {
		hr.log.WithFields(logrus.Fields{
			"release": releaseName,
			"status":  status,
		}).Info("Rolling back Helm release")

		if err := hr.rollbackRelease(ctx, releaseName, releaseNamespace); err != nil {
			return fmt.Errorf("helm rollback failed: %w", err)
		}

		hr.log.WithField("release", releaseName).Info("Helm rollback completed successfully")
		return nil
	}

	// If release is deployed but having issues, trigger upgrade with --reuse-values
	// This re-applies the configuration and can fix transient issues
	hr.log.WithFields(logrus.Fields{
		"release":    releaseName,
		"issue_type": issue.Type,
	}).Info("Triggering Helm upgrade to remediate issue")

	if err := hr.upgradeRelease(ctx, releaseName, releaseNamespace, deploymentInfo); err != nil {
		// If upgrade fails, attempt rollback as safety measure
		hr.log.WithError(err).Warn("Helm upgrade failed, attempting rollback")
		if rollbackErr := hr.rollbackRelease(ctx, releaseName, releaseNamespace); rollbackErr != nil {
			return fmt.Errorf("helm upgrade failed: %w, and rollback also failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("helm upgrade failed (rolled back): %w", err)
	}

	hr.log.WithField("release", releaseName).Info("Helm remediation completed successfully")
	return nil
}

// CanRemediate returns true if deployment is Helm-managed
func (hr *HelmRemediator) CanRemediate(deploymentInfo *models.DeploymentInfo) bool {
	return deploymentInfo.Method == models.DeploymentMethodHelm || deploymentInfo.IsHelmManaged()
}

// Name returns the remediator name
func (hr *HelmRemediator) Name() string {
	return "helm"
}

// SetHelmTimeout allows customizing the Helm operation timeout
func (hr *HelmRemediator) SetHelmTimeout(timeout time.Duration) {
	hr.helmTimeout = timeout
	hr.log.WithField("timeout", timeout).Debug("Helm timeout updated")
}

// getReleaseStatus queries Helm release status and returns parsed status
func (hr *HelmRemediator) getReleaseStatus(ctx context.Context, releaseName, namespace string) (*HelmStatus, error) {
	// Create context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "helm", "status", releaseName,
		"-n", namespace,
		"-o", "json",
	)

	output, err := cmd.Output()
	if err != nil {
		// Check if release exists
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			hr.log.WithFields(logrus.Fields{
				"release":  releaseName,
				"stderr":   string(exitErr.Stderr),
				"exitcode": exitErr.ExitCode(),
			}).Error("Helm status command failed")
		}
		return nil, fmt.Errorf("helm status command failed: %w", err)
	}

	// Parse JSON output
	var status HelmStatus
	if err := json.Unmarshal(output, &status); err != nil {
		hr.log.WithFields(logrus.Fields{
			"release": releaseName,
			"output":  string(output),
		}).Error("Failed to parse Helm status JSON")
		return nil, fmt.Errorf("failed to parse helm status output: %w", err)
	}

	return &status, nil
}

// rollbackRelease rolls back Helm release to previous revision
func (hr *HelmRemediator) rollbackRelease(ctx context.Context, releaseName, namespace string) error {
	// Create context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, hr.helmTimeout)
	defer cancel()

	// #nosec G204 -- helm command with controlled inputs from deployment metadata
	cmd := exec.CommandContext(timeoutCtx, "helm", "rollback", releaseName,
		"-n", namespace,
		"--wait",
		"--timeout", hr.helmTimeout.String(),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		hr.log.WithFields(logrus.Fields{
			"release": releaseName,
			"output":  string(output),
		}).Error("Helm rollback failed")
		return fmt.Errorf("helm rollback failed: %w, output: %s", err, string(output))
	}

	hr.log.WithFields(logrus.Fields{
		"release": releaseName,
		"output":  string(output),
	}).Info("Helm rollback completed")

	return nil
}

// upgradeRelease triggers Helm upgrade with --reuse-values and --atomic
func (hr *HelmRemediator) upgradeRelease(ctx context.Context, releaseName, namespace string, deploymentInfo *models.DeploymentInfo) error {
	// Create context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, hr.helmTimeout)
	defer cancel()

	// Get chart name from deployment info
	chart := deploymentInfo.GetDetail("chart")
	if chart == "" {
		// If chart not available, use release name as fallback
		chart = releaseName
		hr.log.WithField("release", releaseName).Warn("Chart name not found, using release name")
	}

	// Build helm upgrade command
	// --reuse-values: Reuse the last release's values
	// --atomic: If upgrade fails, rollback automatically
	// --wait: Wait for resources to be ready
	// #nosec G204 -- helm command with controlled inputs from deployment metadata
	cmd := exec.CommandContext(timeoutCtx, "helm", "upgrade", releaseName, chart,
		"-n", namespace,
		"--reuse-values",
		"--atomic",
		"--wait",
		"--timeout", hr.helmTimeout.String(),
	)

	hr.log.WithFields(logrus.Fields{
		"release": releaseName,
		"chart":   chart,
		"command": cmd.String(),
	}).Debug("Executing Helm upgrade")

	output, err := cmd.CombinedOutput()
	if err != nil {
		hr.log.WithFields(logrus.Fields{
			"release": releaseName,
			"chart":   chart,
			"output":  string(output),
		}).Error("Helm upgrade failed")
		return fmt.Errorf("helm upgrade failed: %w, output: %s", err, string(output))
	}

	hr.log.WithFields(logrus.Fields{
		"release": releaseName,
		"chart":   chart,
		"output":  string(output),
	}).Info("Helm upgrade completed")

	return nil
}
