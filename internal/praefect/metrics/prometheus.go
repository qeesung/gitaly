package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	promconfig "gitlab.com/gitlab-org/gitaly/internal/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/prometheus/metrics"
)

// RegisterReplicationDelay creates and registers a prometheus histogram
// to observe replication delay times
func RegisterReplicationDelay(conf promconfig.Config) (metrics.HistogramVec, error) {
	replicationDelay := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "replication_delay",
			Buckets:   conf.GRPCLatencyBuckets,
		},
		[]string{"type"},
	)

	return replicationDelay, prometheus.Register(replicationDelay)
}

// RegisterReplicationLatency creates and registers a prometheus histogram
// to observe replication latency times
func RegisterReplicationLatency(conf promconfig.Config) (metrics.HistogramVec, error) {
	replicationLatency := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "replication_latency",
			Buckets:   conf.GRPCLatencyBuckets,
		},
		[]string{"type"},
	)

	return replicationLatency, prometheus.Register(replicationLatency)
}

// RegisterNodeLatency creates and registers a prometheus histogram to
// observe internal node latency
func RegisterNodeLatency(conf promconfig.Config) (metrics.HistogramVec, error) {
	nodeLatency := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "node_latency",
			Buckets:   conf.GRPCLatencyBuckets,
		}, []string{"gitaly_storage"},
	)

	return nodeLatency, prometheus.Register(nodeLatency)
}

// RegisterTransactionCounter creates and registers a Prometheus counter to
// track the number of transactions and their outcomes.
func RegisterTransactionCounter() (*prometheus.CounterVec, error) {
	transactionCounter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "transactions_total",
		},
		[]string{"action"},
	)
	return transactionCounter, prometheus.Register(transactionCounter)
}

// RegisterTransactionDelay creates and registers a Prometheus histogram to
// track the delay of actions performed on transactions.
func RegisterTransactionDelay(conf promconfig.Config) (metrics.HistogramVec, error) {
	transactionDelay := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "transactions_delay_seconds",
			Buckets:   conf.GRPCLatencyBuckets,
		},
		[]string{"action"},
	)
	return transactionDelay, prometheus.Register(transactionDelay)
}

// RegisterSubtransactionsHistogram creates and registers a Prometheus counter to
// gauge the number of subtransactions per transaction.
func RegisterSubtransactionsHistogram() (metrics.Histogram, error) {
	subtransactionsHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "subtransactions_per_transaction_total",
		},
	)
	return subtransactionsHistogram, prometheus.Register(subtransactionsHistogram)
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

var ReadOnlyGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "gitaly_praefect_read_only_mode",
		Help: "Shows whether a virtual storage is in read-only mode.",
	}, []string{"virtual_storage"},
)

var NodeLastHealthcheckGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "gitaly",
		Subsystem: "praefect",
		Name:      "node_last_healthcheck_up",
	}, []string{"gitaly_storage"},
)

var ChecksumMismatchCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "gitaly",
		Subsystem: "praefect",
		Name:      "checksum_mismatch_total",
	}, []string{"target", "source"},
)

// ReadDistribution counts how many read operations was routed to each storage.
var ReadDistribution = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "gitaly",
		Subsystem: "praefect",
		Name:      "read_distribution",
		Help:      "Counts read operations directed to the storages",
	},
	[]string{"virtual_storage", "storage"},
)

func init() {
	prometheus.MustRegister(
		MethodTypeCounter,
		PrimaryGauge,
		ReadOnlyGauge,
		ChecksumMismatchCounter,
		NodeLastHealthcheckGauge,
		ReadDistribution,
	)
}
