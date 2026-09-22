package rca

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func testLog() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)
	return log
}

func TestEventCorrelator_OOMKilled(t *testing.T) {
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myapp-abc.oom", Namespace: "prod",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Name: "myapp-abc", Namespace: "prod",
		},
		Reason:  "OOMKilled",
		Message: "Container exceeded memory limit",
		Type:    "Warning",
		Count:   5,
		LastTimestamp: metav1.Time{
			Time: time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC),
		},
	}

	clientset := fake.NewSimpleClientset(ev)
	corr := NewEventCorrelator(clientset, testLog())

	findings, err := corr.Correlate(&Request{
		Service:   "myapp",
		Namespace: "prod",
		StartTime: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC),
	})

	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, SignalPodEvent, findings[0].SignalType)
	assert.Contains(t, findings[0].Description, "OOMKilled")
	assert.Greater(t, findings[0].Confidence, 0.90)
	assert.Contains(t, findings[0].RemediationSteps, "Increase container memory limits")
}

func TestEventCorrelator_CrashLoopBackOff(t *testing.T) {
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myapp-xyz.crash", Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Name: "myapp-xyz", Namespace: "default",
		},
		Reason:  "CrashLoopBackOff",
		Message: "Back-off restarting failed container",
		Type:    "Warning",
		Count:   10,
		LastTimestamp: metav1.Time{
			Time: time.Date(2026, 9, 22, 10, 15, 0, 0, time.UTC),
		},
	}

	clientset := fake.NewSimpleClientset(ev)
	corr := NewEventCorrelator(clientset, testLog())

	findings, err := corr.Correlate(&Request{
		Service:   "myapp",
		Namespace: "default",
		StartTime: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC),
	})

	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Description, "CrashLoopBackOff")
}

func TestEventCorrelator_FiltersOutOfRange(t *testing.T) {
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myapp-abc.old", Namespace: "prod",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Name: "myapp-abc", Namespace: "prod",
		},
		Reason:  "OOMKilled",
		Message: "Container exceeded memory limit",
		Type:    "Warning",
		Count:   1,
		LastTimestamp: metav1.Time{
			Time: time.Date(2026, 9, 21, 5, 0, 0, 0, time.UTC), // yesterday
		},
	}

	clientset := fake.NewSimpleClientset(ev)
	corr := NewEventCorrelator(clientset, testLog())

	findings, err := corr.Correlate(&Request{
		Service:   "myapp",
		Namespace: "prod",
		StartTime: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC),
	})

	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestEventCorrelator_FiltersNormalEvents(t *testing.T) {
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myapp-abc.scheduled", Namespace: "prod",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Name: "myapp-abc", Namespace: "prod",
		},
		Reason:  "Scheduled",
		Message: "Successfully assigned pod",
		Type:    "Normal",
		Count:   1,
		LastTimestamp: metav1.Time{
			Time: time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC),
		},
	}

	clientset := fake.NewSimpleClientset(ev)
	corr := NewEventCorrelator(clientset, testLog())

	findings, err := corr.Correlate(&Request{
		Service:   "myapp",
		Namespace: "prod",
		StartTime: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC),
	})

	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestEventCorrelator_FiltersUnrelatedService(t *testing.T) {
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: "other-abc.oom", Namespace: "prod",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Name: "other-abc", Namespace: "prod",
		},
		Reason:  "OOMKilled",
		Message: "Container exceeded memory limit",
		Type:    "Warning",
		Count:   1,
		LastTimestamp: metav1.Time{
			Time: time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC),
		},
	}

	clientset := fake.NewSimpleClientset(ev)
	corr := NewEventCorrelator(clientset, testLog())

	findings, err := corr.Correlate(&Request{
		Service:   "myapp",
		Namespace: "prod",
		StartTime: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC),
	})

	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestEventCorrelator_DeduplicatesSameReason(t *testing.T) {
	ev1 := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myapp-abc.oom1", Namespace: "prod",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Name: "myapp-abc", Namespace: "prod",
		},
		Reason:  "OOMKilled",
		Message: "Container exceeded memory limit",
		Type:    "Warning",
		Count:   2,
		LastTimestamp: metav1.Time{
			Time: time.Date(2026, 9, 22, 10, 20, 0, 0, time.UTC),
		},
	}
	ev2 := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myapp-abc.oom2", Namespace: "prod",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Name: "myapp-abc", Namespace: "prod",
		},
		Reason:  "OOMKilled",
		Message: "Container exceeded memory limit (again)",
		Type:    "Warning",
		Count:   3,
		LastTimestamp: metav1.Time{
			Time: time.Date(2026, 9, 22, 10, 40, 0, 0, time.UTC),
		},
	}

	clientset := fake.NewSimpleClientset(ev1, ev2)
	corr := NewEventCorrelator(clientset, testLog())

	findings, err := corr.Correlate(&Request{
		Service:   "myapp",
		Namespace: "prod",
		StartTime: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC),
	})

	require.NoError(t, err)
	require.Len(t, findings, 1, "duplicate events for same pod+reason should be merged")
}

func TestEventCorrelator_EmptyNamespace(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	corr := NewEventCorrelator(clientset, testLog())

	findings, err := corr.Correlate(&Request{
		Service:   "myapp",
		Namespace: "empty-ns",
		StartTime: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC),
	})

	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestConfidenceFromCount(t *testing.T) {
	assert.Equal(t, 1.0, confidenceFromCount(1))
	assert.Greater(t, confidenceFromCount(10), 1.0)
	assert.Less(t, confidenceFromCount(100), 1.40) // capped boost
}
