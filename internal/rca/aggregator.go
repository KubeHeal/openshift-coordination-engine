package rca

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Signal-type weights used when all three correlators are active.
const (
	weightEvents     = 0.40
	weightNetpol     = 0.30
	weightIstio      = 0.30
	weightEventsNoIs = 0.55 // redistributed when Istio is absent
	weightNetpolNoIs = 0.45
)

// Aggregator runs correlators in parallel, merges findings, and computes
// a composite confidence score.
type Aggregator struct {
	correlators    []Correlator
	istioAvailable bool
	log            *logrus.Logger
}

// NewAggregator creates a new RCA aggregator.
// istioAvailable should reflect IstioCorrelator.IsAvailable() at call time.
func NewAggregator(correlators []Correlator, istioAvailable bool, log *logrus.Logger) *Aggregator {
	return &Aggregator{
		correlators:    correlators,
		istioAvailable: istioAvailable,
		log:            log,
	}
}

// correlatorResult collects the output from a single correlator goroutine.
type correlatorResult struct {
	name     string
	findings []RootCause
	duration time.Duration
	err      error
}

// Run executes all correlators in parallel and returns the aggregated Result.
func (a *Aggregator) Run(req *Request) *Result {
	results := a.runParallel(req)

	var allFindings []RootCause
	stats := make([]CorrStat, 0, len(results))
	var affected []string

	for _, cr := range results {
		stats = append(stats, CorrStat{
			Name:     cr.name,
			Findings: len(cr.findings),
			Duration: cr.duration / time.Millisecond,
			Error:    errString(cr.err),
		})
		allFindings = append(allFindings, cr.findings...)
	}

	// Sort by confidence descending.
	sort.Slice(allFindings, func(i, j int) bool {
		return allFindings[i].Confidence > allFindings[j].Confidence
	})

	// Collect affected components.
	seen := make(map[string]bool)
	for i := range allFindings {
		comp := componentKey(&allFindings[i])
		if comp != "" && !seen[comp] {
			seen[comp] = true
			affected = append(affected, comp)
		}
	}

	composite := a.compositeConfidence(allFindings)

	return &Result{
		Status:    "success",
		Service:   req.Service,
		Namespace: req.Namespace,
		TimeRange: TimeRange{
			Start: req.StartTime.UTC().Format(time.RFC3339),
			End:   req.EndTime.UTC().Format(time.RFC3339),
		},
		RootCauses:         allFindings,
		ConfidenceScore:    composite,
		AffectedComponents: affected,
		IstioAvailable:     a.istioAvailable,
		CorrelatorStats:    stats,
	}
}

// runParallel launches each correlator in its own goroutine.
func (a *Aggregator) runParallel(req *Request) []correlatorResult {
	ch := make(chan correlatorResult, len(a.correlators))
	var wg sync.WaitGroup

	for _, c := range a.correlators {
		wg.Add(1)
		go func(corr Correlator) {
			defer wg.Done()
			start := time.Now()
			findings, err := corr.Correlate(req)
			ch <- correlatorResult{
				name:     corr.Name(),
				findings: findings,
				duration: time.Since(start),
				err:      err,
			}
		}(c)
	}

	wg.Wait()
	close(ch)

	var results []correlatorResult
	for cr := range ch {
		if cr.err != nil {
			a.log.WithError(cr.err).WithField("correlator", cr.name).Warn("Correlator failed")
		}
		results = append(results, cr)
	}
	return results
}

// compositeConfidence computes a weighted average across all findings,
// grouped by signal type.
func (a *Aggregator) compositeConfidence(findings []RootCause) float64 {
	if len(findings) == 0 {
		return 0
	}

	typeMax := make(map[SignalType]float64)
	for i := range findings {
		st := findings[i].SignalType
		if findings[i].Confidence > typeMax[st] {
			typeMax[st] = findings[i].Confidence
		}
	}

	weights := a.signalWeights()
	var weighted, totalWeight float64
	for st, maxConf := range typeMax {
		w := weights[st]
		weighted += maxConf * w
		totalWeight += w
	}

	if totalWeight == 0 {
		return 0
	}
	score := weighted / totalWeight
	return math.Round(score*100) / 100
}

// signalWeights returns the weight map, adjusting when Istio is absent.
func (a *Aggregator) signalWeights() map[SignalType]float64 {
	if a.istioAvailable {
		return map[SignalType]float64{
			SignalPodEvent:      weightEvents,
			SignalNetworkPolicy: weightNetpol,
			SignalIstioVS:       weightIstio,
		}
	}
	return map[SignalType]float64{
		SignalPodEvent:      weightEventsNoIs,
		SignalNetworkPolicy: weightNetpolNoIs,
	}
}

// componentKey extracts the most relevant affected-component identifier from
// a finding's evidence map.
func componentKey(rc *RootCause) string {
	if obj, ok := rc.Evidence["object"].(string); ok && obj != "" {
		return obj
	}
	if pn, ok := rc.Evidence["policy_name"].(string); ok && pn != "" {
		return fmt.Sprintf("networkpolicy/%s", pn)
	}
	if vs, ok := rc.Evidence["virtual_service"].(string); ok && vs != "" {
		return fmt.Sprintf("virtualservice/%s", vs)
	}
	return ""
}

func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
