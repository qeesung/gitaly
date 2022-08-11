package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	gitalycfgprom "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/prometheus/metrics"
)

// RegisterReplicationDelay creates and registers a prometheus histogram
// to observe replication delay times
func RegisterReplicationDelay(conf gitalycfgprom.Config, registerer prometheus.Registerer) (metrics.HistogramVec, error) {
	replicationDelay := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "replication_delay",
			Buckets:   conf.GRPCLatencyBuckets,
		},
		[]string{"type"},
	)

	return replicationDelay, registerer.Register(replicationDelay)
}

// RegisterReplicationLatency creates and registers a prometheus histogram
// to observe replication latency times
func RegisterReplicationLatency(conf gitalycfgprom.Config, registerer prometheus.Registerer) (metrics.HistogramVec, error) {
	replicationLatency := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "replication_latency",
			Buckets:   conf.GRPCLatencyBuckets,
		},
		[]string{"type"},
	)

	return replicationLatency, registerer.Register(replicationLatency)
}

// RegisterNodeLatency creates and registers a prometheus histogram to
// observe internal node latency
func RegisterNodeLatency(conf gitalycfgprom.Config, registerer prometheus.Registerer) (metrics.HistogramVec, error) {
	nodeLatency := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "node_latency",
			Buckets:   conf.GRPCLatencyBuckets,
		}, []string{"gitaly_storage"},
	)

	return nodeLatency, registerer.Register(nodeLatency)
}

//nolint: stylecheck // This is unintentionally missing documentation.
var MethodTypeCounter = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "gitaly",
		Subsystem: "praefect",
		Name:      "method_types",
	}, []string{"method_type"},
)

//nolint: stylecheck // This is unintentionally missing documentation.
var PrimaryGauge = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "gitaly",
		Subsystem: "praefect",
		Name:      "primaries",
	}, []string{"virtual_storage", "gitaly_storage"},
)

//nolint: stylecheck // This is unintentionally missing documentation.
var NodeLastHealthcheckGauge = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "gitaly",
		Subsystem: "praefect",
		Name:      "node_last_healthcheck_up",
	}, []string{"gitaly_storage"},
)

// ReadDistribution counts how many read operations was routed to each storage.
var ReadDistribution = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "gitaly",
		Subsystem: "praefect",
		Name:      "read_distribution",
		Help:      "Counts read operations directed to the storages",
	},
	[]string{"virtual_storage", "storage"},
)
