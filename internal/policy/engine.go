package policy

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	resultResolved     = "resolved"
	resultIgnored      = "ignored"
	resultUnmatched    = "unmatched"
	resultDeduped      = "deduped"
	resultNoRepair     = "no_repair"
	resultFeatureError = "feature_error"
	resultModelError   = "model_error"
	resultNoAction     = "no_action"
	resultSuccess      = "success"
	resultError        = "error"
	resultAborted      = "aborted"
	resultUnexpected   = "unexpected_verdict"
	repairDedupeTTL    = 10 * time.Minute
	maxDedupeEntries   = 10000
	nonePolicyLabel    = "none"
)

// Querier runs an instant PromQL query.
type Querier interface {
	Query(ctx context.Context, query string) (float64, error)
}

// Webhook is the subset of an Alertmanager notification the engine evaluates.
type Webhook struct {
	Status string
	Alerts []IncomingAlert
}

// IncomingAlert is one alert from an Alertmanager webhook.
type IncomingAlert struct {
	Status      string
	Labels      map[string]string
	StartsAt    time.Time
	Fingerprint string
}

// AlertsResponse is the body returned by POST /api/v1/alerts.
type AlertsResponse struct {
	Status          string        `json:"status"`
	MatchedPolicies int           `json:"matched_policies"`
	ActionsTaken    []ActionTaken `json:"actions_taken"`
}

// ActionTaken is the outcome of one firing alert that selected a policy.
type ActionTaken struct {
	Policy  string `json:"policy"`
	Alert   string `json:"alert"`
	Model   string `json:"model,omitempty"`
	Verdict *int   `json:"verdict,omitempty"`
	Action  string `json:"action,omitempty"`
	Result  string `json:"result"`
	Error   string `json:"error,omitempty"`
}

// EngineConfig wires the policy loop to Prometheus, KServe, and Kubernetes.
type EngineConfig struct {
	Loader         *Loader
	Querier        Querier
	Scorer         Scorer
	Actions        *ActionExecutor
	ModelNamespace string
	ModelPort      int
	Log            *logrus.Logger
	Now            func() time.Time
}

// Engine matches alerts to policies, scores the policy model, and repairs on verdict -1.
type Engine struct {
	loader         *Loader
	querier        Querier
	scorer         Scorer
	actions        *ActionExecutor
	modelNamespace string
	modelPort      int
	log            *logrus.Logger
	now            func() time.Time
	dedupe         *deduper
}

// NewEngine validates dependencies and returns a policy engine.
func NewEngine(cfg *EngineConfig) (*Engine, error) {
	if cfg == nil {
		return nil, errors.New("policy engine config is required")
	}
	if cfg.Loader == nil {
		return nil, errors.New("policy loader is required")
	}
	if cfg.Querier == nil {
		return nil, errors.New("prometheus querier is required")
	}
	if cfg.Scorer == nil {
		return nil, errors.New("model scorer is required")
	}
	if cfg.Actions == nil {
		return nil, errors.New("action executor is required")
	}
	if cfg.ModelNamespace == "" {
		return nil, errors.New("model namespace is required")
	}
	if cfg.ModelPort <= 0 {
		cfg.ModelPort = 8080
	}
	if cfg.Log == nil {
		cfg.Log = logrus.New()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Engine{
		loader:         cfg.Loader,
		querier:        cfg.Querier,
		scorer:         cfg.Scorer,
		actions:        cfg.Actions,
		modelNamespace: cfg.ModelNamespace,
		modelPort:      cfg.ModelPort,
		log:            cfg.Log,
		now:            cfg.Now,
		dedupe:         newDeduper(repairDedupeTTL),
	}, nil
}

// HandleWebhook evaluates every alert. Unmatched alerts do not fail the request.
func (e *Engine) HandleWebhook(ctx context.Context, webhook Webhook) AlertsResponse {
	response := AlertsResponse{
		Status:       "processed",
		ActionsTaken: make([]ActionTaken, 0),
	}
	for _, alert := range webhook.Alerts {
		outcome := e.processAlert(ctx, webhook.Status, alert)
		recordAlertOutcome(&outcome)
		if !outcome.matched {
			continue
		}
		response.MatchedPolicies++
		response.ActionsTaken = append(response.ActionsTaken, outcome.taken())
	}
	return response
}

func (e *Engine) processAlert(ctx context.Context, webhookStatus string, alert IncomingAlert) alertOutcome {
	outcome := alertOutcome{
		alertName: labelValue(alert.Labels, "alertname"),
		namespace: alertNamespace(alert.Labels),
		result:    resultIgnored,
	}
	status := alert.Status
	if status == "" {
		status = webhookStatus
	}
	if !strings.EqualFold(status, "firing") {
		if strings.EqualFold(status, "resolved") {
			outcome.result = resultResolved
		}
		return outcome
	}
	policy := e.policyFor(outcome.namespace, outcome.alertName)
	if policy == nil {
		outcome.result = resultUnmatched
		e.log.WithFields(logrus.Fields{
			"alert":     outcome.alertName,
			"namespace": outcome.namespace,
		}).Info("alert did not match a self-healing policy")
		return outcome
	}
	outcome.matched = true
	outcome.policyName = policy.Name
	outcome.model = policy.Model.Name
	repair, ok := policy.repairFor(outcome.alertName)
	if !ok {
		outcome.result = resultNoRepair
		return outcome
	}
	outcome.action = repair.Action
	if e.dedupe.seen(alert, e.now()) {
		outcome.result = resultDeduped
		return outcome
	}
	return e.scoreAndRepair(ctx, policy, repair, alert, &outcome)
}

func (e *Engine) scoreAndRepair(
	ctx context.Context,
	policy *Policy,
	repair Repair,
	alert IncomingAlert,
	outcome *alertOutcome,
) alertOutcome {
	features, err := e.featureVector(ctx, policy, alert)
	if err != nil {
		outcome.result = resultFeatureError
		outcome.err = err
		e.log.WithError(err).WithFields(outcome.fields()).Warn("policy feature query failed")
		return *outcome
	}
	verdict, err := e.score(ctx, policy, features)
	outcome.modelCalled = true
	if err != nil {
		outcome.result = resultModelError
		outcome.err = err
		e.log.WithError(err).WithFields(outcome.fields()).Warn("policy model score failed")
		return *outcome
	}
	outcome.verdict = intPtr(verdict)
	switch verdict {
	case 1:
		outcome.result = resultNoAction
		e.log.WithFields(outcome.fields()).Info("model says normal, no action")
	case -1:
		e.executeRepair(ctx, policy.Namespace, repair, alert, outcome)
	default:
		outcome.result = resultUnexpected
		e.log.WithFields(outcome.fields()).Warn("model verdict is not actionable")
	}
	return *outcome
}

func (e *Engine) executeRepair(ctx context.Context, namespace string, repair Repair, alert IncomingAlert, outcome *alertOutcome) {
	outcome.repairAttempted = true
	plan, err := e.actions.Plan(ctx, namespace, repair)
	if err != nil {
		outcome.result = resultError
		outcome.err = fmt.Errorf("%s: %w", repair.Action, err)
		e.log.WithError(outcome.err).WithFields(outcome.fields()).Error("policy repair failed")
		return
	}
	if err := e.actions.Execute(ctx, &plan); err != nil {
		outcome.err = fmt.Errorf("%s: %w", repair.Action, err)
		if errors.Is(err, errAborted) {
			outcome.result = resultAborted
			e.log.WithError(outcome.err).WithFields(outcome.fields()).Warn("policy repair aborted")
			return
		}
		outcome.result = resultError
		e.log.WithError(outcome.err).WithFields(outcome.fields()).Error("policy repair failed")
		return
	}
	outcome.repairOK = true
	outcome.result = resultSuccess
	e.dedupe.remember(alert, e.now())
	e.log.WithFields(outcome.fields()).Info("executed policy repair")
}

func (e *Engine) featureVector(ctx context.Context, policy *Policy, alert IncomingAlert) ([]float64, error) {
	scope, err := sanitizeScope(policy.Namespace, labelValue(alert.Labels, "pod"), labelValue(alert.Labels, "deployment"))
	if err != nil {
		return nil, err
	}
	features := make([]float64, 0, len(policy.Model.FeatureColumns))
	for _, column := range policy.Model.FeatureColumns {
		query, err := resolveFeatureQuery(&policy.Model, column, scope)
		if err != nil {
			return nil, err
		}
		value, err := e.querier.Query(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("query feature %s: %w", column, err)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("feature %s is not a finite number", column)
		}
		features = append(features, value)
	}
	return features, nil
}

func (e *Engine) score(ctx context.Context, policy *Policy, features []float64) (int, error) {
	endpoint, err := PredictURL(&policy.Model, e.modelNamespace, e.modelPort)
	if err != nil {
		return 0, err
	}
	verdict, err := e.scorer.Score(ctx, endpoint, features)
	if err != nil {
		return 0, fmt.Errorf("score model: %w", err)
	}
	return verdict, nil
}

func (e *Engine) policyFor(namespace, alertName string) *Policy {
	if e.loader == nil {
		return nil
	}
	doc := e.loader.Document()
	if doc == nil {
		return nil
	}
	return doc.Match(namespace, alertName)
}

type alertOutcome struct {
	matched         bool
	modelCalled     bool
	repairAttempted bool
	repairOK        bool
	policyName      string
	alertName       string
	namespace       string
	model           string
	action          string
	result          string
	verdict         *int
	err             error
}

func (o *alertOutcome) taken() ActionTaken {
	taken := ActionTaken{
		Policy:  o.policyName,
		Alert:   o.alertName,
		Model:   o.model,
		Verdict: o.verdict,
		Action:  o.action,
		Result:  o.result,
	}
	if o.err != nil {
		taken.Error = o.err.Error()
	}
	return taken
}

func (o *alertOutcome) fields() logrus.Fields {
	fields := logrus.Fields{
		"policy":    o.policyName,
		"alert":     o.alertName,
		"namespace": o.namespace,
		"action":    o.action,
	}
	if o.verdict != nil {
		fields["verdict"] = *o.verdict
	}
	return fields
}

func (o *alertOutcome) policyLabel() string {
	if o.policyName == "" {
		return nonePolicyLabel
	}
	return o.policyName
}

func (o *alertOutcome) verdictLabel() string {
	if o.verdict == nil {
		return "error"
	}
	switch *o.verdict {
	case -1:
		return "-1"
	case 1:
		return "1"
	default:
		return "other"
	}
}

func labelValue(labels map[string]string, key string) string {
	if labels == nil {
		return ""
	}
	return labels[key]
}

func alertNamespace(labels map[string]string) string {
	if namespace := labelValue(labels, "namespace"); namespace != "" {
		return namespace
	}
	return labelValue(labels, "kubernetes_namespace")
}

func intPtr(value int) *int {
	return &value
}

type deduper struct {
	mu    sync.Mutex
	items map[string]time.Time
	ttl   time.Duration
}

func newDeduper(ttl time.Duration) *deduper {
	return &deduper{items: make(map[string]time.Time), ttl: ttl}
}

func (d *deduper) seen(alert IncomingAlert, now time.Time) bool {
	key, ok := dedupeKey(alert)
	if !ok {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	expiry, ok := d.items[key]
	if !ok {
		return false
	}
	if !now.Before(expiry) {
		delete(d.items, key)
		return false
	}
	return true
}

func (d *deduper) remember(alert IncomingAlert, now time.Time) {
	key, ok := dedupeKey(alert)
	if !ok {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.items) >= maxDedupeEntries {
		d.prune(now)
	}
	d.items[key] = now.Add(d.ttl)
}

func (d *deduper) prune(now time.Time) {
	for key, expiry := range d.items {
		if !now.Before(expiry) {
			delete(d.items, key)
		}
	}
	if len(d.items) >= maxDedupeEntries {
		d.items = make(map[string]time.Time)
	}
}

func dedupeKey(alert IncomingAlert) (string, bool) {
	if alert.Fingerprint == "" || alert.StartsAt.IsZero() {
		return "", false
	}
	return alert.Fingerprint + "|" + alert.StartsAt.UTC().Format(time.RFC3339Nano), true
}
