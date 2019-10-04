package command

import "github.com/prometheus/client_golang/prometheus"

var inFlightCommandGauge = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: "gitaly_commands_running",
		Help: "Total number of processes currently being executed",
	},
)

func init() {
	prometheus.MustRegister(inFlightCommandGauge)
}
