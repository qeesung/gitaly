package snapshot

import (
	"github.com/prometheus/client_golang/prometheus"
)

// GlobalMetrics contains the global counters that haven't yet
// been scoped for a specific Manager.
type GlobalMetrics struct {
	createdExclusiveSnapshotTotal   *prometheus.CounterVec
	destroyedExclusiveSnapshotTotal *prometheus.CounterVec
	createdSharedSnapshotTotal      *prometheus.CounterVec
	reusedSharedSnapshotTotal       *prometheus.CounterVec
	destroyedSharedSnapshotTotal    *prometheus.CounterVec
}

// Describe implements prometheus.Collector.
func (m GlobalMetrics) Describe(descs chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(m, descs)
}

// Collect implements prometheus.Collector.
func (m GlobalMetrics) Collect(metrics chan<- prometheus.Metric) {
	m.createdExclusiveSnapshotTotal.Collect(metrics)
	m.destroyedExclusiveSnapshotTotal.Collect(metrics)
	m.createdSharedSnapshotTotal.Collect(metrics)
	m.reusedSharedSnapshotTotal.Collect(metrics)
	m.destroyedSharedSnapshotTotal.Collect(metrics)
}

// NewGlobalMetrics returns a new GlobalMetrics instance.
func NewGlobalMetrics() GlobalMetrics {
	labels := []string{"storage"}
	return GlobalMetrics{
		createdExclusiveSnapshotTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gitaly_exclusive_snapshots_created_total",
			Help: "Number of created exclusive snapshots.",
		}, labels),
		destroyedExclusiveSnapshotTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gitaly_exclusive_snapshots_destroyed_total",
			Help: "Number of destroyed exclusive snapshots.",
		}, labels),
		createdSharedSnapshotTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gitaly_shared_snapshots_created_total",
			Help: "Number of created shared snapshots.",
		}, labels),
		reusedSharedSnapshotTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gitaly_shared_snapshots_reused_total",
			Help: "Number of reused shared snapshots.",
		}, labels),
		destroyedSharedSnapshotTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gitaly_shared_snapshots_destroyed_total",
			Help: "Number of destroyed shared snapshots.",
		}, labels),
	}
}

// Metrics contains the metrics supported by the Manager.
type Metrics struct {
	createdExclusiveSnapshotTotal   prometheus.Counter
	destroyedExclusiveSnapshotTotal prometheus.Counter
	createdSharedSnapshotTotal      prometheus.Counter
	reusedSharedSnapshotTotal       prometheus.Counter
	destroyedSharedSnapshotTotal    prometheus.Counter
}

// Scope returns the metrics scoped for a given Manager.
func (m GlobalMetrics) Scope(storageName string) Metrics {
	labels := prometheus.Labels{"storage": storageName}
	return Metrics{
		createdExclusiveSnapshotTotal:   m.createdExclusiveSnapshotTotal.With(labels),
		destroyedExclusiveSnapshotTotal: m.destroyedExclusiveSnapshotTotal.With(labels),
		createdSharedSnapshotTotal:      m.createdSharedSnapshotTotal.With(labels),
		reusedSharedSnapshotTotal:       m.reusedSharedSnapshotTotal.With(labels),
		destroyedSharedSnapshotTotal:    m.destroyedSharedSnapshotTotal.With(labels),
	}
}
