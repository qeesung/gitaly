package housekeeping

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	gitalycfgprom "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/prometheus"
)

// Metrics stores prometheus metrics of housekeeping tasks.
type Metrics struct {
	DataStructureCount                     *prometheus.HistogramVec
	DataStructureExistence                 *prometheus.CounterVec
	DataStructureSize                      *prometheus.HistogramVec
	DataStructureTimeSinceLastOptimization *prometheus.HistogramVec
	PrunedFilesTotal                       *prometheus.CounterVec
	TasksLatency                           *prometheus.HistogramVec
	TasksTotal                             *prometheus.CounterVec
}

// NewMetrics returns a new metric wrapper object.
func NewMetrics(promCfg gitalycfgprom.Config) *Metrics {
	return &Metrics{
		TasksTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_housekeeping_tasks_total",
				Help: "Total number of housekeeping tasks performed in the repository",
			},
			[]string{"housekeeping_task", "status"},
		),
		TasksLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gitaly_housekeeping_tasks_latency",
				Help:    "Latency of the housekeeping tasks performed",
				Buckets: promCfg.GRPCLatencyBuckets,
			},
			[]string{"housekeeping_task"},
		),
		PrunedFilesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_housekeeping_pruned_files_total",
				Help: "Total number of files pruned",
			},
			[]string{"filetype"},
		),
		DataStructureExistence: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_housekeeping_data_structure_existence_total",
				Help: "Total number of data structures that exist in the repository",
			},
			[]string{"data_structure", "exists"},
		),
		DataStructureCount: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gitaly_housekeeping_data_structure_count",
				Help:    "Total count of the data structures that exist in the repository",
				Buckets: prometheus.ExponentialBucketsRange(1, 10_000_000, 32),
			},
			[]string{"data_structure"},
		),
		DataStructureSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gitaly_housekeeping_data_structure_size",
				Help:    "Total size of the data structures that exist in the repository",
				Buckets: prometheus.ExponentialBucketsRange(1, 50_000_000_000, 32),
			},
			[]string{"data_structure"},
		),
		DataStructureTimeSinceLastOptimization: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "gitaly_housekeeping_time_since_last_optimization_seconds",
				Help: "Absolute time in seconds since a given optimization has last been performed",
				Buckets: []float64{
					time.Second.Seconds(),
					time.Minute.Seconds(),
					(5 * time.Minute).Seconds(),
					(10 * time.Minute).Seconds(),
					(30 * time.Minute).Seconds(),
					(1 * time.Hour).Seconds(),
					(3 * time.Hour).Seconds(),
					(6 * time.Hour).Seconds(),
					(12 * time.Hour).Seconds(),
					(18 * time.Hour).Seconds(),
					(1 * 24 * time.Hour).Seconds(),
					(2 * 24 * time.Hour).Seconds(),
					(3 * 24 * time.Hour).Seconds(),
					(5 * 24 * time.Hour).Seconds(),
					(7 * 24 * time.Hour).Seconds(),
					(14 * 24 * time.Hour).Seconds(),
					(21 * 24 * time.Hour).Seconds(),
					(28 * 24 * time.Hour).Seconds(),
				},
			},
			[]string{"data_structure"},
		),
	}
}

// Describe is used to describe Prometheus metrics.
func (m *Metrics) Describe(descs chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(m, descs)
}

// Collect is used to collect Prometheus metrics.
func (m *Metrics) Collect(metrics chan<- prometheus.Metric) {
	m.TasksTotal.Collect(metrics)
	m.TasksLatency.Collect(metrics)
	m.PrunedFilesTotal.Collect(metrics)
	m.DataStructureExistence.Collect(metrics)
	m.DataStructureCount.Collect(metrics)
	m.DataStructureSize.Collect(metrics)
	m.DataStructureTimeSinceLastOptimization.Collect(metrics)
}

// ReportRepositoryInfo reports the repository info in the form of prometheus metrics.
func (m *Metrics) ReportRepositoryInfo(info stats.RepositoryInfo) {
	m.reportDataStructureExistence("commit_graph", info.CommitGraph.Exists)
	m.reportDataStructureExistence("commit_graph_bloom_filters", info.CommitGraph.HasBloomFilters)
	m.reportDataStructureExistence("commit_graph_generation_data", info.CommitGraph.HasGenerationData)
	m.reportDataStructureExistence("commit_graph_generation_data_overflow", info.CommitGraph.HasGenerationDataOverflow)
	m.reportDataStructureExistence("bitmap", info.Packfiles.Bitmap.Exists)
	m.reportDataStructureExistence("bitmap_hash_cache", info.Packfiles.Bitmap.HasHashCache)
	m.reportDataStructureExistence("bitmap_lookup_table", info.Packfiles.Bitmap.HasLookupTable)
	m.reportDataStructureExistence("multi_pack_index", info.Packfiles.MultiPackIndex.Exists)
	m.reportDataStructureExistence("multi_pack_index_bitmap", info.Packfiles.MultiPackIndexBitmap.Exists)
	m.reportDataStructureExistence("multi_pack_index_bitmap_hash_cache", info.Packfiles.MultiPackIndexBitmap.HasHashCache)
	m.reportDataStructureExistence("multi_pack_index_bitmap_lookup_table", info.Packfiles.MultiPackIndexBitmap.HasLookupTable)

	m.reportDataStructureCount("loose_objects_recent", info.LooseObjects.Count-info.LooseObjects.StaleCount)
	m.reportDataStructureCount("loose_objects_stale", info.LooseObjects.StaleCount)
	m.reportDataStructureCount("commit_graph_chain", info.CommitGraph.CommitGraphChainLength)
	m.reportDataStructureCount("packfiles", info.Packfiles.Count)
	m.reportDataStructureCount("packfiles_cruft", info.Packfiles.CruftCount)
	m.reportDataStructureCount("packfiles_keep", info.Packfiles.KeepCount)
	m.reportDataStructureCount("packfiles_reverse_indices", info.Packfiles.ReverseIndexCount)
	m.reportDataStructureCount("multi_pack_index_packfile_count", info.Packfiles.MultiPackIndex.PackfileCount)
	m.reportDataStructureCount("loose_references", info.References.LooseReferencesCount)

	m.reportDataStructureSize("loose_objects_recent", info.LooseObjects.Size-info.LooseObjects.StaleSize)
	m.reportDataStructureSize("loose_objects_stale", info.LooseObjects.StaleSize)
	m.reportDataStructureSize("packfiles", info.Packfiles.Size)
	m.reportDataStructureSize("packfiles_cruft", info.Packfiles.CruftSize)
	m.reportDataStructureSize("packfiles_keep", info.Packfiles.KeepSize)
	m.reportDataStructureSize("packed_references", info.References.PackedReferencesSize)

	now := time.Now()
	m.DataStructureTimeSinceLastOptimization.WithLabelValues("packfiles_full_repack").Observe(now.Sub(info.Packfiles.LastFullRepack).Seconds())
}

func (m *Metrics) reportDataStructureExistence(dataStructure string, exists bool) {
	m.DataStructureExistence.WithLabelValues(dataStructure, strconv.FormatBool(exists)).Inc()
	// We also report the inverse metric so that it will be visible to clients.
	m.DataStructureExistence.WithLabelValues(dataStructure, strconv.FormatBool(!exists)).Add(0)
}

func (m *Metrics) reportDataStructureCount(dataStructure string, count uint64) {
	m.DataStructureCount.WithLabelValues(dataStructure).Observe(float64(count))
}

func (m *Metrics) reportDataStructureSize(dataStructure string, size uint64) {
	m.DataStructureSize.WithLabelValues(dataStructure).Observe(float64(size))
}
