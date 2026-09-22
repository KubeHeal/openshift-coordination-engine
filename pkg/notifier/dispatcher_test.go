package notifier

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSink implements AlertSink for testing.
type mockSink struct {
	name   string
	sendFn func(ctx context.Context, alert *Alert) error
	called atomic.Int32
	mu     sync.Mutex
	alerts []*Alert
}

func (m *mockSink) Name() string { return m.name }

func (m *mockSink) Send(ctx context.Context, alert *Alert) error {
	m.called.Add(1)
	m.mu.Lock()
	m.alerts = append(m.alerts, alert)
	m.mu.Unlock()
	if m.sendFn != nil {
		return m.sendFn(ctx, alert)
	}
	return nil
}

func (m *mockSink) callCount() int {
	return int(m.called.Load())
}

func (m *mockSink) receivedAlerts() []*Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*Alert, len(m.alerts))
	copy(cp, m.alerts)
	return cp
}

func TestDispatcher_NoSinks(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	d := NewDispatcher(nil, log)
	assert.Equal(t, 0, d.SinkCount())

	// Should not panic
	d.Dispatch(newTestAlert())
}

func TestDispatcher_FanOut(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	sink1 := &mockSink{name: "sink1"}
	sink2 := &mockSink{name: "sink2"}
	sink3 := &mockSink{name: "sink3"}

	d := NewDispatcher([]AlertSink{sink1, sink2, sink3}, log)
	assert.Equal(t, 3, d.SinkCount())

	d.Dispatch(newTestAlert())

	// Wait for goroutines to complete
	require.Eventually(t, func() bool {
		return sink1.callCount() == 1 && sink2.callCount() == 1 && sink3.callCount() == 1
	}, 2*time.Second, 10*time.Millisecond)

	alerts1 := sink1.receivedAlerts()
	require.Len(t, alerts1, 1)
	assert.Equal(t, "critical", alerts1[0].Severity)
	assert.Equal(t, "my-app", alerts1[0].Service)
}

func TestDispatcher_PartialFailure(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	successSink := &mockSink{name: "success"}
	failSink := &mockSink{
		name: "fail",
		sendFn: func(_ context.Context, _ *Alert) error {
			return fmt.Errorf("connection refused")
		},
	}

	d := NewDispatcher([]AlertSink{successSink, failSink}, log)

	d.Dispatch(newTestAlert())

	// Both sinks should be called even though one fails
	require.Eventually(t, func() bool {
		return successSink.callCount() == 1 && failSink.callCount() == 1
	}, 2*time.Second, 10*time.Millisecond)

	// The successful sink should have received the alert
	alerts := successSink.receivedAlerts()
	require.Len(t, alerts, 1)
}

func TestDispatcher_MultipleDispatches(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	sink := &mockSink{name: "counter"}
	d := NewDispatcher([]AlertSink{sink}, log)

	for i := 0; i < 5; i++ {
		d.Dispatch(newTestAlert())
	}

	require.Eventually(t, func() bool {
		return sink.callCount() == 5
	}, 2*time.Second, 10*time.Millisecond)
}

func TestDispatcher_EmptySinks(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	d := NewDispatcher([]AlertSink{}, log)
	assert.Equal(t, 0, d.SinkCount())

	// Should not panic
	d.Dispatch(newTestAlert())
}
