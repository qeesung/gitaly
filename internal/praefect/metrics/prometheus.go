package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	promconfig "gitlab.com/gitlab-org/gitaly/internal/config/prometheus"
)

// RegisterReplicationLatency creates and registers a prometheus histogram
// to observe replication latency times
func RegisterReplicationLatency(conf promconfig.Config) (Histogram, error) {
	replicationLatency := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "replication_latency",
			Buckets:   conf.GRPCLatencyBuckets,
		},
	)

	return replicationLatency, prometheus.Register(replicationLatency)
}

// RegisterReplicationJobsInFlight creates and registers a gauge
// to track the size of the replication queue
func RegisterReplicationJobsInFlight() (Gauge, error) {
	replicationJobsInFlight := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "replication_jobs",
		},
	)
	return replicationJobsInFlight, prometheus.Register(replicationJobsInFlight)
}

var MethodTypeCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "gitaly",
		Subsystem: "praefect",
		Name:      "method_types",
	}, []string{"method_type"},
)

var PrimaryGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "gitaly",
		Subsystem: "praefect",
		Name:      "primaries",
	}, []string{"virtual_storage", "gitaly_storage"},
)

func init() {
	prometheus.MustRegister(MethodTypeCounter)
	prometheus.MustRegister(PrimaryGauge)
}

// Gauge is a subset of a prometheus Gauge
type Gauge interface {
	Inc()
	Dec()
}

// Histogram is a subset of a prometheus Histogram
type Histogram interface {
	Observe(float64)
}
