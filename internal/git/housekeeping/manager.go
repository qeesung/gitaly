package housekeeping

import (
	"context"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/localrepo"
	gitalycfgprom "gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/transaction"
)

// Manager is a housekeeping manager. It is supposed to handle housekeeping tasks for repositories
// such as the cleanup of unneeded files and optimizations for the repository's data structures.
type Manager interface {
	// CleanStaleData removes any stale data in the repository.
	CleanStaleData(context.Context, *localrepo.Repo) error
	// OptimizeRepository optimizes the repository's data structures such that it can be more
	// efficiently served.
	OptimizeRepository(context.Context, *localrepo.Repo) error
}

// RepositoryManager is an implementation of the Manager interface.
type RepositoryManager struct {
	txManager transaction.Manager

	tasksTotal       *prometheus.CounterVec
	tasksLatency     *prometheus.HistogramVec
	prunedFilesTotal *prometheus.CounterVec
	optimizeFunc     func(ctx context.Context, m *RepositoryManager, repo *localrepo.Repo) error
	reposInProgress  sync.Map
}

// NewManager creates a new RepositoryManager.
func NewManager(promCfg gitalycfgprom.Config, txManager transaction.Manager) *RepositoryManager {
	return &RepositoryManager{
		txManager: txManager,

		tasksTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_housekeeping_tasks_total",
				Help: "Total number of housekeeping tasks performed in the repository",
			},
			[]string{"housekeeping_task", "status"},
		),
		tasksLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gitaly_housekeeping_tasks_latency",
				Help:    "Latency of the housekeeping tasks performed",
				Buckets: promCfg.GRPCLatencyBuckets,
			},
			[]string{"housekeeping_task"},
		),
		prunedFilesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_housekeeping_pruned_files_total",
				Help: "Total number of files pruned",
			},
			[]string{"filetype"},
		),
		optimizeFunc: optimizeRepository,
	}
}

// Describe is used to describe Prometheus metrics.
func (m *RepositoryManager) Describe(descs chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(m, descs)
}

// Collect is used to collect Prometheus metrics.
func (m *RepositoryManager) Collect(metrics chan<- prometheus.Metric) {
	m.tasksTotal.Collect(metrics)
	m.tasksLatency.Collect(metrics)
	m.prunedFilesTotal.Collect(metrics)
}
