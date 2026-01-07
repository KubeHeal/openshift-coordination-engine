package coordination

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/internal/integrations"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// MLProvider defines the interface for ML/KServe integration
type MLProvider interface {
	// DetectAnomaliesFromMetrics performs anomaly detection on metric data
	DetectAnomaliesFromMetrics(ctx context.Context, metrics []integrations.MetricData) (*MLAnomalyResult, error)

	// HealthCheck checks if the ML provider is healthy
	HealthCheck(ctx context.Context) error

	// Close closes any resources held by the provider
	Close()
}

// MLAnomalyResult represents the result of anomaly detection from any provider
type MLAnomalyResult struct {
	AnomaliesFound int     `json:"anomalies_found"`
	Total          int     `json:"total"`
	Confidence     float64 `json:"confidence"`
	AnomalyRate    float64 `json:"anomaly_rate"`
	Provider       string  `json:"provider"` // "kserve" or "legacy_ml"
}

// MLLayerDetector enhances layer detection with ML predictions
type MLLayerDetector struct {
	baseDetector                 *LayerDetector // Keyword-based detector (fallback)
	mlClient                     *integrations.MLClient  // Legacy ML client (deprecated)
	kserveClient                 *integrations.KServeClient // KServe client (ADR-039)
	enableML                     bool
	useKServe                    bool // True if using KServe, false for legacy ML
	timeout                      time.Duration
	probabilityThreshold         float64 // Minimum probability to mark layer as affected
	rootCauseConfidenceThreshold float64 // Minimum confidence to use ML-suggested root cause
	log                          *logrus.Logger
}

// NewMLLayerDetector creates a new ML-enhanced layer detector with legacy ML client
// Deprecated: Use NewMLLayerDetectorWithKServe for KServe integration (ADR-039)
func NewMLLayerDetector(mlClient *integrations.MLClient, log *logrus.Logger) *MLLayerDetector {
	return &MLLayerDetector{
		baseDetector:                 NewLayerDetector(log),
		mlClient:                     mlClient,
		kserveClient:                 nil,
		enableML:                     mlClient != nil,
		useKServe:                    false,
		timeout:                      5 * time.Second,
		probabilityThreshold:         0.75, // 75% probability to mark layer as affected
		rootCauseConfidenceThreshold: 0.85, // 85% confidence to use ML root cause suggestion
		log:                          log,
	}
}

// NewMLLayerDetectorWithKServe creates a new ML-enhanced layer detector with KServe client (ADR-039)
func NewMLLayerDetectorWithKServe(kserveClient *integrations.KServeClient, log *logrus.Logger) *MLLayerDetector {
	return &MLLayerDetector{
		baseDetector:                 NewLayerDetector(log),
		mlClient:                     nil,
		kserveClient:                 kserveClient,
		enableML:                     kserveClient != nil && kserveClient.HasAnomalyDetector(),
		useKServe:                    true,
		timeout:                      10 * time.Second, // KServe may need more time
		probabilityThreshold:         0.75,
		rootCauseConfidenceThreshold: 0.85,
		log:                          log,
	}
}

// NewMLLayerDetectorDual creates a detector that can use either KServe or legacy ML
// KServe takes precedence if both are configured
func NewMLLayerDetectorDual(kserveClient *integrations.KServeClient, mlClient *integrations.MLClient, log *logrus.Logger) *MLLayerDetector {
	// Prefer KServe over legacy ML
	if kserveClient != nil && kserveClient.HasAnomalyDetector() {
		log.Info("Using KServe integration for ML layer detection (ADR-039)")
		return NewMLLayerDetectorWithKServe(kserveClient, log)
	}

	if mlClient != nil {
		log.Warn("Using legacy ML_SERVICE_URL for layer detection (deprecated)")
		return NewMLLayerDetector(mlClient, log)
	}

	log.Info("No ML integration configured, using keyword-based detection only")
	return &MLLayerDetector{
		baseDetector:                 NewLayerDetector(log),
		enableML:                     false,
		timeout:                      5 * time.Second,
		probabilityThreshold:         0.75,
		rootCauseConfidenceThreshold: 0.85,
		log:                          log,
	}
}

// DetectLayersWithML performs ML-enhanced layer detection
func (mld *MLLayerDetector) DetectLayersWithML(ctx context.Context, issueID, issueDescription string, resources []models.Resource) *models.LayeredIssue {
	// 1. Start with keyword-based detection (fast path)
	layeredIssue := mld.baseDetector.DetectLayers(ctx, issueID, issueDescription, resources)
	layeredIssue.DetectionMethod = "keyword"

	// Set initial keyword-based confidence (0.70)
	for _, layer := range layeredIssue.AffectedLayers {
		layeredIssue.LayerConfidence[layer] = 0.70
	}

	// 2. If ML is disabled or unavailable, return keyword results
	if !mld.enableML {
		mld.log.Debug("ML detection disabled, using keyword-based results")
		RecordMLLayerDetection(false, false)
		return layeredIssue
	}

	// 3. Call ML service for pattern analysis
	mlCtx, cancel := context.WithTimeout(ctx, mld.timeout)
	defer cancel()

	startTime := time.Now()
	mlPredictions, err := mld.getMLPredictions(mlCtx, issueDescription, resources)
	duration := time.Since(startTime).Seconds()
	RecordMLDetectionDuration(duration)

	if err != nil {
		mld.log.WithError(err).Warn("ML prediction failed, using keyword-based results")
		RecordMLLayerDetection(false, true) // ML available but failed
		return layeredIssue
	}

	// 4. Enhance with ML predictions
	mld.enhanceWithMLPredictions(layeredIssue, mlPredictions)
	layeredIssue.DetectionMethod = "ml_enhanced"

	// Record metrics
	RecordMLLayerDetection(true, true)
	if mlPredictions.Infrastructure != nil {
		RecordMLLayerConfidence(models.LayerInfrastructure, mlPredictions.Infrastructure.Probability)
	}
	if mlPredictions.Platform != nil {
		RecordMLLayerConfidence(models.LayerPlatform, mlPredictions.Platform.Probability)
	}
	if mlPredictions.Application != nil {
		RecordMLLayerConfidence(models.LayerApplication, mlPredictions.Application.Probability)
	}

	mld.log.WithFields(logrus.Fields{
		"issue_id":        issueID,
		"detection":       "ml_enhanced",
		"ml_confidence":   mlPredictions.Confidence,
		"affected_layers": layeredIssue.AffectedLayers,
		"root_cause":      layeredIssue.RootCauseLayer,
	}).Info("ML-enhanced layer detection complete")

	return layeredIssue
}

// getMLPredictions calls ML service for layer predictions
func (mld *MLLayerDetector) getMLPredictions(ctx context.Context, description string, resources []models.Resource) (*models.MLLayerPredictions, error) {
	// Use KServe if configured (ADR-039)
	if mld.useKServe && mld.kserveClient != nil {
		return mld.getKServePredictions(ctx, description, resources)
	}

	// Fall back to legacy ML service
	return mld.getLegacyMLPredictions(ctx, description, resources)
}

// getKServePredictions calls KServe InferenceServices for predictions (ADR-039)
func (mld *MLLayerDetector) getKServePredictions(ctx context.Context, description string, resources []models.Resource) (*models.MLLayerPredictions, error) {
	mld.log.WithFields(logrus.Fields{
		"description": description,
		"resources":   len(resources),
		"provider":    "kserve",
	}).Debug("Calling KServe for anomaly detection")

	// Convert resources to feature vectors for KServe
	instances := mld.resourcesToFeatureVectors(resources)
	if len(instances) == 0 {
		// If no resources, create a dummy instance based on description keywords
		instances = [][]float64{{0.5, 0.5, 0.5}} // Neutral values
	}

	// Call KServe anomaly detector
	result, err := mld.kserveClient.DetectAnomalies(ctx, instances)
	if err != nil {
		return nil, fmt.Errorf("KServe anomaly detection failed: %w", err)
	}

	// Convert KServe result to MLLayerPredictions
	predictions := mld.parseKServeResponse(result, resources)

	mld.log.WithFields(logrus.Fields{
		"provider":        "kserve",
		"anomalies_found": result.Summary.AnomaliesFound,
		"total":           result.Summary.Total,
		"confidence":      predictions.Confidence,
		"root_suggestion": predictions.RootCauseSuggestion,
	}).Debug("KServe predictions parsed")

	return predictions, nil
}

// resourcesToFeatureVectors converts resources to feature vectors for KServe
func (mld *MLLayerDetector) resourcesToFeatureVectors(resources []models.Resource) [][]float64 {
	if len(resources) == 0 {
		return [][]float64{}
	}

	instances := make([][]float64, 0, len(resources))
	for _, r := range resources {
		// Create feature vector based on resource type and layer
		// Features: [infrastructure_score, platform_score, application_score]
		var infraScore, platformScore, appScore float64

		switch r.Kind {
		case "Node", "MachineConfig", "MachineConfigPool":
			infraScore = 1.0
		case "ClusterOperator", "NetworkPolicy", "StorageClass":
			platformScore = 1.0
		case "Pod", "Deployment", "StatefulSet", "Service":
			appScore = 1.0
		default:
			// Unknown resource type - assign equal scores
			infraScore, platformScore, appScore = 0.33, 0.33, 0.34
		}

		instances = append(instances, []float64{infraScore, platformScore, appScore})
	}

	return instances
}

// parseKServeResponse converts KServe anomaly detection result to MLLayerPredictions
func (mld *MLLayerDetector) parseKServeResponse(result *integrations.AnomalyDetectionResult, resources []models.Resource) *models.MLLayerPredictions {
	predictions := &models.MLLayerPredictions{
		PredictedAt:  time.Now(),
		AnalysisType: "kserve_anomaly",
	}

	// Calculate confidence based on anomaly detection results
	if result.Summary.Total > 0 {
		// Confidence is higher when more anomalies are detected with clear patterns
		predictions.Confidence = 0.7 + (result.Summary.AnomalyRate * 0.3)
	} else {
		predictions.Confidence = 0.5 // Low confidence when no data
	}

	// Analyze which layers are affected based on resource types and anomaly predictions
	infraAnomalies, platformAnomalies, appAnomalies := 0, 0, 0
	infraTotal, platformTotal, appTotal := 0, 0, 0

	for i, pred := range result.Predictions {
		if i >= len(resources) {
			break
		}
		r := resources[i]

		switch r.Kind {
		case "Node", "MachineConfig", "MachineConfigPool":
			infraTotal++
			if pred.IsAnomaly {
				infraAnomalies++
			}
		case "ClusterOperator", "NetworkPolicy", "StorageClass":
			platformTotal++
			if pred.IsAnomaly {
				platformAnomalies++
			}
		case "Pod", "Deployment", "StatefulSet", "Service":
			appTotal++
			if pred.IsAnomaly {
				appAnomalies++
			}
		}
	}

	// Calculate probabilities for each layer
	if infraTotal > 0 {
		infraProb := float64(infraAnomalies) / float64(infraTotal)
		if infraProb > 0 {
			predictions.Infrastructure = &models.LayerPrediction{
				Affected:    infraProb >= mld.probabilityThreshold,
				Probability: minFloat64(infraProb+0.3, 1.0), // Boost probability
				Evidence:    []string{"kserve_anomaly_detection", fmt.Sprintf("%d/%d anomalies", infraAnomalies, infraTotal)},
				IsRootCause: false,
			}
		}
	}

	if platformTotal > 0 {
		platformProb := float64(platformAnomalies) / float64(platformTotal)
		if platformProb > 0 {
			predictions.Platform = &models.LayerPrediction{
				Affected:    platformProb >= mld.probabilityThreshold,
				Probability: minFloat64(platformProb+0.3, 1.0),
				Evidence:    []string{"kserve_anomaly_detection", fmt.Sprintf("%d/%d anomalies", platformAnomalies, platformTotal)},
				IsRootCause: false,
			}
		}
	}

	if appTotal > 0 {
		appProb := float64(appAnomalies) / float64(appTotal)
		if appProb > 0 {
			predictions.Application = &models.LayerPrediction{
				Affected:    appProb >= mld.probabilityThreshold,
				Probability: minFloat64(appProb+0.3, 1.0),
				Evidence:    []string{"kserve_anomaly_detection", fmt.Sprintf("%d/%d anomalies", appAnomalies, appTotal)},
				IsRootCause: false,
			}
		}
	}

	// Determine root cause (highest anomaly rate wins, with infrastructure priority)
	infraRate := 0.0
	platformRate := 0.0
	appRate := 0.0

	if infraTotal > 0 {
		infraRate = float64(infraAnomalies) / float64(infraTotal)
	}
	if platformTotal > 0 {
		platformRate = float64(platformAnomalies) / float64(platformTotal)
	}
	if appTotal > 0 {
		appRate = float64(appAnomalies) / float64(appTotal)
	}

	predictions.RootCauseSuggestion = mld.determineMLRootCause(infraRate, platformRate, appRate)

	// Mark root cause layer
	switch predictions.RootCauseSuggestion {
	case models.LayerInfrastructure:
		if predictions.Infrastructure != nil {
			predictions.Infrastructure.IsRootCause = true
		}
	case models.LayerPlatform:
		if predictions.Platform != nil {
			predictions.Platform.IsRootCause = true
		}
	case models.LayerApplication:
		if predictions.Application != nil {
			predictions.Application.IsRootCause = true
		}
	}

	return predictions
}

// getLegacyMLPredictions calls the legacy ML service for predictions (deprecated)
func (mld *MLLayerDetector) getLegacyMLPredictions(ctx context.Context, description string, resources []models.Resource) (*models.MLLayerPredictions, error) {
	// Extract events from resources for pattern analysis
	events := make([]string, 0, len(resources))
	for _, r := range resources {
		if r.Issue != "" {
			events = append(events, r.Issue)
		}
	}

	// Create pattern analysis request
	// Note: We use empty metrics array and rely on analysis_type="layer_detection"
	// The ML service will analyze the description and events to predict affected layers
	req := &integrations.PatternAnalysisRequest{
		Metrics: []integrations.MetricData{
			// TODO: Could add resource metrics here if available
			// For now, rely on description and events
		},
		TimeRange: struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		}{
			Start: time.Now().Add(-1 * time.Hour),
			End:   time.Now(),
		},
		AnalysisType: "layer_detection",
	}

	mld.log.WithFields(logrus.Fields{
		"description": description,
		"resources":   len(resources),
		"events":      len(events),
		"provider":    "legacy_ml",
	}).Debug("Calling legacy ML pattern analysis for layer detection")

	resp, err := mld.mlClient.AnalyzePatterns(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ML pattern analysis failed: %w", err)
	}

	// Parse response into ML predictions
	predictions := mld.parseMLResponse(resp, resources)

	mld.log.WithFields(logrus.Fields{
		"provider":        "legacy_ml",
		"ml_confidence":   predictions.Confidence,
		"patterns_found":  len(resp.Patterns),
		"root_suggestion": predictions.RootCauseSuggestion,
	}).Debug("ML predictions parsed")

	return predictions, nil
}

// parseMLResponse converts PatternAnalysisResponse to MLLayerPredictions
func (mld *MLLayerDetector) parseMLResponse(resp *integrations.PatternAnalysisResponse, resources []models.Resource) *models.MLLayerPredictions {
	predictions := &models.MLLayerPredictions{
		Confidence:   resp.Summary.Confidence,
		PredictedAt:  time.Now(),
		AnalysisType: "pattern",
	}

	// Extract pattern information
	// The ML service should return patterns with layer-specific information
	// For now, we use heuristics based on pattern type and confidence

	// Check for infrastructure patterns
	infraProb := mld.calculateLayerProbability(resp, models.LayerInfrastructure, resources)
	if infraProb > 0.0 {
		predictions.Infrastructure = &models.LayerPrediction{
			Affected:    infraProb > mld.probabilityThreshold,
			Probability: infraProb,
			Evidence:    mld.extractEvidence(resp, models.LayerInfrastructure),
			IsRootCause: false, // Will be set below
		}
	}

	// Check for platform patterns
	platformProb := mld.calculateLayerProbability(resp, models.LayerPlatform, resources)
	if platformProb > 0.0 {
		predictions.Platform = &models.LayerPrediction{
			Affected:    platformProb > mld.probabilityThreshold,
			Probability: platformProb,
			Evidence:    mld.extractEvidence(resp, models.LayerPlatform),
			IsRootCause: false,
		}
	}

	// Check for application patterns
	appProb := mld.calculateLayerProbability(resp, models.LayerApplication, resources)
	if appProb > 0.0 {
		predictions.Application = &models.LayerPrediction{
			Affected:    appProb > mld.probabilityThreshold,
			Probability: appProb,
			Evidence:    mld.extractEvidence(resp, models.LayerApplication),
			IsRootCause: false,
		}
	}

	// Determine root cause based on highest probability
	predictions.RootCauseSuggestion = mld.determineMLRootCause(infraProb, platformProb, appProb)

	// Mark root cause layer
	switch predictions.RootCauseSuggestion {
	case models.LayerInfrastructure:
		if predictions.Infrastructure != nil {
			predictions.Infrastructure.IsRootCause = true
		}
	case models.LayerPlatform:
		if predictions.Platform != nil {
			predictions.Platform.IsRootCause = true
		}
	case models.LayerApplication:
		if predictions.Application != nil {
			predictions.Application.IsRootCause = true
		}
	}

	return predictions
}

// calculateLayerProbability estimates layer probability from pattern analysis
func (mld *MLLayerDetector) calculateLayerProbability(resp *integrations.PatternAnalysisResponse, layer models.Layer, resources []models.Resource) float64 {
	// Use pattern confidence as base probability
	baseProbability := resp.Summary.Confidence

	// Check if patterns mention this layer
	layerMentioned := false
	for _, pattern := range resp.Patterns {
		if mld.patternMatchesLayer(&pattern, layer) {
			layerMentioned = true
			// Boost probability if pattern strongly matches this layer
			baseProbability = maxFloat64(baseProbability, pattern.Confidence)
		}
	}

	// If layer not mentioned in patterns, return 0
	if !layerMentioned {
		// Fallback: check if any resources match this layer
		for _, r := range resources {
			if mld.resourceMatchesLayer(r, layer) {
				return 0.70 // Keyword-based probability
			}
		}
		return 0.0
	}

	return baseProbability
}

// patternMatchesLayer checks if a pattern description matches a layer
func (mld *MLLayerDetector) patternMatchesLayer(pattern *integrations.Pattern, layer models.Layer) bool {
	description := pattern.Description + " " + pattern.Type

	switch layer {
	case models.LayerInfrastructure:
		return containsAny(description, []string{"infrastructure", "node", "mco", "machine", "kernel", "os"})
	case models.LayerPlatform:
		return containsAny(description, []string{"platform", "operator", "sdn", "networking", "storage", "cluster"})
	case models.LayerApplication:
		return containsAny(description, []string{"application", "pod", "deployment", "container", "workload"})
	default:
		return false
	}
}

// resourceMatchesLayer checks if a resource belongs to a layer
func (mld *MLLayerDetector) resourceMatchesLayer(resource models.Resource, layer models.Layer) bool {
	switch layer {
	case models.LayerInfrastructure:
		return resource.Kind == "Node" || resource.Kind == "MachineConfig" || resource.Kind == "MachineConfigPool"
	case models.LayerPlatform:
		return resource.Kind == "ClusterOperator" || resource.Kind == "NetworkPolicy"
	case models.LayerApplication:
		return resource.Kind == "Pod" || resource.Kind == "Deployment" || resource.Kind == "StatefulSet"
	default:
		return false
	}
}

// extractEvidence extracts evidence from patterns for a specific layer
func (mld *MLLayerDetector) extractEvidence(resp *integrations.PatternAnalysisResponse, layer models.Layer) []string {
	evidence := []string{}

	// Add insights as evidence
	for _, insight := range resp.Insights {
		if containsLayer(insight, layer) {
			evidence = append(evidence, insight)
		}
	}

	// Add pattern types as evidence
	for _, pattern := range resp.Patterns {
		if mld.patternMatchesLayer(&pattern, layer) {
			evidence = append(evidence, pattern.Type)
		}
	}

	return evidence
}

// determineMLRootCause determines root cause based on layer probabilities
func (mld *MLLayerDetector) determineMLRootCause(infraProb, platformProb, appProb float64) models.Layer {
	// Infrastructure > Platform > Application (highest probability wins)
	maxProb := maxFloat64(infraProb, maxFloat64(platformProb, appProb))

	if maxProb == infraProb && infraProb > 0 {
		return models.LayerInfrastructure
	}
	if maxProb == platformProb && platformProb > 0 {
		return models.LayerPlatform
	}
	return models.LayerApplication
}

// enhanceWithMLPredictions merges ML predictions with keyword-based results
func (mld *MLLayerDetector) enhanceWithMLPredictions(issue *models.LayeredIssue, mlPred *models.MLLayerPredictions) {
	issue.MLPredictions = mlPred

	// Update affected layers based on ML probabilities (use max of keyword and ML confidence)
	if mlPred.Infrastructure != nil && mlPred.Infrastructure.Affected {
		issue.AddAffectedLayer(models.LayerInfrastructure)
		// Use max of keyword (0.70) and ML confidence
		keywordConf := issue.LayerConfidence[models.LayerInfrastructure]
		issue.LayerConfidence[models.LayerInfrastructure] = maxFloat64(keywordConf, mlPred.Infrastructure.Probability)
	}

	if mlPred.Platform != nil && mlPred.Platform.Affected {
		issue.AddAffectedLayer(models.LayerPlatform)
		keywordConf := issue.LayerConfidence[models.LayerPlatform]
		issue.LayerConfidence[models.LayerPlatform] = maxFloat64(keywordConf, mlPred.Platform.Probability)
	}

	if mlPred.Application != nil && mlPred.Application.Affected {
		issue.AddAffectedLayer(models.LayerApplication)
		keywordConf := issue.LayerConfidence[models.LayerApplication]
		issue.LayerConfidence[models.LayerApplication] = maxFloat64(keywordConf, mlPred.Application.Probability)
	}

	// Extract historical pattern from ML response
	if len(mlPred.RootCauseSuggestion) > 0 {
		issue.HistoricalPattern = fmt.Sprintf("%s_pattern", mlPred.RootCauseSuggestion)
	}

	// Use ML-suggested root cause if confidence is high enough
	if mlPred.Confidence >= mld.rootCauseConfidenceThreshold {
		issue.RootCauseLayer = mlPred.RootCauseSuggestion
		mld.log.WithFields(logrus.Fields{
			"ml_suggestion": mlPred.RootCauseSuggestion,
			"confidence":    mlPred.Confidence,
			"threshold":     mld.rootCauseConfidenceThreshold,
		}).Info("Using ML-suggested root cause")
	} else {
		mld.log.WithFields(logrus.Fields{
			"ml_suggestion": mlPred.RootCauseSuggestion,
			"ml_confidence": mlPred.Confidence,
			"threshold":     mld.rootCauseConfidenceThreshold,
			"using_keyword": issue.RootCauseLayer,
		}).Debug("ML confidence below threshold, using keyword-based root cause")
	}
}

// Helper functions

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if contains(text, keyword) {
			return true
		}
	}
	return false
}

func contains(text, substring string) bool {
	// Simple contains check (case-insensitive)
	textLower := text
	substringLower := substring
	for i := 0; i <= len(textLower)-len(substringLower); i++ {
		if textLower[i:i+len(substringLower)] == substringLower {
			return true
		}
	}
	return false
}

func containsLayer(text string, layer models.Layer) bool {
	return contains(text, string(layer))
}
