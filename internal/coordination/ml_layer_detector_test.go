package coordination

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/tosin2013/openshift-coordination-engine/internal/integrations"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// TestNewMLLayerDetector tests ML detector initialization
func TestNewMLLayerDetector(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create mock ML client
	mlClient := integrations.NewMLClient("http://localhost:8080", 30*time.Second, log)

	detector := NewMLLayerDetector(mlClient, log)

	assert.NotNil(t, detector)
	assert.NotNil(t, detector.baseDetector)
	assert.NotNil(t, detector.mlClient)
	assert.True(t, detector.enableML)
	assert.Equal(t, 5*time.Second, detector.timeout)
	assert.Equal(t, 0.75, detector.probabilityThreshold)
	assert.Equal(t, 0.85, detector.rootCauseConfidenceThreshold)
}

// TestNewMLLayerDetector_NilClient tests fallback to keyword-based detection
func TestNewMLLayerDetector_NilClient(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	detector := NewMLLayerDetector(nil, log)

	assert.NotNil(t, detector)
	assert.Nil(t, detector.mlClient)
	assert.False(t, detector.enableML)
}

// TestDetectLayersWithML_MLDisabled tests fallback when ML is disabled
func TestDetectLayersWithML_MLDisabled(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	detector := NewMLLayerDetector(nil, log)
	ctx := context.Background()

	resources := []models.Resource{
		{Kind: "Node", Name: "worker-1", Namespace: "", Issue: "DiskPressure"},
		{Kind: "Pod", Name: "my-app", Namespace: "default", Issue: "Evicted"},
	}

	layeredIssue := detector.DetectLayersWithML(ctx, "issue-001", "node disk pressure causing pod evictions", resources)

	// Should use keyword-based detection
	assert.NotNil(t, layeredIssue)
	assert.Equal(t, "keyword", layeredIssue.DetectionMethod)
	assert.Contains(t, layeredIssue.AffectedLayers, models.LayerInfrastructure)
	assert.Contains(t, layeredIssue.AffectedLayers, models.LayerApplication)
	assert.Equal(t, models.LayerInfrastructure, layeredIssue.RootCauseLayer)
	assert.Nil(t, layeredIssue.MLPredictions)
}

// TestDetermineMLRootCause tests root cause determination logic
func TestDetermineMLRootCause(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	mlClient := integrations.NewMLClient("http://localhost:8080", 30*time.Second, log)
	detector := NewMLLayerDetector(mlClient, log)

	tests := []struct {
		name         string
		infraProb    float64
		platformProb float64
		appProb      float64
		expected     models.Layer
	}{
		{
			name:         "Infrastructure highest",
			infraProb:    0.95,
			platformProb: 0.80,
			appProb:      0.70,
			expected:     models.LayerInfrastructure,
		},
		{
			name:         "Platform highest",
			infraProb:    0.70,
			platformProb: 0.90,
			appProb:      0.75,
			expected:     models.LayerPlatform,
		},
		{
			name:         "Application highest",
			infraProb:    0.65,
			platformProb: 0.70,
			appProb:      0.92,
			expected:     models.LayerApplication,
		},
		{
			name:         "All zero",
			infraProb:    0.0,
			platformProb: 0.0,
			appProb:      0.0,
			expected:     models.LayerApplication, // Default fallback
		},
		{
			name:         "Infrastructure only",
			infraProb:    0.85,
			platformProb: 0.0,
			appProb:      0.0,
			expected:     models.LayerInfrastructure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.determineMLRootCause(tt.infraProb, tt.platformProb, tt.appProb)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestPatternMatchesLayer tests pattern matching logic
func TestPatternMatchesLayer(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	mlClient := integrations.NewMLClient("http://localhost:8080", 30*time.Second, log)
	detector := NewMLLayerDetector(mlClient, log)

	tests := []struct {
		name     string
		pattern  integrations.Pattern
		layer    models.Layer
		expected bool
	}{
		{
			name: "Infrastructure pattern",
			pattern: integrations.Pattern{
				Type:        "infrastructure_failure",
				Description: "Node disk pressure causing cascading failures",
			},
			layer:    models.LayerInfrastructure,
			expected: true,
		},
		{
			name: "Platform pattern",
			pattern: integrations.Pattern{
				Type:        "operator_degradation",
				Description: "Network operator degraded",
			},
			layer:    models.LayerPlatform,
			expected: true,
		},
		{
			name: "Application pattern",
			pattern: integrations.Pattern{
				Type:        "pod_crash",
				Description: "Application pod crashloop",
			},
			layer:    models.LayerApplication,
			expected: true,
		},
		{
			name: "No match",
			pattern: integrations.Pattern{
				Type:        "unknown",
				Description: "Generic issue",
			},
			layer:    models.LayerInfrastructure,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.patternMatchesLayer(&tt.pattern, tt.layer)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestResourceMatchesLayer tests resource to layer mapping
func TestResourceMatchesLayer(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	mlClient := integrations.NewMLClient("http://localhost:8080", 30*time.Second, log)
	detector := NewMLLayerDetector(mlClient, log)

	tests := []struct {
		name     string
		resource models.Resource
		layer    models.Layer
		expected bool
	}{
		{
			name:     "Node -> Infrastructure",
			resource: models.Resource{Kind: "Node", Name: "worker-1"},
			layer:    models.LayerInfrastructure,
			expected: true,
		},
		{
			name:     "MachineConfig -> Infrastructure",
			resource: models.Resource{Kind: "MachineConfig", Name: "worker-config"},
			layer:    models.LayerInfrastructure,
			expected: true,
		},
		{
			name:     "ClusterOperator -> Platform",
			resource: models.Resource{Kind: "ClusterOperator", Name: "network"},
			layer:    models.LayerPlatform,
			expected: true,
		},
		{
			name:     "Pod -> Application",
			resource: models.Resource{Kind: "Pod", Name: "my-app"},
			layer:    models.LayerApplication,
			expected: true,
		},
		{
			name:     "Deployment -> Application",
			resource: models.Resource{Kind: "Deployment", Name: "my-app"},
			layer:    models.LayerApplication,
			expected: true,
		},
		{
			name:     "Pod -> Not Infrastructure",
			resource: models.Resource{Kind: "Pod", Name: "my-app"},
			layer:    models.LayerInfrastructure,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.resourceMatchesLayer(tt.resource, tt.layer)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestEnhanceWithMLPredictions tests ML prediction enhancement logic
func TestEnhanceWithMLPredictions(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	mlClient := integrations.NewMLClient("http://localhost:8080", 30*time.Second, log)
	detector := NewMLLayerDetector(mlClient, log)

	// Create base issue with keyword detection
	issue := models.NewLayeredIssue("issue-001", "test issue", models.LayerApplication)
	issue.LayerConfidence[models.LayerApplication] = 0.70 // Keyword confidence

	// Create ML predictions
	mlPred := &models.MLLayerPredictions{
		Infrastructure: &models.LayerPrediction{
			Affected:    true,
			Probability: 0.95,
			Evidence:    []string{"high_disk_usage", "node_pressure"},
			IsRootCause: true,
		},
		Application: &models.LayerPrediction{
			Affected:    true,
			Probability: 0.88,
			Evidence:    []string{"pod_eviction"},
			IsRootCause: false,
		},
		RootCauseSuggestion: models.LayerInfrastructure,
		Confidence:          0.92,
		PredictedAt:         time.Now(),
		AnalysisType:        "pattern",
	}

	// Enhance with ML predictions
	detector.enhanceWithMLPredictions(issue, mlPred)

	// Verify ML predictions added
	assert.NotNil(t, issue.MLPredictions)
	assert.Equal(t, mlPred, issue.MLPredictions)

	// Verify infrastructure layer added
	assert.Contains(t, issue.AffectedLayers, models.LayerInfrastructure)
	assert.Contains(t, issue.AffectedLayers, models.LayerApplication)

	// Verify confidence updated (should be max of keyword and ML)
	assert.Equal(t, 0.95, issue.LayerConfidence[models.LayerInfrastructure])
	assert.Equal(t, 0.88, issue.LayerConfidence[models.LayerApplication]) // Max of 0.70 keyword and 0.88 ML

	// Verify root cause updated (ML confidence 0.92 > threshold 0.85)
	assert.Equal(t, models.LayerInfrastructure, issue.RootCauseLayer)

	// Verify historical pattern set
	assert.Equal(t, "infrastructure_pattern", issue.HistoricalPattern)
}

// TestEnhanceWithMLPredictions_LowConfidence tests fallback when ML confidence is low
func TestEnhanceWithMLPredictions_LowConfidence(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	mlClient := integrations.NewMLClient("http://localhost:8080", 30*time.Second, log)
	detector := NewMLLayerDetector(mlClient, log)

	// Create base issue with keyword detection
	issue := models.NewLayeredIssue("issue-001", "test issue", models.LayerApplication)
	issue.LayerConfidence[models.LayerApplication] = 0.70

	// Create ML predictions with low confidence
	mlPred := &models.MLLayerPredictions{
		Infrastructure: &models.LayerPrediction{
			Affected:    true,
			Probability: 0.80,
			Evidence:    []string{"some_evidence"},
			IsRootCause: true,
		},
		RootCauseSuggestion: models.LayerInfrastructure,
		Confidence:          0.75, // Below threshold (0.85)
		PredictedAt:         time.Now(),
	}

	// Enhance with ML predictions
	detector.enhanceWithMLPredictions(issue, mlPred)

	// Verify ML predictions added
	assert.NotNil(t, issue.MLPredictions)

	// Verify root cause NOT updated (ML confidence 0.75 < threshold 0.85)
	assert.Equal(t, models.LayerApplication, issue.RootCauseLayer) // Still keyword-based
}

// TestHelperFunctions tests utility functions
func TestMaxFloat64(t *testing.T) {
	assert.Equal(t, 5.0, maxFloat64(3.0, 5.0))
	assert.Equal(t, 5.0, maxFloat64(5.0, 3.0))
	assert.Equal(t, 5.0, maxFloat64(5.0, 5.0))
}

func TestContainsAny(t *testing.T) {
	assert.True(t, containsAny("node disk pressure", []string{"node", "memory"}))
	assert.True(t, containsAny("operator degraded", []string{"operator"}))
	assert.False(t, containsAny("generic issue", []string{"node", "operator"}))
}

func TestContains(t *testing.T) {
	assert.True(t, contains("hello world", "world"))
	assert.True(t, contains("hello world", "hello"))
	assert.False(t, contains("hello world", "foo"))
}

func TestContainsLayer(t *testing.T) {
	assert.True(t, containsLayer("infrastructure failure", models.LayerInfrastructure))
	assert.True(t, containsLayer("platform degraded", models.LayerPlatform))
	assert.True(t, containsLayer("application pod crash", models.LayerApplication))
	assert.False(t, containsLayer("generic issue", models.LayerInfrastructure))
}
