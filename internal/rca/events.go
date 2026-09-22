package rca

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// eventClassification maps event reasons to severity + confidence + remediation.
type eventClassification struct {
	Severity         string
	BaseConfidence   float64
	RemediationSteps []string
}

var knownEventReasons = map[string]eventClassification{
	"OOMKilled": {
		Severity:       "critical",
		BaseConfidence: 0.95,
		RemediationSteps: []string{
			"Increase container memory limits",
			"Profile application for memory leaks",
			"Enable OOM-aware remediation (Issue #62)",
		},
	},
	"CrashLoopBackOff": {
		Severity:       "critical",
		BaseConfidence: 0.90,
		RemediationSteps: []string{
			"Check container logs for crash reason",
			"Verify liveness/readiness probe configuration",
			"Check for missing ConfigMaps or Secrets",
		},
	},
	"FailedScheduling": {
		Severity:       "warning",
		BaseConfidence: 0.85,
		RemediationSteps: []string{
			"Check node resource availability",
			"Review pod resource requests vs cluster capacity",
			"Verify node affinity and toleration rules",
		},
	},
	"Unhealthy": {
		Severity:       "warning",
		BaseConfidence: 0.80,
		RemediationSteps: []string{
			"Review readiness/liveness probe endpoints",
			"Check application startup time vs probe initial delay",
			"Verify service dependencies are available",
		},
	},
	"FailedMount": {
		Severity:       "warning",
		BaseConfidence: 0.85,
		RemediationSteps: []string{
			"Verify PersistentVolumeClaim exists and is bound",
			"Check StorageClass provisioner health",
			"Verify Secret or ConfigMap referenced in volume mount exists",
		},
	},
	"ImagePullBackOff": {
		Severity:       "warning",
		BaseConfidence: 0.88,
		RemediationSteps: []string{
			"Verify image name and tag are correct",
			"Check image pull secret credentials",
			"Confirm container registry is reachable",
		},
	},
	"BackOff": {
		Severity:       "warning",
		BaseConfidence: 0.75,
		RemediationSteps: []string{
			"Check container logs for startup errors",
			"Verify command and entrypoint configuration",
		},
	},
	"Evicted": {
		Severity:       "warning",
		BaseConfidence: 0.82,
		RemediationSteps: []string{
			"Check node disk pressure and memory pressure conditions",
			"Review pod priority and QoS class",
			"Consider setting resource requests to avoid eviction",
		},
	},
	"FailedCreate": {
		Severity:       "warning",
		BaseConfidence: 0.80,
		RemediationSteps: []string{
			"Check resource quota in the namespace",
			"Verify RBAC permissions for the controller",
		},
	},
}

// EventCorrelator analyses Kubernetes Events for a service in a time window.
type EventCorrelator struct {
	clientset kubernetes.Interface
	log       *logrus.Logger
}

// NewEventCorrelator creates a new event correlator.
func NewEventCorrelator(clientset kubernetes.Interface, log *logrus.Logger) *EventCorrelator {
	return &EventCorrelator{clientset: clientset, log: log}
}

// Name returns the correlator identifier.
func (ec *EventCorrelator) Name() string { return "pod_events" }

// Correlate lists events in the namespace, filters by service pods and the
// requested time window, then classifies each event into a RootCause finding.
func (ec *EventCorrelator) Correlate(req *Request) ([]RootCause, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// List all events in the namespace (the Events API does not support
	// server-side time-range filtering).
	events, err := ec.clientset.CoreV1().Events(req.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list events in namespace %s: %w", req.Namespace, err)
	}

	var findings []RootCause
	seen := make(map[string]int) // dedup key -> index in findings

	for i := range events.Items {
		ev := &events.Items[i]

		if !ec.matchesService(ev, req.Service) {
			continue
		}
		if !ec.inTimeRange(ev, req.StartTime, req.EndTime) {
			continue
		}
		if ev.Type == "Normal" {
			continue
		}

		finding, dedupKey := ec.classifyEvent(ev)
		if existing, ok := seen[dedupKey]; ok {
			// Merge: bump count in evidence
			if cnt, ok := findings[existing].Evidence["count"].(int32); ok {
				findings[existing].Evidence["count"] = cnt + eventCount(ev)
			}
			continue
		}
		seen[dedupKey] = len(findings)
		findings = append(findings, finding)
	}

	ec.log.WithFields(logrus.Fields{
		"namespace":    req.Namespace,
		"service":      req.Service,
		"findings":     len(findings),
		"total_events": len(events.Items),
	}).Info("Event correlation complete")

	return findings, nil
}

// matchesService returns true when the event's involved object belongs to the
// target service (pod name prefix match or direct service reference).
func (ec *EventCorrelator) matchesService(ev *corev1.Event, service string) bool {
	obj := ev.InvolvedObject
	switch obj.Kind {
	case "Pod":
		return strings.HasPrefix(obj.Name, service+"-")
	case "ReplicaSet":
		return strings.HasPrefix(obj.Name, service+"-")
	case "Deployment":
		return obj.Name == service
	case "Service":
		return obj.Name == service
	default:
		return false
	}
}

// inTimeRange checks whether an event falls within [start, end].
func (ec *EventCorrelator) inTimeRange(ev *corev1.Event, start, end time.Time) bool {
	ts := eventTimestamp(ev)
	return !ts.Before(start) && !ts.After(end)
}

// eventTimestamp returns the best available timestamp for an event.
func eventTimestamp(ev *corev1.Event) time.Time {
	if !ev.EventTime.IsZero() {
		return ev.EventTime.Time
	}
	if !ev.LastTimestamp.IsZero() {
		return ev.LastTimestamp.Time
	}
	return ev.CreationTimestamp.Time
}

// eventCount returns the event count, defaulting to 1.
func eventCount(ev *corev1.Event) int32 {
	if ev.Count > 0 {
		return ev.Count
	}
	return 1
}

// classifyEvent maps an event to a RootCause and a deduplication key.
func (ec *EventCorrelator) classifyEvent(ev *corev1.Event) (RootCause, string) {
	reason := ev.Reason
	cls, known := knownEventReasons[reason]
	if !known {
		cls = eventClassification{
			Severity:         "info",
			BaseConfidence:   0.50,
			RemediationSteps: []string{"Investigate event: " + reason},
		}
	}

	count := eventCount(ev)
	confidence := cls.BaseConfidence * confidenceFromCount(count)
	confidence = math.Min(confidence, 1.0)

	description := fmt.Sprintf("%s: %s on %s/%s",
		reason, ev.Message, strings.ToLower(ev.InvolvedObject.Kind), ev.InvolvedObject.Name)

	evidence := map[string]interface{}{
		"event_type": ev.Type,
		"reason":     reason,
		"count":      count,
		"message":    ev.Message,
		"severity":   cls.Severity,
		"object":     fmt.Sprintf("%s/%s", strings.ToLower(ev.InvolvedObject.Kind), ev.InvolvedObject.Name),
		"timestamp":  eventTimestamp(ev).UTC().Format(time.RFC3339),
	}

	dedupKey := fmt.Sprintf("%s/%s/%s", ev.InvolvedObject.Kind, ev.InvolvedObject.Name, reason)

	return RootCause{
		SignalType:       SignalPodEvent,
		Description:      description,
		Evidence:         evidence,
		Confidence:       math.Round(confidence*100) / 100,
		RemediationSteps: cls.RemediationSteps,
	}, dedupKey
}

// confidenceFromCount increases confidence when an event recurs.
func confidenceFromCount(count int32) float64 {
	if count <= 1 {
		return 1.0
	}
	// Logarithmic boost capped at ~1.15x for 50+ events.
	return 1.0 + 0.05*math.Log2(float64(count))
}
