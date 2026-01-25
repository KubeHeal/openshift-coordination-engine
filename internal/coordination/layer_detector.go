package coordination

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// LayerDetector detects which layers are affected by an issue
type LayerDetector struct {
	log *logrus.Logger
}

// NewLayerDetector creates a new layer detector
func NewLayerDetector(log *logrus.Logger) *LayerDetector {
	return &LayerDetector{
		log: log,
	}
}

// DetectLayers analyzes an issue and determines affected layers
// Returns a LayeredIssue with affected layers, root cause, and grouped resources
func (ld *LayerDetector) DetectLayers(ctx context.Context, issueID, issueDescription string, resources []models.Resource) *models.LayeredIssue {
	ld.log.WithFields(logrus.Fields{
		"issue_id":    issueID,
		"description": issueDescription,
		"resources":   len(resources),
	}).Debug("Detecting affected layers")

	// Create layered issue with initial layer based on heuristics
	rootCauseLayer := ld.determineInitialLayer(issueDescription, resources)
	layeredIssue := models.NewLayeredIssue(issueID, issueDescription, rootCauseLayer)

	// Check each layer
	if ld.hasInfrastructureIssues(issueDescription, resources) {
		layeredIssue.AddAffectedLayer(models.LayerInfrastructure)
	}

	if ld.hasPlatformIssues(issueDescription, resources) {
		layeredIssue.AddAffectedLayer(models.LayerPlatform)
	}

	if ld.hasApplicationIssues(issueDescription, resources) {
		layeredIssue.AddAffectedLayer(models.LayerApplication)
	}

	// Refine root cause based on all affected layers
	layeredIssue.RootCauseLayer = ld.determineRootCause(layeredIssue.AffectedLayers)

	// Group resources by layer
	ld.groupAndAddResources(layeredIssue, resources)

	ld.log.WithFields(logrus.Fields{
		"issue_id":        issueID,
		"affected_layers": layeredIssue.AffectedLayers,
		"root_cause":      layeredIssue.RootCauseLayer,
		"is_multi_layer":  layeredIssue.IsMultiLayer(),
	}).Info("Layer detection complete")

	// Record metrics
	RecordLayerDetection(layeredIssue.RootCauseLayer, layeredIssue.IsMultiLayer())
	if layeredIssue.IsMultiLayer() {
		RecordMultiLayerIssue(len(layeredIssue.AffectedLayers), layeredIssue.RootCauseLayer)
	}

	return layeredIssue
}

// determineInitialLayer provides an initial guess for the root cause layer
func (ld *LayerDetector) determineInitialLayer(description string, resources []models.Resource) models.Layer {
	// Quick heuristic based on keywords
	if ld.hasInfrastructureIssues(description, resources) {
		return models.LayerInfrastructure
	}
	if ld.hasPlatformIssues(description, resources) {
		return models.LayerPlatform
	}
	return models.LayerApplication
}

// hasInfrastructureIssues checks if issue involves infrastructure layer
func (ld *LayerDetector) hasInfrastructureIssues(description string, resources []models.Resource) bool {
	keywords := []string{
		"node", "machineconfig", "mco", "kubelet",
		"memory pressure", "disk pressure", "pid pressure",
		"os", "kernel", "systemd", "coreos",
		"notready", "networkunavailable",
	}

	desc := strings.ToLower(description)
	for _, keyword := range keywords {
		if strings.Contains(desc, keyword) {
			ld.log.WithField("keyword", keyword).Debug("Infrastructure keyword matched")
			return true
		}
	}

	// Check resource kinds
	for _, resource := range resources {
		if resource.Kind == "Node" ||
			resource.Kind == "MachineConfig" ||
			resource.Kind == "MachineConfigPool" {
			ld.log.WithField("kind", resource.Kind).Debug("Infrastructure resource detected")
			return true
		}
	}

	return false
}

// hasPlatformIssues checks if issue involves platform layer
func (ld *LayerDetector) hasPlatformIssues(description string, resources []models.Resource) bool {
	keywords := []string{
		"operator", "sdn", "networking", "ovn",
		"storage", "csi", "ingress", "router",
		"api server", "controller manager", "scheduler",
		"clusteroperator", "degraded", "progressing",
	}

	desc := strings.ToLower(description)
	for _, keyword := range keywords {
		if strings.Contains(desc, keyword) {
			ld.log.WithField("keyword", keyword).Debug("Platform keyword matched")
			return true
		}
	}

	// Check resource kinds
	for _, resource := range resources {
		if resource.Kind == "ClusterOperator" ||
			strings.Contains(resource.Kind, "Operator") ||
			resource.Kind == "NetworkPolicy" {
			ld.log.WithField("kind", resource.Kind).Debug("Platform resource detected")
			return true
		}
	}

	return false
}

// hasApplicationIssues checks if issue involves application layer
func (ld *LayerDetector) hasApplicationIssues(description string, resources []models.Resource) bool {
	keywords := []string{
		"pod", "deployment", "replicaset", "statefulset",
		"crashloop", "imagepull", "container", "oom",
		"application", "service", "endpoint",
		"crashloopbackoff", "imagepullbackoff",
	}

	desc := strings.ToLower(description)
	for _, keyword := range keywords {
		if strings.Contains(desc, keyword) {
			ld.log.WithField("keyword", keyword).Debug("Application keyword matched")
			return true
		}
	}

	// Check resource kinds
	for _, resource := range resources {
		if resource.Kind == "Pod" ||
			resource.Kind == "Deployment" ||
			resource.Kind == "StatefulSet" ||
			resource.Kind == "DaemonSet" ||
			resource.Kind == "ReplicaSet" {
			ld.log.WithField("kind", resource.Kind).Debug("Application resource detected")
			return true
		}
	}

	return false
}

// determineRootCause identifies the root cause layer
// Heuristic: Infrastructure > Platform > Application (lowest layer is usually root cause)
func (ld *LayerDetector) determineRootCause(affectedLayers []models.Layer) models.Layer {
	if len(affectedLayers) == 0 {
		return models.LayerApplication // Default fallback
	}

	// Infrastructure issues are usually root causes
	for _, layer := range affectedLayers {
		if layer == models.LayerInfrastructure {
			return models.LayerInfrastructure
		}
	}

	// Then platform issues
	for _, layer := range affectedLayers {
		if layer == models.LayerPlatform {
			return models.LayerPlatform
		}
	}

	// Default to application
	return models.LayerApplication
}

// groupAndAddResources organizes resources by their layer and adds them to the issue
func (ld *LayerDetector) groupAndAddResources(layeredIssue *models.LayeredIssue, resources []models.Resource) {
	for _, resource := range resources {
		layer := ld.resourceToLayer(resource)
		layeredIssue.AddImpactedResource(layer, resource)
	}
}

// resourceToLayer maps a resource kind to its layer
func (ld *LayerDetector) resourceToLayer(resource models.Resource) models.Layer {
	switch resource.Kind {
	case "Node", "MachineConfig", "MachineConfigPool":
		return models.LayerInfrastructure
	case "ClusterOperator", "NetworkPolicy":
		return models.LayerPlatform
	default:
		// Check if it's an operator
		if strings.Contains(resource.Kind, "Operator") {
			return models.LayerPlatform
		}
		return models.LayerApplication
	}
}

// DetectFromIssue converts a simple Issue to a LayeredIssue
// This provides backward compatibility with existing Issue model
func (ld *LayerDetector) DetectFromIssue(ctx context.Context, issue *models.Issue) *models.LayeredIssue {
	// Create resource from issue
	resource := models.Resource{
		Kind:      issue.ResourceType,
		Name:      issue.ResourceName,
		Namespace: issue.Namespace,
		Issue:     fmt.Sprintf("%s: %s", issue.Type, issue.Description),
	}

	return ld.DetectLayers(ctx, issue.ID, issue.Description, []models.Resource{resource})
}
