// Package features provides feature engineering for ML models.
package features

import (
	"context"
	"fmt"
	"time"

	"github.com/tosin2013/openshift-coordination-engine/internal/integrations"
)

// PrometheusAdapter adapts the PrometheusClient to the MetricDataProvider interface.
// This allows the PredictiveFeatureBuilder to use Prometheus as its data source.
type PrometheusAdapter struct {
	client *integrations.PrometheusClient
}

// NewPrometheusAdapter creates a new adapter wrapping a PrometheusClient
func NewPrometheusAdapter(client *integrations.PrometheusClient) *PrometheusAdapter {
	return &PrometheusAdapter{client: client}
}

// QueryRange implements MetricDataProvider.QueryRange by delegating to PrometheusClient
func (a *PrometheusAdapter) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]DataPoint, error) {
	if a.client == nil {
		return nil, nil
	}

	// Call the PrometheusClient's QueryRange method
	prometheusPoints, err := a.client.QueryRange(ctx, query, start, end, step)
	if err != nil {
		return nil, fmt.Errorf("prometheus range query failed: %w", err)
	}

	// Convert PredictiveDataPoint to features.DataPoint
	dataPoints := make([]DataPoint, len(prometheusPoints))
	for i, p := range prometheusPoints {
		dataPoints[i] = DataPoint{
			Timestamp: p.Timestamp,
			Value:     p.Value,
		}
	}

	return dataPoints, nil
}

// Query implements MetricDataProvider.Query by delegating to PrometheusClient
func (a *PrometheusAdapter) Query(ctx context.Context, query string) (float64, error) {
	if a.client == nil {
		return 0, nil
	}
	value, err := a.client.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("prometheus instant query failed: %w", err)
	}
	return value, nil
}

// IsAvailable implements MetricDataProvider.IsAvailable
func (a *PrometheusAdapter) IsAvailable() bool {
	return a.client != nil && a.client.IsAvailable()
}
