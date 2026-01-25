package models

import (
	"fmt"
	"time"
)

// Layer represents a coordination layer in OpenShift
type Layer string

const (
	// LayerInfrastructure represents the infrastructure layer (nodes, MCO, operating system)
	LayerInfrastructure Layer = "infrastructure"

	// LayerPlatform represents the platform layer (OpenShift operators, SDN, core services)
	LayerPlatform Layer = "platform"

	// LayerApplication represents the application layer (user pods, deployments, services)
	LayerApplication Layer = "application"
)

// Priority returns the remediation priority (lower number = higher priority)
// Infrastructure must be fixed before platform, platform before application
func (l Layer) Priority() int {
	switch l {
	case LayerInfrastructure:
		return 0 // Highest priority
	case LayerPlatform:
		return 1 // Medium priority
	case LayerApplication:
		return 2 // Lowest priority
	default:
		return 99 // Unknown layer
	}
}

// String returns the string representation of the layer
func (l Layer) String() string {
	return string(l)
}

// Validate checks if the layer is valid
func (l Layer) Validate() error {
	switch l {
	case LayerInfrastructure, LayerPlatform, LayerApplication:
		return nil
	default:
		return fmt.Errorf("invalid layer: %s", l)
	}
}

// Resource represents an impacted Kubernetes resource
type Resource struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Issue     string `json:"issue"`
}

// String returns a string representation of the resource
func (r *Resource) String() string {
	if r.Namespace != "" {
		return fmt.Sprintf("%s/%s/%s", r.Kind, r.Namespace, r.Name)
	}
	return fmt.Sprintf("%s/%s", r.Kind, r.Name)
}

// LayeredIssue represents an issue that may span multiple layers
type LayeredIssue struct {
	ID                string               `json:"id"`
	Description       string               `json:"description"`
	AffectedLayers    []Layer              `json:"affected_layers"`
	RootCauseLayer    Layer                `json:"root_cause_layer"`
	ImpactedResources map[Layer][]Resource `json:"impacted_resources"`
	DetectedAt        time.Time            `json:"detected_at"`
	Severity          string               `json:"severity"` // critical, high, medium, low

	// ML-enhanced fields (Phase 6)
	MLPredictions     *MLLayerPredictions `json:"ml_predictions,omitempty"`
	LayerConfidence   map[Layer]float64   `json:"layer_confidence,omitempty"`
	DetectionMethod   string              `json:"detection_method"`             // "keyword", "ml_enhanced", "ml_only"
	HistoricalPattern string              `json:"historical_pattern,omitempty"` // e.g., "infrastructure_cascading_failure"
}

// NewLayeredIssue creates a new layered issue
func NewLayeredIssue(id, description string, rootCauseLayer Layer) *LayeredIssue {
	return &LayeredIssue{
		ID:                id,
		Description:       description,
		RootCauseLayer:    rootCauseLayer,
		AffectedLayers:    []Layer{rootCauseLayer},
		ImpactedResources: make(map[Layer][]Resource),
		DetectedAt:        time.Now(),
		Severity:          "medium",
		LayerConfidence:   make(map[Layer]float64),
		DetectionMethod:   "keyword", // Default to keyword-based detection
	}
}

// IsMultiLayer returns true if the issue affects multiple layers
func (li *LayeredIssue) IsMultiLayer() bool {
	return len(li.AffectedLayers) > 1
}

// RequiresInfrastructureRemediation returns true if infrastructure layer is affected
func (li *LayeredIssue) RequiresInfrastructureRemediation() bool {
	for _, layer := range li.AffectedLayers {
		if layer == LayerInfrastructure {
			return true
		}
	}
	return false
}

// RequiresPlatformRemediation returns true if platform layer is affected
func (li *LayeredIssue) RequiresPlatformRemediation() bool {
	for _, layer := range li.AffectedLayers {
		if layer == LayerPlatform {
			return true
		}
	}
	return false
}

// RequiresApplicationRemediation returns true if application layer is affected
func (li *LayeredIssue) RequiresApplicationRemediation() bool {
	for _, layer := range li.AffectedLayers {
		if layer == LayerApplication {
			return true
		}
	}
	return false
}

// AddAffectedLayer adds a layer to the affected layers list
func (li *LayeredIssue) AddAffectedLayer(layer Layer) {
	// Check if layer already exists
	for _, l := range li.AffectedLayers {
		if l == layer {
			return
		}
	}
	li.AffectedLayers = append(li.AffectedLayers, layer)
}

// AddImpactedResource adds a resource to the impacted resources for a layer
func (li *LayeredIssue) AddImpactedResource(layer Layer, resource Resource) {
	if li.ImpactedResources == nil {
		li.ImpactedResources = make(map[Layer][]Resource)
	}
	li.ImpactedResources[layer] = append(li.ImpactedResources[layer], resource)
}

// GetResourcesForLayer returns all impacted resources for a specific layer
func (li *LayeredIssue) GetResourcesForLayer(layer Layer) []Resource {
	if li.ImpactedResources == nil {
		return []Resource{}
	}
	return li.ImpactedResources[layer]
}

// GetLayersByPriority returns affected layers sorted by priority (infrastructure first)
func (li *LayeredIssue) GetLayersByPriority() []Layer {
	// Copy affected layers
	layers := make([]Layer, len(li.AffectedLayers))
	copy(layers, li.AffectedLayers)

	// Sort by priority (bubble sort for simplicity)
	for i := 0; i < len(layers); i++ {
		for j := i + 1; j < len(layers); j++ {
			if layers[i].Priority() > layers[j].Priority() {
				layers[i], layers[j] = layers[j], layers[i]
			}
		}
	}

	return layers
}

// Validate checks if the layered issue is valid
func (li *LayeredIssue) Validate() error {
	if li.ID == "" {
		return fmt.Errorf("layered issue ID is required")
	}

	if li.Description == "" {
		return fmt.Errorf("layered issue description is required")
	}

	if err := li.RootCauseLayer.Validate(); err != nil {
		return fmt.Errorf("invalid root cause layer: %w", err)
	}

	if len(li.AffectedLayers) == 0 {
		return fmt.Errorf("at least one affected layer is required")
	}

	for _, layer := range li.AffectedLayers {
		if err := layer.Validate(); err != nil {
			return fmt.Errorf("invalid affected layer: %w", err)
		}
	}

	return nil
}

// String returns a human-readable string representation
func (li *LayeredIssue) String() string {
	return fmt.Sprintf("LayeredIssue[%s]: %s (root: %s, layers: %d)",
		li.ID, li.Description, li.RootCauseLayer, len(li.AffectedLayers))
}

// MLLayerPredictions contains ML-based predictions for layer detection (Phase 6)
type MLLayerPredictions struct {
	Infrastructure      *LayerPrediction `json:"infrastructure,omitempty"`
	Platform            *LayerPrediction `json:"platform,omitempty"`
	Application         *LayerPrediction `json:"application,omitempty"`
	RootCauseSuggestion Layer            `json:"root_cause_suggestion"`
	Confidence          float64          `json:"confidence"`
	PredictedAt         time.Time        `json:"predicted_at"`
	AnalysisType        string           `json:"analysis_type,omitempty"` // "pattern", "anomaly", "prediction"
}

// LayerPrediction contains ML prediction details for a specific layer
type LayerPrediction struct {
	Affected    bool     `json:"affected"`
	Probability float64  `json:"probability"`        // 0.0 to 1.0
	Evidence    []string `json:"evidence,omitempty"` // Supporting evidence (e.g., "high_disk_usage", "node_pressure")
	IsRootCause bool     `json:"is_root_cause"`
}

// GetConfidence returns the confidence score for a layer (ML or keyword-based)
func (li *LayeredIssue) GetConfidence(layer Layer) float64 {
	if li.LayerConfidence == nil {
		return 0.70 // Default keyword-based confidence
	}

	if conf, exists := li.LayerConfidence[layer]; exists {
		return conf
	}

	return 0.0 // Layer not detected
}

// HasMLPredictions returns true if ML predictions are available
func (li *LayeredIssue) HasMLPredictions() bool {
	return li.MLPredictions != nil
}

// GetMLConfidence returns the overall ML prediction confidence
func (li *LayeredIssue) GetMLConfidence() float64 {
	if li.MLPredictions == nil {
		return 0.0
	}
	return li.MLPredictions.Confidence
}
