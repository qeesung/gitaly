package datastore

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore/glsql"
)

var (
	// This is kept for backwards compatibility as some alerting rules depend on this.
	// The unavailable repositories is a more accurate description for the metric and
	// is exported below so we can migrate to it.
	descReadOnlyRepositories = prometheus.NewDesc(
		"gitaly_praefect_read_only_repositories",
		"Number of repositories in read-only mode within a virtual storage.",
		[]string{"virtual_storage"},
		nil,
	)

	descUnavailableRepositories = prometheus.NewDesc(
		"gitaly_praefect_unavailable_repositories",
		"Number of repositories that have no healthy, up to date replicas.",
		[]string{"virtual_storage"},
		nil,
	)

	descReplicationQueueDepth = prometheus.NewDesc(
		"gitaly_praefect_replication_queue_depth",
		"Number of jobs in the replication queue",
		[]string{"virtual_storage", "target_node", "state"},
		nil,
	)

	descriptions = []*prometheus.Desc{
		descReadOnlyRepositories,
		descUnavailableRepositories,
	}
)

// RepositoryStoreCollector collects metrics from the RepositoryStore.
type RepositoryStoreCollector struct {
	log             logrus.FieldLogger
	db              glsql.Querier
	virtualStorages []string
	timeout         time.Duration
}

// NewRepositoryStoreCollector returns a new collector.
func NewRepositoryStoreCollector(
	log logrus.FieldLogger,
	virtualStorages []string,
	db glsql.Querier,
	timeout time.Duration,
) *RepositoryStoreCollector {
	return &RepositoryStoreCollector{
		log:             log.WithField("component", "RepositoryStoreCollector"),
		db:              db,
		virtualStorages: virtualStorages,
		timeout:         timeout,
	}
}

//nolint: stylecheck // This is unintentionally missing documentation.
func (c *RepositoryStoreCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range descriptions {
		ch <- desc
	}
}

//nolint: stylecheck // This is unintentionally missing documentation.
func (c *RepositoryStoreCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.TODO(), c.timeout)
	defer cancel()

	unavailableCounts, err := CountUnavailableRepositories(ctx, c.db, c.virtualStorages)
	if err != nil {
		c.log.WithError(err).Error("failed collecting read-only repository count metric")
		return
	}

	for _, vs := range c.virtualStorages {
		for _, desc := range descriptions {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(unavailableCounts[vs]), vs)
		}
	}
}

// CountUnavailableRepositories queries the number of unavailable repositories from the database.
// A repository is unavailable when it has no replicas that can act as a primary, indicating
// they are either unhealthy or out of date.
func CountUnavailableRepositories(ctx context.Context, db glsql.Querier, virtualStorages []string) (map[string]int, error) {
	rows, err := db.QueryContext(ctx, `
SELECT virtual_storage, COUNT(*)
FROM repositories
WHERE NOT EXISTS (
	SELECT FROM valid_primaries
	WHERE valid_primaries.repository_id = repositories.repository_id
) AND repositories.virtual_storage = ANY($1)
GROUP BY virtual_storage
	`, virtualStorages)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	vsUnavailable := make(map[string]int)
	for rows.Next() {
		var vs string
		var count int

		if err := rows.Scan(&vs, &count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		vsUnavailable[vs] = count
	}

	return vsUnavailable, rows.Err()
}

// QueueDepthCollector collects metrics describing replication queue depths
type QueueDepthCollector struct {
	log     logrus.FieldLogger
	timeout time.Duration
	db      glsql.Querier
}

//nolint: stylecheck // This is unintentionally missing documentation.
func (q *QueueDepthCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descReplicationQueueDepth
}

// NewQueueDepthCollector returns a new QueueDepthCollector
func NewQueueDepthCollector(log logrus.FieldLogger, db glsql.Querier, timeout time.Duration) *QueueDepthCollector {
	return &QueueDepthCollector{
		timeout: timeout,
		db:      db,
	}
}

// Collect collects metrics describing the replication queue depth
func (q *QueueDepthCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.TODO(), q.timeout)
	defer cancel()

	rows, err := q.db.QueryContext(ctx, `
SELECT job->>'virtual_storage', job->>'target_node_storage', state, COUNT(*)
FROM replication_queue
GROUP BY job->>'virtual_storage', job->>'target_node_storage', state
`)
	if err != nil {
		q.log.WithError(err).Error("failed to query queue depth metrics")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var virtualStorage, targetNode, state string
		var count float64

		if err := rows.Scan(&virtualStorage, &targetNode, &state, &count); err != nil {
			q.log.WithError(err).Error("failed to scan row for queue depth metrics")
			return
		}

		ch <- prometheus.MustNewConstMetric(
			descReplicationQueueDepth,
			prometheus.GaugeValue,
			count,
			virtualStorage, targetNode, state)
	}

	if err := rows.Err(); err != nil {
		q.log.WithError(err).Error("failed to iterate over rows for queue depth metrics")
	}
}

const (
	statusUnverified = "unverified"
	statusExpired    = "expired"
)

// VerificationQueueDepthCollector collects the verification queue depth metric from the database.
type VerificationQueueDepthCollector struct {
	log                    logrus.FieldLogger
	timeout                time.Duration
	db                     glsql.Querier
	verificationInterval   time.Duration
	verificationQueueDepth *prometheus.GaugeVec
}

// NewVerificationQueueDepthCollector returns a new VerificationQueueDepthCollector
func NewVerificationQueueDepthCollector(log logrus.FieldLogger, db glsql.Querier, timeout, verificationInterval time.Duration, configuredStorages map[string][]string) *VerificationQueueDepthCollector {
	v := &VerificationQueueDepthCollector{
		log:                  log.WithField("component", "verification_queue_depth_collector"),
		timeout:              timeout,
		db:                   db,
		verificationInterval: verificationInterval,
		verificationQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gitaly_praefect_verification_queue_depth",
			Help: "Number of replicas pending verification.",
		}, []string{"virtual_storage", "storage", "status"}),
	}

	// pre-warm metrics to produce output even for storages which have an empty queue.
	for virtualStorage, storages := range configuredStorages {
		for _, storage := range storages {
			for _, status := range []string{statusUnverified, statusExpired} {
				v.verificationQueueDepth.WithLabelValues(virtualStorage, storage, status)
			}
		}
	}

	return v
}

// Describe describes the collected metrics to Prometheus.
func (c *VerificationQueueDepthCollector) Describe(ch chan<- *prometheus.Desc) {
	c.verificationQueueDepth.Describe(ch)
}

// Collect collects the verification queue depth metric from the database.
func (c *VerificationQueueDepthCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.TODO(), c.timeout)
	defer cancel()

	rows, err := c.db.QueryContext(ctx, `
SELECT
	repositories.virtual_storage,
	storage,
	COUNT(*) FILTER (WHERE verified_at IS NULL),
	COUNT(*) FILTER (WHERE verified_at IS NOT NULL)
FROM repositories
JOIN storage_repositories USING (repository_id)
WHERE verified_at IS NULL
OR    verified_at < now() - $1 * '1 microsecond'::interval
GROUP BY repositories.virtual_storage, storage
`, c.verificationInterval.Microseconds())
	if err != nil {
		c.log.WithError(err).Error("failed to query verification queue depth metric")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var virtualStorage, storage string
		var unverified, expired float64

		if err := rows.Scan(&virtualStorage, &storage, &unverified, &expired); err != nil {
			c.log.WithError(err).Error("failed to scan verification queue depth row")
			return
		}

		for _, metric := range []struct {
			status string
			value  float64
		}{
			{status: statusUnverified, value: unverified},
			{status: statusExpired, value: expired},
		} {
			c.verificationQueueDepth.WithLabelValues(virtualStorage, storage, metric.status).Set(metric.value)
		}
	}

	if err := rows.Err(); err != nil {
		c.log.WithError(err).Error("failed read verification queue depth rows")
	}

	c.verificationQueueDepth.Collect(ch)
}
