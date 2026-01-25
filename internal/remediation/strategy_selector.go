package remediation

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// StrategySelector routes remediation to the appropriate remediator based on deployment method
type StrategySelector struct {
	remediators        []Remediator
	fallbackRemediator Remediator
	log                *logrus.Logger
}

// NewStrategySelector creates a new strategy selector
func NewStrategySelector(log *logrus.Logger) *StrategySelector {
	return &StrategySelector{
		remediators: make([]Remediator, 0),
		log:         log,
	}
}

// RegisterRemediator registers a remediator with the selector
func (ss *StrategySelector) RegisterRemediator(remediator Remediator) {
	ss.remediators = append(ss.remediators, remediator)
	ss.log.WithField("remediator", remediator.Name()).Info("Remediator registered")
}

// SetFallbackRemediator sets the fallback remediator (usually manual)
func (ss *StrategySelector) SetFallbackRemediator(remediator Remediator) {
	ss.fallbackRemediator = remediator
	ss.log.WithField("remediator", remediator.Name()).Info("Fallback remediator set")
}

// SelectRemediator chooses the appropriate remediator based on deployment info
func (ss *StrategySelector) SelectRemediator(deploymentInfo *models.DeploymentInfo) Remediator {
	ss.log.WithFields(logrus.Fields{
		"method":     deploymentInfo.Method,
		"confidence": deploymentInfo.Confidence,
		"namespace":  deploymentInfo.Namespace,
		"resource":   deploymentInfo.ResourceName,
	}).Debug("Selecting remediation strategy")

	// Try each registered remediator in order
	for _, remediator := range ss.remediators {
		if remediator.CanRemediate(deploymentInfo) {
			ss.log.WithFields(logrus.Fields{
				"remediator": remediator.Name(),
				"method":     deploymentInfo.Method,
			}).Info("Remediator selected")

			// Record strategy selection metrics
			RecordStrategySelection(remediator.Name(), string(deploymentInfo.Method), true)

			return remediator
		} else {
			// Record non-selection
			RecordStrategySelection(remediator.Name(), string(deploymentInfo.Method), false)
		}
	}

	// Fall back to default remediator
	if ss.fallbackRemediator != nil {
		ss.log.WithFields(logrus.Fields{
			"remediator": ss.fallbackRemediator.Name(),
			"method":     deploymentInfo.Method,
		}).Warn("No specific remediator matched, using fallback")

		// Record fallback strategy selection
		RecordStrategySelection(ss.fallbackRemediator.Name(), string(deploymentInfo.Method), true)

		return ss.fallbackRemediator
	}

	// This shouldn't happen if fallback is set properly
	ss.log.Error("No remediator found and no fallback set")
	return nil
}

// Remediate executes remediation using the selected strategy
func (ss *StrategySelector) Remediate(ctx context.Context, deploymentInfo *models.DeploymentInfo, issue *models.Issue) error {
	remediator := ss.SelectRemediator(deploymentInfo)
	if remediator == nil {
		return fmt.Errorf("no remediator available for deployment method: %s", deploymentInfo.Method)
	}

	ss.log.WithFields(logrus.Fields{
		"issue_id":   issue.ID,
		"issue_type": issue.Type,
		"remediator": remediator.Name(),
		"namespace":  issue.Namespace,
		"resource":   issue.ResourceName,
	}).Info("Starting remediation with selected strategy")

	err := remediator.Remediate(ctx, deploymentInfo, issue)
	if err != nil {
		ss.log.WithError(err).WithFields(logrus.Fields{
			"remediator": remediator.Name(),
			"issue_id":   issue.ID,
		}).Error("Remediation failed")
		return fmt.Errorf("%s remediation failed: %w", remediator.Name(), err)
	}

	ss.log.WithFields(logrus.Fields{
		"remediator": remediator.Name(),
		"issue_id":   issue.ID,
	}).Info("Remediation completed successfully")

	return nil
}

// CanRemediate returns true if any remediator can handle the deployment
func (ss *StrategySelector) CanRemediate(deploymentInfo *models.DeploymentInfo) bool {
	return ss.SelectRemediator(deploymentInfo) != nil
}

// Name returns the selector name
func (ss *StrategySelector) Name() string {
	return "strategy-selector"
}

// GetRegisteredRemediators returns all registered remediators
func (ss *StrategySelector) GetRegisteredRemediators() []string {
	names := make([]string, 0, len(ss.remediators))
	for _, r := range ss.remediators {
		names = append(names, r.Name())
	}
	if ss.fallbackRemediator != nil {
		names = append(names, ss.fallbackRemediator.Name()+" (fallback)")
	}
	return names
}
