package praefect

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/conn"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

var (
	replicationLatency = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "replication_latency",
			Buckets:   prometheus.LinearBuckets(0, 100, 100),
		},
	)

	replicationJobsInFlight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "gitaly",
			Subsystem: "praefect",
			Name:      "replication_jobs",
		},
	)

	recordReplicationLatency = func(d float64) {
		go replicationLatency.Observe(d)
	}

	incReplicationJobsInFlight = func() {
		go replicationJobsInFlight.Inc()
	}

	decReplicationJobsInFlight = func() {
		go replicationJobsInFlight.Dec()
	}
)

func init() {
	prometheus.MustRegister(replicationLatency)
	prometheus.MustRegister(replicationJobsInFlight)
}

// Replicator performs the actual replication logic between two nodes
type Replicator interface {
	// Replicate propagates changes from the source to the target
	Replicate(ctx context.Context, job datastore.ReplJob, source, target *grpc.ClientConn) error
	// Destroy will remove the target repo on the specified target connection
	Destroy(ctx context.Context, job datastore.ReplJob, target *grpc.ClientConn) error
}

type defaultReplicator struct {
	log *logrus.Entry
}

func (dr defaultReplicator) Replicate(ctx context.Context, job datastore.ReplJob, sourceCC, targetCC *grpc.ClientConn) error {
	targetRepository := &gitalypb.Repository{
		StorageName:  job.TargetNode.Storage,
		RelativePath: job.Repository.RelativePath,
	}

	sourceRepository := &gitalypb.Repository{
		StorageName:  job.SourceNode.Storage,
		RelativePath: job.Repository.RelativePath,
	}

	repositoryClient := gitalypb.NewRepositoryServiceClient(targetCC)

	if _, err := repositoryClient.ReplicateRepository(ctx, &gitalypb.ReplicateRepositoryRequest{
		Source:     sourceRepository,
		Repository: targetRepository,
	}); err != nil {
		return fmt.Errorf("failed to create repository: %v", err)
	}

	checksumsMatch, err := dr.confirmChecksums(ctx, gitalypb.NewRepositoryServiceClient(sourceCC), repositoryClient, sourceRepository, targetRepository)
	if err != nil {
		return err
	}

	// TODO: Do something meaninful with the result of confirmChecksums if checksums do not match
	if !checksumsMatch {
		dr.log.WithFields(logrus.Fields{
			"primary": sourceRepository,
			"replica": targetRepository,
		}).Error("checksums do not match")
	}

	// TODO: ensure attribute files are synced
	// https://gitlab.com/gitlab-org/gitaly/issues/1655

	// TODO: ensure objects/info/alternates are synced
	// https://gitlab.com/gitlab-org/gitaly/issues/1674

	return nil
}

func (dr defaultReplicator) Destroy(ctx context.Context, job datastore.ReplJob, targetCC *grpc.ClientConn) error {
	targetRepo := &gitalypb.Repository{
		StorageName:  job.TargetNode.Storage,
		RelativePath: job.Repository.RelativePath,
	}

	repoSvcClient := gitalypb.NewRepositoryServiceClient(targetCC)

	_, err := repoSvcClient.RemoveRepository(ctx, &gitalypb.RemoveRepositoryRequest{
		Repository: targetRepo,
	})

	return err
}

func getChecksumFunc(ctx context.Context, client gitalypb.RepositoryServiceClient, repo *gitalypb.Repository, checksum *string) func() error {
	return func() error {
		primaryChecksumRes, err := client.CalculateChecksum(ctx, &gitalypb.CalculateChecksumRequest{
			Repository: repo,
		})
		if err != nil {
			return err
		}
		*checksum = primaryChecksumRes.GetChecksum()
		return nil
	}
}

func (dr defaultReplicator) confirmChecksums(ctx context.Context, primaryClient, replicaClient gitalypb.RepositoryServiceClient, primary, replica *gitalypb.Repository) (bool, error) {
	g, gCtx := errgroup.WithContext(ctx)

	var primaryChecksum, replicaChecksum string

	g.Go(getChecksumFunc(gCtx, primaryClient, primary, &primaryChecksum))
	g.Go(getChecksumFunc(gCtx, replicaClient, replica, &replicaChecksum))

	if err := g.Wait(); err != nil {
		return false, err
	}

	dr.log.WithFields(logrus.Fields{
		"primary":          primary,
		"replica":          replica,
		"primary_checksum": primaryChecksum,
		"replica_checksum": replicaChecksum,
	}).Info("replication finished")

	return primaryChecksum == replicaChecksum, nil
}

// ReplMgr is a replication manager for handling replication jobs
type ReplMgr struct {
	log               *logrus.Entry
	datastore         datastore.Datastore
	clientConnections *conn.ClientConnections
	targetNode        string     // which replica is this replicator responsible for?
	replicator        Replicator // does the actual replication logic

	// whitelist contains the project names of the repos we wish to replicate
	whitelist map[string]struct{}
}

// ReplMgrOpt allows a replicator to be configured with additional options
type ReplMgrOpt func(*ReplMgr)

// NewReplMgr initializes a replication manager with the provided dependencies
// and options
func NewReplMgr(targetNode string, log *logrus.Entry, datastore datastore.Datastore, c *conn.ClientConnections, opts ...ReplMgrOpt) ReplMgr {
	r := ReplMgr{
		log:               log,
		datastore:         datastore,
		whitelist:         map[string]struct{}{},
		replicator:        defaultReplicator{log},
		targetNode:        targetNode,
		clientConnections: c,
	}

	for _, opt := range opts {
		opt(&r)
	}

	return r
}

// WithWhitelist will configure a whitelist for repos to allow replication
func WithWhitelist(whitelistedRepos []string) ReplMgrOpt {
	return func(r *ReplMgr) {
		for _, repo := range whitelistedRepos {
			r.whitelist[repo] = struct{}{}
		}
	}
}

// WithReplicator overrides the default replicator
func WithReplicator(r Replicator) ReplMgrOpt {
	return func(rm *ReplMgr) {
		rm.replicator = r
	}
}

// ScheduleReplication will store a replication job in the datastore for later
// execution. It filters out projects that are not whitelisted.
// TODO: add a parameter to delay replication
func (r ReplMgr) ScheduleReplication(ctx context.Context, repo models.Repository) error {
	_, ok := r.whitelist[repo.RelativePath]
	if !ok {
		r.log.WithField(logKeyProjectPath, repo.RelativePath).
			Infof("project %q is not whitelisted for replication", repo.RelativePath)
		return nil
	}

	id, err := r.datastore.CreateReplicaReplJobs(repo.RelativePath, datastore.UpdateRepo)
	if err != nil {
		return err
	}

	r.log.WithFields(logrus.Fields{
		logWithReplJobID: id,
		"relative_path":  repo.RelativePath,
	}).Info("replication job created")

	return nil
}

const (
	jobFetchInterval  = 10 * time.Millisecond
	logWithReplJobID  = "replication_job_id"
	logWithReplSource = "replication_job_source"
	logWithReplTarget = "replication_job_target"
)

// ProcessBacklog will process queued jobs. It will block while processing jobs.
func (r ReplMgr) ProcessBacklog(ctx context.Context) error {
	for {
		nodes, err := r.datastore.GetStorageNodes()
		if err != nil {
			return nil
		}

		for _, node := range nodes {
			jobs, err := r.datastore.GetJobs(datastore.JobStateReady, node.Storage, 10)
			if err != nil {
				return err
			}

			if len(jobs) == 0 {
				r.log.WithFields(logrus.Fields{
					"node_storage":     node.Storage,
					"recheck_interval": jobFetchInterval,
				}).Trace("no jobs")

				select {
				// TODO: exponential backoff when no queries are returned
				case <-time.After(jobFetchInterval):
					continue

				case <-ctx.Done():
					return ctx.Err()
				}
			}

			for _, job := range jobs {
				r.log.WithFields(logrus.Fields{
					logWithReplJobID: job.ID,
					"from_storage":   job.SourceNode.Storage,
					"to_storage":     job.TargetNode.Storage,
					"relative_path":  job.Repository.RelativePath,
				}).Info("processing replication job")
				r.processReplJob(ctx, job)
			}
		}
	}
}

// TODO: errors that occur during replication should be handled better. Logging
// is a crutch in this situation. Ideally, we need to update state somewhere
// with information regarding the replication failure. See follow up issue:
// https://gitlab.com/gitlab-org/gitaly/issues/2138
func (r ReplMgr) processReplJob(ctx context.Context, job datastore.ReplJob) {
	l := r.log.
		WithField(logWithReplJobID, job.ID).
		WithField(logWithReplSource, job.SourceNode).
		WithField(logWithReplTarget, job.TargetNode)

	if err := r.datastore.UpdateReplJob(job.ID, datastore.JobStateInProgress); err != nil {
		l.WithError(err).Error("unable to update replication job to in progress")
		return
	}

	targetCC, err := r.clientConnections.GetConnection(job.TargetNode.Storage)
	if err != nil {
		l.WithError(err).Error("unable to obtain client connection for secondary node in replication job")
		return
	}

	sourceCC, err := r.clientConnections.GetConnection(job.Repository.Primary.Storage)
	if err != nil {
		l.WithError(err).Error("unable to obtain client connection for primary node in replication job")
		return
	}

	injectedCtx, err := helper.InjectGitalyServers(ctx, job.Repository.Primary.Storage, job.SourceNode.Address, job.SourceNode.Token)
	if err != nil {
		l.WithError(err).Error("unable to inject Gitaly servers into context for replication job")
		return
	}

	replStart := time.Now()
	incReplicationJobsInFlight()
	defer decReplicationJobsInFlight()

	switch job.Change {
	case datastore.UpdateRepo:
		err = r.replicator.Replicate(injectedCtx, job, sourceCC, targetCC)
	case datastore.DeleteRepo:
		err = r.replicator.Destroy(injectedCtx, job, targetCC)
	default:
		err = fmt.Errorf("unknown replication change type encountered: %d", job.Change)
	}
	if err != nil {
		l.WithError(err).Error("unable to replicate")
		return
	}

	replDuration := time.Since(replStart)
	recordReplicationLatency(float64(replDuration / time.Millisecond))

	if err := r.datastore.UpdateReplJob(job.ID, datastore.JobStateComplete); err != nil {
		l.WithError(err).Error("error when updating replication job status to complete")
	}
}
