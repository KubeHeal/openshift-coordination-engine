// Package remediation implements deployment-specific remediation strategies.
package remediation

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/internal/integrations"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// ArgoCDRemediator handles ArgoCD-managed application remediation
type ArgoCDRemediator struct {
	argocdClient *integrations.ArgoCDClient
	log          *logrus.Logger
	syncTimeout  time.Duration
}

// NewArgoCDRemediator creates a new ArgoCD remediator
func NewArgoCDRemediator(argocdClient *integrations.ArgoCDClient, log *logrus.Logger) *ArgoCDRemediator {
	return &ArgoCDRemediator{
		argocdClient: argocdClient,
		log:          log,
		syncTimeout:  5 * time.Minute, // Default 5 minute timeout
	}
}

// Remediate performs ArgoCD-based remediation by triggering sync
func (ar *ArgoCDRemediator) Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo, issue *models.Issue) error {
	ar.log.WithFields(logrus.Fields{
		"namespace":  issue.Namespace,
		"resource":   issue.ResourceName,
		"issue_type": issue.Type,
		"method":     "argocd",
	}).Info("Starting ArgoCD remediation")

	// Find ArgoCD application managing this resource
	appName := deploymentInfo.GetDetail("argocd_app")
	if appName == "" {
		// Try to find application by resource
		app, err := ar.argocdClient.FindApplicationByResource(ctx, issue.Namespace, issue.ResourceName, issue.ResourceType)
		if err != nil {
			return fmt.Errorf("failed to find ArgoCD application: %w", err)
		}
		appName = app.Metadata.Name
	}

	ar.log.WithField("app_name", appName).Info("Found ArgoCD application")

	// Get current application status
	app, err := ar.argocdClient.GetApplication(ctx, appName)
	if err != nil {
		return fmt.Errorf("failed to get application status: %w", err)
	}

	ar.log.WithFields(logrus.Fields{
		"app_name":      appName,
		"sync_status":   app.Status.Sync.Status,
		"health_status": app.Status.Health.Status,
	}).Info("Current application status")

	// Check if application is already synced and healthy
	if app.Status.Sync.Status == "Synced" && app.Status.Health.Status == "Healthy" {
		ar.log.Info("Application is already synced and healthy, triggering refresh sync")
	}

	// Trigger ArgoCD sync (respects GitOps workflow)
	syncReq := &integrations.SyncRequest{
		Prune:  false, // Don't prune by default - safer
		DryRun: false,
	}

	// For CrashLoopBackOff or similar, trigger a full sync
	if issue.Type == "CrashLoopBackOff" || issue.Type == "pod_crash_loop" {
		ar.log.Info("Triggering full ArgoCD sync for crash loop issue")
		if err := ar.argocdClient.SyncApplication(ctx, appName, syncReq); err != nil {
			return fmt.Errorf("failed to trigger sync: %w", err)
		}
	} else {
		// For other issues, try targeted sync
		ar.log.Info("Triggering ArgoCD sync")
		if err := ar.argocdClient.SyncApplication(ctx, appName, syncReq); err != nil {
			return fmt.Errorf("failed to trigger sync: %w", err)
		}
	}

	// Wait for sync to complete
	ar.log.WithField("timeout", ar.syncTimeout).Info("Waiting for ArgoCD sync completion")
	if err := ar.argocdClient.WaitForSync(ctx, appName, ar.syncTimeout); err != nil {
		return fmt.Errorf("sync did not complete successfully: %w", err)
	}

	ar.log.WithField("app_name", appName).Info("ArgoCD remediation completed successfully")
	return nil
}

// CanRemediate returns true if deployment is ArgoCD-managed
func (ar *ArgoCDRemediator) CanRemediate(deploymentInfo *models.DeploymentInfo) bool {
	return deploymentInfo.Method == models.DeploymentMethodArgoCD || deploymentInfo.IsGitOpsManaged()
}

// Name returns the remediator name
func (ar *ArgoCDRemediator) Name() string {
	return "argocd"
}

// SetSyncTimeout allows customizing the sync timeout
func (ar *ArgoCDRemediator) SetSyncTimeout(timeout time.Duration) {
	ar.syncTimeout = timeout
	ar.log.WithField("timeout", timeout).Debug("ArgoCD sync timeout updated")
}
