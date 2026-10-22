package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Scorer calls one KServe model and returns the integer verdict.
type Scorer interface {
	Score(ctx context.Context, predictURL string, features []float64) (int, error)
}

// PredictURL builds the KServe v1 predict URL for a policy model.
// The service defaults to {model.name}-predictor in defaultNamespace.
// Optional model.service and model.namespace override that derivation.
// This does not consult KSERVE_*_SERVICE environment variables.
func PredictURL(model *ModelSpec, defaultNamespace string, port int) (string, error) {
	if model == nil || model.Name == "" {
		return "", errors.New("model name is required")
	}
	namespace := model.Namespace
	if namespace == "" {
		namespace = defaultNamespace
	}
	if namespace == "" {
		return "", errors.New("model namespace is required")
	}
	service, err := resolvedService(model)
	if err != nil {
		return "", err
	}
	if port <= 0 {
		port = 8080
	}
	if port > 65535 {
		return "", fmt.Errorf("model port %d is invalid", port)
	}
	return fmt.Sprintf(
		"http://%s.%s.svc.cluster.local:%d/v1/models/%s:predict",
		service, namespace, port, model.Name,
	), nil
}

func resolvedService(model *ModelSpec) (string, error) {
	if model == nil {
		return "", errors.New("model name is required")
	}
	service := model.Service
	if service == "" {
		service = model.Name + "-predictor"
	}
	if errs := validation.IsDNS1123Label(service); len(errs) > 0 {
		return "", fmt.Errorf("model service %q is invalid: %s", service, strings.Join(errs, ", "))
	}
	return service, nil
}

// HTTPScorer posts a one-row KServe v1 request and reads predictions[0].
type HTTPScorer struct {
	client *http.Client
	log    *logrus.Logger
}

// NewHTTPScorer returns a scorer with connection pooling.
func NewHTTPScorer(timeout time.Duration, log *logrus.Logger) *HTTPScorer {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if log == nil {
		log = logrus.New()
	}
	return &HTTPScorer{
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		log: log,
	}
}

// Score posts the feature vector and returns an integer verdict.
func (s *HTTPScorer) Score(ctx context.Context, predictURL string, features []float64) (int, error) {
	body, err := json.Marshal(map[string]any{
		"instances": [][]float64{features},
	})
	if err != nil {
		return 0, fmt.Errorf("encode model request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, predictURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("call model: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			s.log.WithError(cerr).Debug("close model response")
		}
	}()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("read model response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("model returned status %d: %s", resp.StatusCode, trimBody(payload))
	}
	return parseVerdict(payload)
}

func parseVerdict(payload []byte) (int, error) {
	var decoded struct {
		Predictions []json.RawMessage `json:"predictions"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return 0, fmt.Errorf("decode model response: %w", err)
	}
	if len(decoded.Predictions) == 0 {
		return 0, errors.New("model response has no predictions")
	}
	var number json.Number
	if err := json.Unmarshal(decoded.Predictions[0], &number); err != nil {
		return 0, fmt.Errorf("prediction is not a number: %w", err)
	}
	value, err := number.Float64()
	if err != nil {
		return 0, fmt.Errorf("parse prediction: %w", err)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("prediction is not finite")
	}
	rounded := math.Round(value)
	if math.Abs(value-rounded) > 1e-6 {
		return 0, fmt.Errorf("prediction %s is not an integer verdict", number.String())
	}
	return int(rounded), nil
}

func trimBody(payload []byte) string {
	text := strings.TrimSpace(string(payload))
	if len(text) > 256 {
		return text[:256]
	}
	return text
}
