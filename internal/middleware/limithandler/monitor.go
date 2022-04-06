package limithandler

import (
	"context"
	"strings"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/prometheus/client_golang/prometheus"
)

const acquireDurationLogThreshold = 10 * time.Millisecond

// ConcurrencyMonitor allows the concurrency monitor to be observed
type ConcurrencyMonitor interface {
	Queued(ctx context.Context)
	Dequeued(ctx context.Context)
	Enter(ctx context.Context, acquireTime time.Duration)
	Exit(ctx context.Context)
	Dropped(ctx context.Context, message string)
}

type nullConcurrencyMonitor struct{}

func (c *nullConcurrencyMonitor) Queued(ctx context.Context)                           {}
func (c *nullConcurrencyMonitor) Dequeued(ctx context.Context)                         {}
func (c *nullConcurrencyMonitor) Enter(ctx context.Context, acquireTime time.Duration) {}
func (c *nullConcurrencyMonitor) Exit(ctx context.Context)                             {}
func (c *nullConcurrencyMonitor) Dropped(ctx context.Context, reason string)           {}

type promMonitor struct {
	queuedMetric           prometheus.Gauge
	inProgressMetric       prometheus.Gauge
	acquiringSecondsMetric prometheus.Observer
	requestsDroppedMetric  *prometheus.CounterVec
}

// newPromMonitor creates a new ConcurrencyMonitor that tracks limiter
// activity in Prometheus.
func newPromMonitor(
	system, fullMethod string,
	queuedMetric, inProgressMetric *prometheus.GaugeVec,
	acquiringSecondsMetric prometheus.ObserverVec,
	requestsDroppedMetric *prometheus.CounterVec,
) ConcurrencyMonitor {
	serviceName, methodName := splitMethodName(fullMethod)

	return &promMonitor{
		queuedMetric.WithLabelValues(system, serviceName, methodName),
		inProgressMetric.WithLabelValues(system, serviceName, methodName),
		acquiringSecondsMetric.WithLabelValues(system, serviceName, methodName),
		requestsDroppedMetric.MustCurryWith(prometheus.Labels{
			"system":       system,
			"grpc_service": serviceName,
			"grpc_method":  methodName,
		}),
	}
}

func (c *promMonitor) Queued(ctx context.Context) {
	c.queuedMetric.Inc()
}

func (c *promMonitor) Dequeued(ctx context.Context) {
	c.queuedMetric.Dec()
}

func (c *promMonitor) Enter(ctx context.Context, acquireTime time.Duration) {
	c.inProgressMetric.Inc()

	if acquireTime > acquireDurationLogThreshold {
		logger := ctxlogrus.Extract(ctx)
		logger.WithField("acquire_ms", acquireTime.Seconds()*1000).Info("Rate limit acquire wait")
	}

	c.acquiringSecondsMetric.Observe(acquireTime.Seconds())
}

func (c *promMonitor) Exit(ctx context.Context) {
	c.inProgressMetric.Dec()
}

func (c *promMonitor) Dropped(ctx context.Context, reason string) {
	c.requestsDroppedMetric.WithLabelValues(reason).Inc()
}

func splitMethodName(fullMethodName string) (string, string) {
	fullMethodName = strings.TrimPrefix(fullMethodName, "/") // remove leading slash
	if i := strings.Index(fullMethodName, "/"); i >= 0 {
		return fullMethodName[:i], fullMethodName[i+1:]
	}
	return "unknown", "unknown"
}
