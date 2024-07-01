package storagemgr

import (
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	gitalycfgprom "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr/snapshot"
)

type metrics struct {
	// housekeeping accounts for housekeeping task metrics.
	housekeeping *housekeeping.Metrics
	// snapshot contains snapshotting related metrics.
	snapshot snapshot.Metrics
}

func newMetrics(promCfg gitalycfgprom.Config) *metrics {
	return &metrics{
		housekeeping: housekeeping.NewMetrics(promCfg),
		snapshot:     snapshot.NewMetrics(),
	}
}

// Describe is used to describe Prometheus metrics.
func (m *metrics) Describe(metrics chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(m, metrics)
}

// Collect is used to collect Prometheus metrics.
func (m *metrics) Collect(metrics chan<- prometheus.Metric) {
	m.housekeeping.Collect(metrics)
	m.snapshot.Collect(metrics)
}
