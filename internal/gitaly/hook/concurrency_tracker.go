package hook

import (
	"context"
	"sync"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/prometheus/client_golang/prometheus"
)

// NewConcurrencyTracker creates a new ConcurrencyTracker.
func NewConcurrencyTracker() *ConcurrencyTracker {
	c := &ConcurrencyTracker{
		concurrencyMap: make(map[string]int),
		currentCallersVec: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gitaly_pack_objects_process_active_callers",
				Help: "Number of unique callers that have an active pack objects processes",
			},
			[]string{"segment"},
		),
		totalCallersVec: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_pack_objects_process_active_callers_total",
				Help: "Total unique callers that have initiated a pack objects processes",
			},
			[]string{"segment"},
		),
		concurrentProcessesVec: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gitaly_pack_objects_concurrent_processes",
				Help:    "Number of concurrent processes",
				Buckets: prometheus.LinearBuckets(0, 5, 20),
			},
			[]string{"segment"},
		),
	}

	return c
}

// ConcurrencyTracker tracks concurrency of pack object calls
type ConcurrencyTracker struct {
	lock                   sync.Mutex
	concurrencyMap         map[string]int
	currentCallersVec      *prometheus.GaugeVec
	totalCallersVec        *prometheus.CounterVec
	concurrentProcessesVec *prometheus.HistogramVec
}

func (c *ConcurrencyTracker) addConcurrencyDelta(compositeKey string, delta int) int {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.concurrencyMap[compositeKey] += delta

	if c.concurrencyMap[compositeKey] == 0 {
		delete(c.concurrencyMap, compositeKey)
	}

	return c.concurrencyMap[compositeKey]
}

// LogConcurrency logs the number of concurrent calls for a keyType, key
// combination
func (c *ConcurrencyTracker) LogConcurrency(ctx context.Context, keyType, key string) func() {
	compositeKey := keyType + ":" + key

	concurrency := c.addConcurrencyDelta(compositeKey, 1)
	if concurrency == 1 {
		// If there are no entries in the map for this keyType, key
		// combination, it means this is the first pack objects process
		// for this keyType, key. Hence, we increment the total number
		// of active callers for this keyType as this is a newly active
		// caller of a pack object process. In finish, when the pack
		// object process finishes, we check if it is the only active
		// call and if so, we decrement this metric since that means the
		// keyType, key caller is no longer responsible for any pack
		c.currentCallersVec.WithLabelValues(keyType).Inc()
		c.totalCallersVec.WithLabelValues(keyType).Inc()
	}

	c.concurrentProcessesVec.WithLabelValues(
		keyType,
	).Observe(float64(concurrency))

	ctxlogrus.Extract(ctx).
		WithField("concurrency_type", keyType).
		WithField("concurrency_key", key).
		WithField("concurrency", concurrency).
		Info("concurrency")

	return func() {
		c.finish(keyType, compositeKey)
	}
}

func (c *ConcurrencyTracker) finish(keyType, compositeKey string) {
	if c.addConcurrencyDelta(compositeKey, -1) == 0 {
		c.currentCallersVec.WithLabelValues(keyType).Dec()
	}
}

// Collect allows ConcurrencyTracker to adhere to the prometheus.Collector
// interface for collecting metrics.
func (c *ConcurrencyTracker) Collect(ch chan<- prometheus.Metric) {
	c.currentCallersVec.Collect(ch)
	c.totalCallersVec.Collect(ch)
	c.concurrentProcessesVec.Collect(ch)
}

// Describe allows ConcurrencyTracker to adhere to the prometheus.Collector
// interface for collecing metrics
func (c *ConcurrencyTracker) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, ch)
}
