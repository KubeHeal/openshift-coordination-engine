package coordination

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

func TestNewLayerDetector(t *testing.T) {
	log := logrus.New()
	detector := NewLayerDetector(log)

	assert.NotNil(t, detector)
	assert.NotNil(t, detector.log)
}

func TestDetectLayers_InfrastructureOnly(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detector := NewLayerDetector(log)
	ctx := context.Background()

	resources := []models.Resource{
		{
			Kind:      "Node",
			Name:      "worker-1",
			Namespace: "",
			Issue:     "NotReady",
		},
	}

	layeredIssue := detector.DetectLayers(ctx, "issue-001", "node not ready", resources)

	assert.NotNil(t, layeredIssue)
	assert.Equal(t, "issue-001", layeredIssue.ID)
	assert.Equal(t, models.LayerInfrastructure, layeredIssue.RootCauseLayer)
	assert.Contains(t, layeredIssue.AffectedLayers, models.LayerInfrastructure)
	assert.False(t, layeredIssue.IsMultiLayer())
}

func TestDetectLayers_PlatformOnly(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detector := NewLayerDetector(log)
	ctx := context.Background()

	resources := []models.Resource{
		{
			Kind:      "ClusterOperator",
			Name:      "network",
			Namespace: "",
			Issue:     "Degraded",
		},
	}

	layeredIssue := detector.DetectLayers(ctx, "issue-002", "cluster operator degraded", resources)

	assert.NotNil(t, layeredIssue)
	assert.Equal(t, models.LayerPlatform, layeredIssue.RootCauseLayer)
	assert.Contains(t, layeredIssue.AffectedLayers, models.LayerPlatform)
	assert.False(t, layeredIssue.IsMultiLayer())
}

func TestDetectLayers_ApplicationOnly(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detector := NewLayerDetector(log)
	ctx := context.Background()

	resources := []models.Resource{
		{
			Kind:      "Pod",
			Name:      "my-app-123",
			Namespace: "default",
			Issue:     "CrashLoopBackOff",
		},
	}

	layeredIssue := detector.DetectLayers(ctx, "issue-003", "pod crash loop", resources)

	assert.NotNil(t, layeredIssue)
	assert.Equal(t, models.LayerApplication, layeredIssue.RootCauseLayer)
	assert.Contains(t, layeredIssue.AffectedLayers, models.LayerApplication)
	assert.False(t, layeredIssue.IsMultiLayer())
}

func TestDetectLayers_MultiLayer(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detector := NewLayerDetector(log)
	ctx := context.Background()

	resources := []models.Resource{
		{
			Kind:      "Node",
			Name:      "worker-1",
			Namespace: "",
			Issue:     "DiskPressure",
		},
		{
			Kind:      "Pod",
			Name:      "my-app-123",
			Namespace: "default",
			Issue:     "Evicted",
		},
	}

	layeredIssue := detector.DetectLayers(ctx, "issue-004", "node disk pressure causing pod evictions", resources)

	assert.NotNil(t, layeredIssue)
	assert.True(t, layeredIssue.IsMultiLayer())
	assert.Contains(t, layeredIssue.AffectedLayers, models.LayerInfrastructure)
	assert.Contains(t, layeredIssue.AffectedLayers, models.LayerApplication)
	// Infrastructure is the root cause (lowest layer priority)
	assert.Equal(t, models.LayerInfrastructure, layeredIssue.RootCauseLayer)
}

func TestDetectLayers_AllThreeLayers(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detector := NewLayerDetector(log)
	ctx := context.Background()

	resources := []models.Resource{
		{
			Kind:      "MachineConfigPool",
			Name:      "worker",
			Namespace: "",
			Issue:     "Updating",
		},
		{
			Kind:      "ClusterOperator",
			Name:      "network",
			Namespace: "",
			Issue:     "Degraded",
		},
		{
			Kind:      "Deployment",
			Name:      "my-app",
			Namespace: "default",
			Issue:     "Not Ready",
		},
	}

	layeredIssue := detector.DetectLayers(ctx, "issue-005", "mco update causing network and app issues", resources)

	assert.NotNil(t, layeredIssue)
	assert.True(t, layeredIssue.IsMultiLayer())
	assert.Len(t, layeredIssue.AffectedLayers, 3)
	assert.Contains(t, layeredIssue.AffectedLayers, models.LayerInfrastructure)
	assert.Contains(t, layeredIssue.AffectedLayers, models.LayerPlatform)
	assert.Contains(t, layeredIssue.AffectedLayers, models.LayerApplication)
	// Infrastructure is the root cause
	assert.Equal(t, models.LayerInfrastructure, layeredIssue.RootCauseLayer)
}

func TestDetectFromIssue(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detector := NewLayerDetector(log)
	ctx := context.Background()

	issue := &models.Issue{
		ID:           "issue-006",
		Type:         "CrashLoopBackOff",
		Description:  "Pod is crash looping",
		Namespace:    "default",
		ResourceName: "my-app-123",
		ResourceType: "Pod",
	}

	layeredIssue := detector.DetectFromIssue(ctx, issue)

	assert.NotNil(t, layeredIssue)
	assert.Equal(t, "issue-006", layeredIssue.ID)
	assert.Contains(t, layeredIssue.AffectedLayers, models.LayerApplication)
}

func TestHasInfrastructureIssues_Keywords(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detector := NewLayerDetector(log)

	tests := []struct {
		description string
		expected    bool
	}{
		{"node is not ready", true},
		{"machineconfig updated", true},
		{"disk pressure on worker", true},
		{"pod is crashing", false},
		{"deployment failed", false},
	}

	for _, tt := range tests {
		result := detector.hasInfrastructureIssues(tt.description, nil)
		assert.Equal(t, tt.expected, result, "description: %s", tt.description)
	}
}

func TestHasPlatformIssues_Keywords(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detector := NewLayerDetector(log)

	tests := []struct {
		description string
		expected    bool
	}{
		{"operator is degraded", true},
		{"sdn networking issue", true},
		{"ingress router down", true},
		{"pod is crashing", false},
		{"node pressure", false},
	}

	for _, tt := range tests {
		result := detector.hasPlatformIssues(tt.description, nil)
		assert.Equal(t, tt.expected, result, "description: %s", tt.description)
	}
}

func TestHasApplicationIssues_Keywords(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detector := NewLayerDetector(log)

	tests := []struct {
		description string
		expected    bool
	}{
		{"pod crashloop", true},
		{"deployment not ready", true},
		{"imagepullbackoff error", true},
		{"node pressure", false},
		{"operator degraded", false},
	}

	for _, tt := range tests {
		result := detector.hasApplicationIssues(tt.description, nil)
		assert.Equal(t, tt.expected, result, "description: %s", tt.description)
	}
}

func TestResourceToLayer(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detector := NewLayerDetector(log)

	tests := []struct {
		resource      models.Resource
		expectedLayer models.Layer
	}{
		{models.Resource{Kind: "Node"}, models.LayerInfrastructure},
		{models.Resource{Kind: "MachineConfig"}, models.LayerInfrastructure},
		{models.Resource{Kind: "ClusterOperator"}, models.LayerPlatform},
		{models.Resource{Kind: "NetworkOperator"}, models.LayerPlatform},
		{models.Resource{Kind: "Pod"}, models.LayerApplication},
		{models.Resource{Kind: "Deployment"}, models.LayerApplication},
		{models.Resource{Kind: "StatefulSet"}, models.LayerApplication},
	}

	for _, tt := range tests {
		result := detector.resourceToLayer(tt.resource)
		assert.Equal(t, tt.expectedLayer, result, "resource kind: %s", tt.resource.Kind)
	}
}

func TestGetLayersByPriority(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	detector := NewLayerDetector(log)
	ctx := context.Background()

	resources := []models.Resource{
		{Kind: "Pod", Name: "app-pod", Namespace: "default"},
		{Kind: "ClusterOperator", Name: "network", Namespace: ""},
		{Kind: "Node", Name: "worker-1", Namespace: ""},
	}

	layeredIssue := detector.DetectLayers(ctx, "issue-007", "multi-layer issue", resources)
	orderedLayers := layeredIssue.GetLayersByPriority()

	// Should be Infrastructure -> Platform -> Application
	assert.Len(t, orderedLayers, 3)
	assert.Equal(t, models.LayerInfrastructure, orderedLayers[0])
	assert.Equal(t, models.LayerPlatform, orderedLayers[1])
	assert.Equal(t, models.LayerApplication, orderedLayers[2])
}
