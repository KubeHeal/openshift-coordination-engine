package notifier

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
)

// Dispatcher fans out alerts to all registered sinks asynchronously.
// Sink errors are logged but never propagated to the caller.
type Dispatcher struct {
	sinks []AlertSink
	log   *logrus.Logger
}

// NewDispatcher creates a dispatcher with the given sinks.
// Nil or empty sinks are valid — Dispatch becomes a no-op.
func NewDispatcher(sinks []AlertSink, log *logrus.Logger) *Dispatcher {
	return &Dispatcher{sinks: sinks, log: log}
}

// Dispatch sends the alert to every registered sink in its own goroutine.
// It returns immediately and never blocks the caller.
func (d *Dispatcher) Dispatch(alert *Alert) {
	if len(d.sinks) == 0 {
		return
	}

	for _, s := range d.sinks {
		go func(sink AlertSink) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := sink.Send(ctx, alert); err != nil {
				d.log.WithError(err).WithFields(logrus.Fields{
					"sink":     sink.Name(),
					"severity": alert.Severity,
					"service":  alert.Service,
				}).Warn("Alert sink delivery failed")
			} else {
				d.log.WithFields(logrus.Fields{
					"sink":     sink.Name(),
					"severity": alert.Severity,
					"service":  alert.Service,
				}).Info("Alert dispatched successfully")
			}
		}(s)
	}
}

// SinkCount returns the number of registered sinks.
func (d *Dispatcher) SinkCount() int {
	return len(d.sinks)
}
