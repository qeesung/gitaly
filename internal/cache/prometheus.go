package cache

import "github.com/prometheus/client_golang/prometheus"

var (
	requestTotals = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitaly_diskcache_requests_total",
			Help: "Total number of disk cache requests",
		},
	)
	missTotals = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitaly_diskcache_miss_total",
			Help: "Total number of disk cache misses",
		},
	)
	bytesStoredtotals = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitaly_diskcache_bytes_stored_total",
			Help: "Total number of disk cache bytes stored",
		},
	)
	bytesFetchedtotals = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitaly_diskcache_bytes_fetched_total",
			Help: "Total number of disk cache bytes fetched",
		},
	)
	errTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_diskcache_errors_total",
			Help: "Total number of errors encountered by disk cache",
		},
		[]string{"error"},
	)
	walkerCheckTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitaly_diskcache_walker_check_total",
			Help: "Total number of events during diskcache filesystem walks",
		},
	)
	walkerRemovalTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitaly_diskcache_walker_removal_total",
			Help: "Total number of events during diskcache filesystem walks",
		},
	)
)

func init() {
	prometheus.MustRegister(requestTotals)
	prometheus.MustRegister(missTotals)
	prometheus.MustRegister(bytesStoredtotals)
	prometheus.MustRegister(bytesFetchedtotals)
	prometheus.MustRegister(errTotal)
	prometheus.MustRegister(walkerCheckTotal)
	prometheus.MustRegister(walkerRemovalTotal)
}

func countErr(err error) error {
	switch err {
	case ErrMissingLeaseFile:
		errTotal.WithLabelValues("ErrMissingLeaseFile").Inc()
	case ErrPendingExists:
		errTotal.WithLabelValues("ErrPendingExists").Inc()
	}
	return err
}

func countRequest()             { requestTotals.Inc() }
func countMiss()                { missTotals.Inc() }
func countWriteBytes(n float64) { bytesStoredtotals.Add(n) }
func countReadBytes(n float64)  { bytesFetchedtotals.Add(n) }
func countWalkRemoval()         { walkerRemovalTotal.Inc() }
func countWalkCheck()           { walkerCheckTotal.Inc() }
