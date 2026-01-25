package detector

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// DetectionTotal counts total deployment method detections by method and source
	DetectionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_detection_total",
			Help: "Total number of deployment method detections",
		},
		[]string{"method", "source", "resource_kind"},
	)

	// DetectionConfidence records the confidence score distribution for detections
	DetectionConfidence = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_detection_confidence",
			Help:    "Distribution of deployment detection confidence scores",
			Buckets: []float64{0.5, 0.6, 0.7, 0.8, 0.9, 0.95, 1.0},
		},
		[]string{"method"},
	)

	// CacheHits counts successful cache hits
	CacheHits = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_detection_cache_hits_total",
			Help: "Total number of deployment detection cache hits",
		},
		[]string{"resource_kind"},
	)

	// CacheMisses counts cache misses (requiring API call)
	CacheMisses = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_detection_cache_misses_total",
			Help: "Total number of deployment detection cache misses",
		},
		[]string{"resource_kind"},
	)

	// DetectionDuration measures the time taken for detection
	DetectionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_detection_duration_seconds",
			Help:    "Time taken to detect deployment method",
			Buckets: prometheus.DefBuckets, // 0.005s to 10s
		},
		[]string{"method", "cache_status"},
	)

	// DetectionErrors counts detection errors by type
	DetectionErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_detection_errors_total",
			Help: "Total number of deployment detection errors",
		},
		[]string{"error_type", "resource_kind"},
	)

	// CacheSize tracks the current size of the detection cache
	CacheSize = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "coordination_engine_detection_cache_size",
			Help: "Current number of entries in the detection cache",
		},
		[]string{"status"}, // valid, expired
	)
)

// RecordDetection records metrics for a successful detection
func RecordDetection(method, source, resourceKind string, confidence float64, fromCache bool) {
	DetectionTotal.WithLabelValues(method, source, resourceKind).Inc()
	DetectionConfidence.WithLabelValues(method).Observe(confidence)

	cacheStatus := "miss"
	if fromCache {
		CacheHits.WithLabelValues(resourceKind).Inc()
		cacheStatus = "hit"
	} else {
		CacheMisses.WithLabelValues(resourceKind).Inc()
	}

	DetectionDuration.WithLabelValues(method, cacheStatus).Observe(0) // Will be set by timer
}

// RecordDetectionError records a detection error
func RecordDetectionError(errorType, resourceKind string) {
	DetectionErrors.WithLabelValues(errorType, resourceKind).Inc()
}

// UpdateCacheSize updates the cache size metrics
func UpdateCacheSize(validEntries, expiredEntries int) {
	CacheSize.WithLabelValues("valid").Set(float64(validEntries))
	CacheSize.WithLabelValues("expired").Set(float64(expiredEntries))
}
