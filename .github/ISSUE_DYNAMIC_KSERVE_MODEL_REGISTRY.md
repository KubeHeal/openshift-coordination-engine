# Feature Request: Dynamic KServe Model Registry Support

## Summary

Enable users to register custom KServe models via a dynamic model registry configuration, allowing domain-specific ML models (database, network, storage, security) to be integrated without modifying coordination engine code.

## Background

### Current Limitation

The coordination engine uses **hardcoded environment variables** for KServe model discovery:

```go
// internal/integrations/kserve_client.go
type KServeClient struct {
    anomalyDetectorURL     string  // Hardcoded via KSERVE_ANOMALY_DETECTOR_SERVICE
    predictiveAnalyticsURL string  // Hardcoded via KSERVE_PREDICTIVE_ANALYTICS_SERVICE
    // ...
}
```

**Problem**: Users cannot add custom models (e.g., `disk-failure-predictor`, `postgres-query-anomaly`) without coordination engine code changes.

### User Requirements (Platform ADR-040)

From the platform repository [ADR-040: Extensible KServe Model Registry](https://github.com/tosin2013/openshift-aiops-platform/blob/main/docs/adrs/040-extensible-kserve-model-registry.md):

> Users should be able to register custom domain-specific models with the coordination engine without modifying platform code.

**Example Use Cases**:
- **Database Performance**: `postgres-query-anomaly` - Detect abnormal database query patterns
- **Network Traffic**: `network-traffic-predictor` - Forecast network load
- **Disk Failure Prediction**: `disk-failure-predictor` - Predict disk failures 24h in advance
- **Security Threats**: `security-threat-detector` - Detect suspicious API call patterns

## Proposed Solution

Support dynamic model loading via `KSERVE_MODEL_REGISTRY` environment variable containing a JSON array of model configurations.

### Configuration Example

```bash
# Environment variable from Helm chart
export KSERVE_MODEL_REGISTRY='[
  {
    "name": "anomaly-detector",
    "service": "anomaly-detector-predictor",
    "namespace": "self-healing-platform",
    "type": "anomaly",
    "description": "Isolation Forest anomaly detection"
  },
  {
    "name": "disk-failure-predictor",
    "service": "disk-failure-predictor-predictor",
    "namespace": "storage-monitoring",
    "type": "predictive",
    "description": "Predicts disk failures using SMART metrics"
  },
  {
    "name": "postgres-query-anomaly",
    "service": "postgres-anomaly-predictor",
    "namespace": "database-monitoring",
    "type": "anomaly",
    "description": "Detects abnormal database query patterns"
  }
]'
```

## Implementation Requirements

### 1. Data Structures (`pkg/models/kserve_registry.go` - NEW FILE)

```go
package models

// ModelType represents the type of ML model
type ModelType string

const (
    ModelTypeAnomaly        ModelType = "anomaly"
    ModelTypePredictive     ModelType = "predictive"
    ModelTypeClassification ModelType = "classification"
)

// KServeModel represents a single KServe model registration
type KServeModel struct {
    Name        string    `json:"name"`        // Unique model identifier
    Service     string    `json:"service"`     // KServe InferenceService name (e.g., "anomaly-detector-predictor")
    Namespace   string    `json:"namespace"`   // Kubernetes namespace (optional, uses default if empty)
    Type        ModelType `json:"type"`        // Model type
    Description string    `json:"description"` // Human-readable description (optional)
}

// ModelRegistry holds all registered models and provides lookup methods
type ModelRegistry struct {
    Models        []KServeModel
    DefaultNS     string
    BaseURLFormat string // "http://{service}.{namespace}.svc.cluster.local"
}

// GetModelURL constructs the full URL for a registered model
func (r *ModelRegistry) GetModelURL(modelName string) (string, error) {
    for _, model := range r.Models {
        if model.Name == modelName {
            namespace := model.Namespace
            if namespace == "" {
                namespace = r.DefaultNS
            }
            return fmt.Sprintf("http://%s.%s.svc.cluster.local", model.Service, namespace), nil
        }
    }
    return "", fmt.Errorf("model not found: %s", modelName)
}

// GetModelsByType returns all models of a specific type
func (r *ModelRegistry) GetModelsByType(modelType ModelType) []KServeModel {
    var result []KServeModel
    for _, model := range r.Models {
        if model.Type == modelType {
            result = append(result, model)
        }
    }
    return result
}

// ListModels returns all registered models
func (r *ModelRegistry) ListModels() []KServeModel {
    return r.Models
}
```

### 2. Registry Loader (`pkg/config/kserve_registry_loader.go` - NEW FILE)

```go
package config

import (
    "encoding/json"
    "fmt"
    "os"

    "github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// LoadModelRegistry loads the model registry from KSERVE_MODEL_REGISTRY environment variable
func LoadModelRegistry() (*models.ModelRegistry, error) {
    registryJSON := os.Getenv("KSERVE_MODEL_REGISTRY")
    if registryJSON == "" {
        return nil, fmt.Errorf("KSERVE_MODEL_REGISTRY environment variable not set")
    }

    var modelList []models.KServeModel
    if err := json.Unmarshal([]byte(registryJSON), &modelList); err != nil {
        return nil, fmt.Errorf("failed to parse KSERVE_MODEL_REGISTRY: %w", err)
    }

    if len(modelList) == 0 {
        return nil, fmt.Errorf("KSERVE_MODEL_REGISTRY contains no models")
    }

    defaultNS := os.Getenv("KSERVE_NAMESPACE")
    if defaultNS == "" {
        defaultNS = "self-healing-platform"
    }

    return &models.ModelRegistry{
        Models:        modelList,
        DefaultNS:     defaultNS,
        BaseURLFormat: "http://{service}.{namespace}.svc.cluster.local",
    }, nil
}

// LoadModelRegistryWithFallback loads model registry with backward compatibility
func LoadModelRegistryWithFallback(log *logrus.Logger) (*models.ModelRegistry, error) {
    // Try new approach first
    registry, err := LoadModelRegistry()
    if err == nil {
        log.Infof("Loaded %d models from KSERVE_MODEL_REGISTRY", len(registry.Models))
        for _, model := range registry.Models {
            url, _ := registry.GetModelURL(model.Name)
            log.Infof("  - %s (%s) at %s", model.Name, model.Type, url)
        }
        return registry, nil
    }

    // Fall back to old approach (hardcoded env vars) for backward compatibility
    log.Warnf("KSERVE_MODEL_REGISTRY not found, using legacy configuration: %v", err)

    models := []models.KServeModel{}
    defaultNS := os.Getenv("KSERVE_NAMESPACE")
    if defaultNS == "" {
        defaultNS = "self-healing-platform"
    }

    // Legacy: KSERVE_ANOMALY_DETECTOR_SERVICE
    if svc := os.Getenv("KSERVE_ANOMALY_DETECTOR_SERVICE"); svc != "" {
        models = append(models, models.KServeModel{
            Name:        "anomaly-detector",
            Service:     svc,
            Namespace:   defaultNS,
            Type:        models.ModelTypeAnomaly,
            Description: "Anomaly detection (legacy config)",
        })
    }

    // Legacy: KSERVE_PREDICTIVE_ANALYTICS_SERVICE
    if svc := os.Getenv("KSERVE_PREDICTIVE_ANALYTICS_SERVICE"); svc != "" {
        models = append(models, models.KServeModel{
            Name:        "predictive-analytics",
            Service:     svc,
            Namespace:   defaultNS,
            Type:        models.ModelTypePredictive,
            Description: "Predictive analytics (legacy config)",
        })
    }

    if len(models) == 0 {
        return nil, fmt.Errorf("no KServe models configured (neither KSERVE_MODEL_REGISTRY nor legacy env vars)")
    }

    log.Infof("Loaded %d models from legacy environment variables", len(models))
    return &models.ModelRegistry{
        Models:    models,
        DefaultNS: defaultNS,
    }, nil
}
```

### 3. KServe Client Refactor (`internal/integrations/kserve_client.go` - MODIFY)

**Current Code** (hardcoded):
```go
type KServeClient struct {
    anomalyDetectorURL     string
    predictiveAnalyticsURL string
    httpClient             *http.Client
    log                    *logrus.Logger
}
```

**New Code** (dynamic):
```go
type KServeClient struct {
    registry   *models.ModelRegistry
    httpClient *http.Client
    log        *logrus.Logger
}

func NewKServeClient(registry *models.ModelRegistry, log *logrus.Logger) *KServeClient {
    transport := &http.Transport{
        MaxIdleConns:        100,
        MaxIdleConnsPerHost: 10,
        IdleConnTimeout:     90 * time.Second,
    }

    return &KServeClient{
        registry: registry,
        httpClient: &http.Client{
            Transport: transport,
            Timeout:   10 * time.Second,
        },
        log: log,
    }
}

// DetectAnomalies now uses registry lookup
func (c *KServeClient) DetectAnomalies(
    ctx context.Context,
    instances [][]float64,
) (*AnomalyDetectionResult, error) {
    // Find anomaly models from registry
    anomalyModels := c.registry.GetModelsByType(models.ModelTypeAnomaly)
    if len(anomalyModels) == 0 {
        return nil, fmt.Errorf("no anomaly detection models registered")
    }

    // Use first anomaly model (or implement round-robin/fallback)
    model := anomalyModels[0]
    baseURL, err := c.registry.GetModelURL(model.Name)
    if err != nil {
        return nil, fmt.Errorf("failed to get model URL: %w", err)
    }

    endpoint := fmt.Sprintf("%s/v1/models/%s:predict", baseURL, model.Name)
    // ... rest of implementation
}

// PredictByModelName - NEW METHOD for custom model invocation
func (c *KServeClient) PredictByModelName(
    ctx context.Context,
    modelName string,
    instances [][]float64,
) (*KServeV1Response, error) {
    baseURL, err := c.registry.GetModelURL(modelName)
    if err != nil {
        return nil, fmt.Errorf("model not found: %w", err)
    }

    endpoint := fmt.Sprintf("%s/v1/models/%s:predict", baseURL, modelName)

    req := &KServeV1Request{Instances: instances}
    body, err := json.Marshal(req)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal request: %w", err)
    }

    httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(body))
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("prediction request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("prediction failed with status %d", resp.StatusCode)
    }

    var result KServeV1Response
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("failed to decode response: %w", err)
    }

    return &result, nil
}
```

### 4. API Endpoints (`internal/api/models.go` - NEW FILE)

Add REST API endpoints to expose registered models:

```go
package api

import (
    "net/http"

    "github.com/gin-gonic/gin"
    "github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// ModelsHandler handles model registry API endpoints
type ModelsHandler struct {
    registry *models.ModelRegistry
}

// NewModelsHandler creates a new models handler
func NewModelsHandler(registry *models.ModelRegistry) *ModelsHandler {
    return &ModelsHandler{registry: registry}
}

// RegisterRoutes registers model API routes
func (h *ModelsHandler) RegisterRoutes(r *gin.RouterGroup) {
    r.GET("/models", h.ListModels)
    r.GET("/models/:name", h.GetModel)
    r.POST("/models/:name/predict", h.PredictWithModel)
}

// ListModels godoc
// @Summary List all registered KServe models
// @Description Returns list of all models registered in the model registry
// @Tags models
// @Produce json
// @Success 200 {object} map[string]interface{} "List of models"
// @Router /api/v1/models [get]
func (h *ModelsHandler) ListModels(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{
        "models": h.registry.ListModels(),
        "total":  len(h.registry.ListModels()),
    })
}

// GetModel godoc
// @Summary Get specific model details
// @Description Returns details about a registered model including its URL and health status
// @Tags models
// @Produce json
// @Param name path string true "Model name"
// @Success 200 {object} map[string]interface{} "Model details"
// @Failure 404 {object} map[string]string "Model not found"
// @Router /api/v1/models/{name} [get]
func (h *ModelsHandler) GetModel(c *gin.Context) {
    modelName := c.Param("name")

    for _, model := range h.registry.ListModels() {
        if model.Name == modelName {
            url, _ := h.registry.GetModelURL(modelName)
            c.JSON(http.StatusOK, gin.H{
                "name":        model.Name,
                "service":     model.Service,
                "namespace":   model.Namespace,
                "type":        model.Type,
                "description": model.Description,
                "url":         url,
            })
            return
        }
    }

    c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
}

// PredictWithModel godoc
// @Summary Proxy prediction request to KServe model
// @Description Forwards prediction request to the specified KServe InferenceService
// @Tags models
// @Accept json
// @Produce json
// @Param name path string true "Model name"
// @Param request body map[string]interface{} true "KServe v1 prediction request"
// @Success 200 {object} map[string]interface{} "Prediction response"
// @Failure 404 {object} map[string]string "Model not found"
// @Failure 500 {object} map[string]string "Prediction error"
// @Router /api/v1/models/{name}/predict [post]
func (h *ModelsHandler) PredictWithModel(c *gin.Context) {
    modelName := c.Param("name")

    // Parse request body
    var req map[string]interface{}
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
        return
    }

    // TODO: Forward request to KServe via KServeClient.PredictByModelName()
    // This requires access to KServeClient instance

    c.JSON(http.StatusNotImplemented, gin.H{
        "error": "prediction proxy not yet implemented",
        "model": modelName,
    })
}
```

### 5. Main Application Update (`cmd/coordination-engine/main.go` - MODIFY)

```go
func main() {
    log := logrus.New()

    // Load model registry at startup
    registry, err := config.LoadModelRegistryWithFallback(log)
    if err != nil {
        log.Fatalf("Failed to load model registry: %v", err)
    }

    log.Infof("Model registry loaded successfully with %d models", len(registry.ListModels()))

    // Create KServe client with registry
    kserveClient := integrations.NewKServeClient(registry, log)

    // Create API handlers
    modelsHandler := api.NewModelsHandler(registry)

    // Register routes
    router := gin.Default()
    v1 := router.Group("/api/v1")
    {
        modelsHandler.RegisterRoutes(v1)
        // ... other routes
    }

    // Start server
    log.Infof("Starting coordination engine on :8080")
    if err := router.Run(":8080"); err != nil {
        log.Fatalf("Failed to start server: %v", err)
    }
}
```

## Testing Requirements

### Unit Tests

#### `pkg/models/kserve_registry_test.go` (NEW)

```go
package models_test

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

func TestModelRegistry_GetModelURL(t *testing.T) {
    registry := &models.ModelRegistry{
        Models: []models.KServeModel{
            {
                Name:      "test-model",
                Service:   "test-predictor",
                Namespace: "test-ns",
            },
        },
        DefaultNS: "default",
    }

    url, err := registry.GetModelURL("test-model")
    assert.NoError(t, err)
    assert.Equal(t, "http://test-predictor.test-ns.svc.cluster.local", url)
}

func TestModelRegistry_GetModelURL_UsesDefaultNamespace(t *testing.T) {
    registry := &models.ModelRegistry{
        Models: []models.KServeModel{
            {
                Name:    "test-model",
                Service: "test-predictor",
                // No namespace specified
            },
        },
        DefaultNS: "self-healing-platform",
    }

    url, err := registry.GetModelURL("test-model")
    assert.NoError(t, err)
    assert.Equal(t, "http://test-predictor.self-healing-platform.svc.cluster.local", url)
}

func TestModelRegistry_GetModelsByType(t *testing.T) {
    registry := &models.ModelRegistry{
        Models: []models.KServeModel{
            {Name: "anomaly-1", Type: models.ModelTypeAnomaly},
            {Name: "predictive-1", Type: models.ModelTypePredictive},
            {Name: "anomaly-2", Type: models.ModelTypeAnomaly},
        },
    }

    anomalyModels := registry.GetModelsByType(models.ModelTypeAnomaly)
    assert.Len(t, anomalyModels, 2)
    assert.Equal(t, "anomaly-1", anomalyModels[0].Name)
    assert.Equal(t, "anomaly-2", anomalyModels[1].Name)
}
```

#### `pkg/config/kserve_registry_loader_test.go` (NEW)

```go
package config_test

import (
    "os"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/tosin2013/openshift-coordination-engine/pkg/config"
)

func TestLoadModelRegistry(t *testing.T) {
    os.Setenv("KSERVE_MODEL_REGISTRY", `[
        {"name": "anomaly-detector", "service": "anomaly-predictor", "type": "anomaly"},
        {"name": "disk-predictor", "service": "disk-predictor", "namespace": "storage", "type": "predictive"}
    ]`)
    defer os.Unsetenv("KSERVE_MODEL_REGISTRY")

    registry, err := config.LoadModelRegistry()
    assert.NoError(t, err)
    assert.Len(t, registry.Models, 2)
    assert.Equal(t, "anomaly-detector", registry.Models[0].Name)
}

func TestLoadModelRegistryWithFallback_Legacy(t *testing.T) {
    // Clear new env var
    os.Unsetenv("KSERVE_MODEL_REGISTRY")

    // Set legacy env vars
    os.Setenv("KSERVE_ANOMALY_DETECTOR_SERVICE", "anomaly-predictor")
    os.Setenv("KSERVE_PREDICTIVE_ANALYTICS_SERVICE", "predictive-predictor")
    defer os.Unsetenv("KSERVE_ANOMALY_DETECTOR_SERVICE")
    defer os.Unsetenv("KSERVE_PREDICTIVE_ANALYTICS_SERVICE")

    log := logrus.New()
    registry, err := config.LoadModelRegistryWithFallback(log)
    assert.NoError(t, err)
    assert.Len(t, registry.Models, 2)
}
```

### Integration Tests

#### `test/integration/kserve_registry_test.go` (NEW)

```bash
# Deploy 3 test KServe InferenceServices
kubectl apply -f test/fixtures/kserve-test-models.yaml

# Run integration tests
make test-integration
```

Test scenarios:
1. ✅ Load registry with 3+ models in different namespaces
2. ✅ Verify `/api/v1/models` returns all registered models
3. ✅ Test prediction proxy to each model
4. ✅ Verify backward compatibility with legacy env vars

## Documentation Updates

### README.md

Add section after line 42 (after KServe integration example):

```markdown
#### Dynamic Model Registry (ADR-040)

Register multiple custom models via JSON configuration:

```bash
export KSERVE_MODEL_REGISTRY='[
  {
    "name": "anomaly-detector",
    "service": "anomaly-detector-predictor",
    "namespace": "self-healing-platform",
    "type": "anomaly"
  },
  {
    "name": "disk-failure-predictor",
    "service": "disk-failure-predictor-predictor",
    "namespace": "storage-monitoring",
    "type": "predictive",
    "description": "Predicts disk failures 24h in advance"
  }
]'

podman run -d \
  -p 8080:8080 \
  -e ENABLE_KSERVE_INTEGRATION=true \
  -e KSERVE_NAMESPACE=self-healing-platform \
  -e KSERVE_MODEL_REGISTRY="$KSERVE_MODEL_REGISTRY" \
  quay.io/takinosh/openshift-coordination-engine:latest
```

List registered models:
```bash
curl http://localhost:8080/api/v1/models
```

Use custom model:
```bash
curl -X POST http://localhost:8080/api/v1/models/disk-failure-predictor/predict \
  -H "Content-Type: application/json" \
  -d '{"instances": [[85.5, 5000, 365]]}'
```
```

### API-CONTRACT.md

Add new section after line 218:

```markdown
### 2.3 Dynamic Model Registry API (ADR-040)

The coordination engine exposes APIs to discover and interact with registered KServe models.

#### `GET /api/v1/models`

List all registered KServe models.

**Response** (200 OK):
```json
{
  "models": [
    {
      "name": "anomaly-detector",
      "service": "anomaly-detector-predictor",
      "namespace": "self-healing-platform",
      "type": "anomaly",
      "description": "Isolation Forest anomaly detection"
    },
    {
      "name": "disk-failure-predictor",
      "service": "disk-failure-predictor-predictor",
      "namespace": "storage-monitoring",
      "type": "predictive",
      "description": "Predicts disk failures using SMART metrics"
    }
  ],
  "total": 2
}
```

#### `GET /api/v1/models/{name}`

Get details about a specific registered model.

**Response** (200 OK):
```json
{
  "name": "disk-failure-predictor",
  "service": "disk-failure-predictor-predictor",
  "namespace": "storage-monitoring",
  "type": "predictive",
  "description": "Predicts disk failures using SMART metrics",
  "url": "http://disk-failure-predictor-predictor.storage-monitoring.svc.cluster.local"
}
```

#### `POST /api/v1/models/{name}/predict`

Proxy prediction request to a registered KServe model.

**Request** (KServe v1 format):
```json
{
  "instances": [[85.5, 5000, 365]]
}
```

**Response** (200 OK):
```json
{
  "predictions": [-1],
  "model_name": "disk-failure-predictor",
  "model_version": "v1"
}
```
```

### docs/CONFIGURATION.md (NEW FILE)

Create comprehensive configuration guide:

```markdown
# Configuration Guide

## Environment Variables

### KServe Integration

#### ENABLE_KSERVE_INTEGRATION
- **Type**: Boolean (true/false)
- **Default**: false
- **Description**: Enable KServe model integration

#### KSERVE_NAMESPACE
- **Type**: String
- **Default**: self-healing-platform
- **Description**: Default namespace for KServe models (used when model doesn't specify namespace)

#### KSERVE_MODEL_REGISTRY
- **Type**: JSON Array
- **Required**: When ENABLE_KSERVE_INTEGRATION=true
- **Description**: Dynamic model registry configuration
- **Format**:
  ```json
  [
    {
      "name": "model-name",
      "service": "kserve-service-name",
      "namespace": "optional-namespace",
      "type": "anomaly|predictive|classification",
      "description": "optional-description"
    }
  ]
  ```

#### Legacy Environment Variables (Deprecated)

**KSERVE_ANOMALY_DETECTOR_SERVICE**
- **Status**: Deprecated (use KSERVE_MODEL_REGISTRY instead)
- **Description**: Hardcoded anomaly detector service name

**KSERVE_PREDICTIVE_ANALYTICS_SERVICE**
- **Status**: Deprecated (use KSERVE_MODEL_REGISTRY instead)
- **Description**: Hardcoded predictive analytics service name

## Migration Guide

### From Legacy to Dynamic Registry

**Before** (legacy hardcoded):
```bash
export KSERVE_ANOMALY_DETECTOR_SERVICE=anomaly-detector-predictor
export KSERVE_PREDICTIVE_ANALYTICS_SERVICE=predictive-analytics-predictor
```

**After** (dynamic registry):
```bash
export KSERVE_MODEL_REGISTRY='[
  {"name": "anomaly-detector", "service": "anomaly-detector-predictor", "type": "anomaly"},
  {"name": "predictive-analytics", "service": "predictive-analytics-predictor", "type": "predictive"}
]'
```
```

## Success Criteria

- [ ] Model registry loads from `KSERVE_MODEL_REGISTRY` JSON
- [ ] Supports multi-namespace model deployment
- [ ] `/api/v1/models` endpoint lists all registered models
- [ ] `/api/v1/models/:name` endpoint returns model details
- [ ] `/api/v1/models/:name/predict` proxies to KServe InferenceServices
- [ ] Backward compatible with legacy `KSERVE_ANOMALY_DETECTOR_SERVICE` env vars
- [ ] Unit tests for model registry loading and lookup
- [ ] Integration tests with 3+ custom models
- [ ] Documentation updated (README.md, API-CONTRACT.md, CONFIGURATION.md)
- [ ] CI/CD tests pass

## Platform Integration

This feature enables the OpenShift AIOps Platform to support user-deployed models:

**Platform Side** (values-hub.yaml):
```yaml
coordinationEngine:
  kserve:
    models:
      - name: anomaly-detector
        service: anomaly-detector-predictor
        type: anomaly
      - name: disk-failure-predictor  # USER ADDS THIS
        service: disk-failure-predictor-predictor
        namespace: storage-monitoring
        type: predictive
```

**Coordination Engine Side** (receives via env var):
```bash
KSERVE_MODEL_REGISTRY='[...]'  # Passed from Helm chart
```

## References

- **Platform ADR-039**: [User-Deployed KServe Models](https://github.com/tosin2013/openshift-aiops-platform/blob/main/docs/adrs/039-user-deployed-kserve-models.md)
- **Platform ADR-040**: [Extensible KServe Model Registry](https://github.com/tosin2013/openshift-aiops-platform/blob/main/docs/adrs/040-extensible-kserve-model-registry.md)
- **Platform User Guide**: [USER-MODEL-DEPLOYMENT-GUIDE.md](https://github.com/tosin2013/openshift-aiops-platform/blob/main/docs/guides/USER-MODEL-DEPLOYMENT-GUIDE.md)
- **KServe v1 Protocol**: https://kserve.github.io/website/latest/modelserving/data_plane/v1_protocol/

## Labels

- `enhancement`
- `kserve-integration`
- `user-extensibility`
- `platform-agnostic`
- `backward-compatible`

## Milestone

**v2.0.0** - Next major release with extensibility features

---

**Created by**: OpenShift AIOps Platform Team
**Related Platform Issue**: N/A (architectural decision documented in ADR-040)
