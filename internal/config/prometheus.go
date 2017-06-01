package config

import (
	"github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// ConfigurePrometheus uses the global configuration to configure prometheus
func ConfigurePrometheus() {
	if len(Config.Prometheus.GRPCLatencyBuckets) == 0 {
		return
	}

	log.WithField("latencies", Config.Prometheus.GRPCLatencyBuckets).Debug("grpc prometheus histograms enabled")

	// After all your registrations, make sure all of the Prometheus metrics are initialized.
	grpc_prometheus.EnableHandlingTimeHistogram(func(histogramOpts *prometheus.HistogramOpts) {
		histogramOpts.Buckets = Config.Prometheus.GRPCLatencyBuckets
	})
}
