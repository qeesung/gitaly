package praefect

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/middleware/metadatahandler"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/metrics"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/nodes"
	prommetrics "gitlab.com/gitlab-org/gitaly/internal/prometheus/metrics"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	grpccorrelation "gitlab.com/gitlab-org/labkit/correlation/grpc"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

// Replicator performs the actual replication logic between two nodes
type Replicator interface {
	// Replicate propagates changes from the source to the target
	Replicate(ctx context.Context, job datastore.ReplJob, source, target *grpc.ClientConn) error
	// Destroy will remove the target repo on the specified target connection
	Destroy(ctx context.Context, job datastore.ReplJob, target *grpc.ClientConn) error
	// Rename will rename(move) the target repo on the specified target connection
	Rename(ctx context.Context, job datastore.ReplJob, target *grpc.ClientConn) error
	// GarbageCollect will run gc on the target repository
	GarbageCollect(ctx context.Context, job datastore.ReplJob, target *grpc.ClientConn) error
	// RepackFull will do a full repack on the target repository
	RepackFull(ctx context.Context, job datastore.ReplJob, target *grpc.ClientConn) error
	// RepackIncremental will do an incremental repack on the target repository
	RepackIncremental(ctx context.Context, job datastore.ReplJob, target *grpc.ClientConn) error
}

type defaultReplicator struct {
	log *logrus.Entry
}

func (dr defaultReplicator) Replicate(ctx context.Context, job datastore.ReplJob, sourceCC, targetCC *grpc.ClientConn) error {
	targetRepository := &gitalypb.Repository{
		StorageName:  job.TargetNode.Storage,
		RelativePath: job.RelativePath,
	}

	sourceRepository := &gitalypb.Repository{
		StorageName:  job.SourceNode.Storage,
		RelativePath: job.RelativePath,
	}

	targetRepositoryClient := gitalypb.NewRepositoryServiceClient(targetCC)

	if _, err := targetRepositoryClient.ReplicateRepository(ctx, &gitalypb.ReplicateRepositoryRequest{
		Source:     sourceRepository,
		Repository: targetRepository,
	}); err != nil {
		return fmt.Errorf("failed to create repository: %v", err)
	}

	// check if the repository has an object pool
	sourceObjectPoolClient := gitalypb.NewObjectPoolServiceClient(sourceCC)

	resp, err := sourceObjectPoolClient.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{
		Repository: sourceRepository,
	})
	if err != nil {
		return err
	}

	sourceObjectPool := resp.GetObjectPool()

	if sourceObjectPool != nil {
		targetObjectPoolClient := gitalypb.NewObjectPoolServiceClient(targetCC)
		targetObjectPool := *sourceObjectPool
		targetObjectPool.GetRepository().StorageName = targetRepository.GetStorageName()
		if _, err := targetObjectPoolClient.LinkRepositoryToObjectPool(ctx, &gitalypb.LinkRepositoryToObjectPoolRequest{
			ObjectPool: &targetObjectPool,
			Repository: targetRepository,
		}); err != nil {
			return err
		}
	}

	checksumsMatch, err := dr.confirmChecksums(ctx, gitalypb.NewRepositoryServiceClient(sourceCC), targetRepositoryClient, sourceRepository, targetRepository)
	if err != nil {
		return err
	}

	// TODO: Do something meaninful with the result of confirmChecksums if checksums do not match
	if !checksumsMatch {
		metrics.ChecksumMismatchCounter.WithLabelValues(
			targetRepository.GetStorageName(),
			sourceRepository.GetStorageName(),
		).Inc()
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
		RelativePath: job.RelativePath,
	}

	repoSvcClient := gitalypb.NewRepositoryServiceClient(targetCC)

	_, err := repoSvcClient.RemoveRepository(ctx, &gitalypb.RemoveRepositoryRequest{
		Repository: targetRepo,
	})

	return err
}

func (dr defaultReplicator) Rename(ctx context.Context, job datastore.ReplJob, targetCC *grpc.ClientConn) error {
	targetRepo := &gitalypb.Repository{
		StorageName:  job.TargetNode.Storage,
		RelativePath: job.RelativePath,
	}

	repoSvcClient := gitalypb.NewRepositoryServiceClient(targetCC)

	val, found := job.Params["RelativePath"]
	if !found {
		return errors.New("no 'RelativePath' parameter for rename")
	}

	relativePath, ok := val.(string)
	if !ok {
		return fmt.Errorf("parameter 'RelativePath' has unexpected type: %T", relativePath)
	}

	_, err := repoSvcClient.RenameRepository(ctx, &gitalypb.RenameRepositoryRequest{
		Repository:   targetRepo,
		RelativePath: relativePath,
	})

	return err
}

func (dr defaultReplicator) GarbageCollect(ctx context.Context, job datastore.ReplJob, targetCC *grpc.ClientConn) error {
	targetRepo := &gitalypb.Repository{
		StorageName:  job.TargetNode.Storage,
		RelativePath: job.RelativePath,
	}

	val, found := job.Params["CreateBitmap"]
	if !found {
		return errors.New("no 'CreateBitmap' parameter for garbage collect")
	}
	createBitmap, ok := val.(bool)
	if !ok {
		return fmt.Errorf("parameter 'CreateBitmap' has unexpected type: %T", createBitmap)
	}

	repoSvcClient := gitalypb.NewRepositoryServiceClient(targetCC)

	_, err := repoSvcClient.GarbageCollect(ctx, &gitalypb.GarbageCollectRequest{
		Repository:   targetRepo,
		CreateBitmap: createBitmap,
	})

	return err
}

func (dr defaultReplicator) RepackIncremental(ctx context.Context, job datastore.ReplJob, targetCC *grpc.ClientConn) error {
	targetRepo := &gitalypb.Repository{
		StorageName:  job.TargetNode.Storage,
		RelativePath: job.RelativePath,
	}

	repoSvcClient := gitalypb.NewRepositoryServiceClient(targetCC)

	_, err := repoSvcClient.RepackIncremental(ctx, &gitalypb.RepackIncrementalRequest{
		Repository: targetRepo,
	})

	return err
}

func (dr defaultReplicator) RepackFull(ctx context.Context, job datastore.ReplJob, targetCC *grpc.ClientConn) error {
	targetRepo := &gitalypb.Repository{
		StorageName:  job.TargetNode.Storage,
		RelativePath: job.RelativePath,
	}

	val, found := job.Params["CreateBitmap"]
	if !found {
		return errors.New("no 'CreateBitmap' parameter for repack full")
	}
	createBitmap, ok := val.(bool)
	if !ok {
		return fmt.Errorf("parameter 'CreateBitmap' has unexpected type: %T", createBitmap)
	}

	repoSvcClient := gitalypb.NewRepositoryServiceClient(targetCC)

	_, err := repoSvcClient.RepackFull(ctx, &gitalypb.RepackFullRequest{
		Repository:   targetRepo,
		CreateBitmap: createBitmap,
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
	nodeManager       nodes.Manager
	virtualStorages   []string   // replicas this replicator is responsible for
	replicator        Replicator // does the actual replication logic
	replQueueMetric   prommetrics.Gauge
	replLatencyMetric prommetrics.HistogramVec
	replDelayMetric   prommetrics.HistogramVec
	replJobTimeout    time.Duration
	// whitelist contains the project names of the repos we wish to replicate
	whitelist map[string]struct{}
}

// ReplMgrOpt allows a replicator to be configured with additional options
type ReplMgrOpt func(*ReplMgr)

// WithQueueMetric is an option to set the queue size prometheus metric
func WithQueueMetric(g prommetrics.Gauge) func(*ReplMgr) {
	return func(m *ReplMgr) {
		m.replQueueMetric = g
	}
}

// WithLatencyMetric is an option to set the latency prometheus metric
func WithLatencyMetric(h prommetrics.HistogramVec) func(*ReplMgr) {
	return func(m *ReplMgr) {
		m.replLatencyMetric = h
	}
}

// WithDelayMetric is an option to set the delay prometheus metric
func WithDelayMetric(h prommetrics.HistogramVec) func(*ReplMgr) {
	return func(m *ReplMgr) {
		m.replDelayMetric = h
	}
}

// NewReplMgr initializes a replication manager with the provided dependencies
// and options
func NewReplMgr(log *logrus.Entry, datastore datastore.Datastore, nodeMgr nodes.Manager, opts ...ReplMgrOpt) ReplMgr {
	r := ReplMgr{
		log:               log.WithField("component", "replication_manager"),
		datastore:         datastore,
		whitelist:         map[string]struct{}{},
		replicator:        defaultReplicator{log},
		virtualStorages:   datastore.VirtualStorages(),
		nodeManager:       nodeMgr,
		replLatencyMetric: prometheus.NewHistogramVec(prometheus.HistogramOpts{}, []string{"type"}),
		replDelayMetric:   prometheus.NewHistogramVec(prometheus.HistogramOpts{}, []string{"type"}),
		replQueueMetric:   prometheus.NewGauge(prometheus.GaugeOpts{}),
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

const (
	logWithReplJobID   = "replication_job_id"
	logWithReplVirtual = "replication_job_virtual"
	logWithReplSource  = "replication_job_source"
	logWithReplTarget  = "replication_job_target"
	logWithReplChange  = "replication_job_change"
	logWithReplPath    = "replication_job_path"
	logWithCorrID      = "replication_correlation_id"
)

type backoff func() time.Duration
type backoffReset func()

// BackoffFunc is a function that n turn provides a pair of functions backoff and backoffReset
type BackoffFunc func() (backoff, backoffReset)

// ExpBackoffFunc generates a backoffFunc based off of start and max time durations
func ExpBackoffFunc(start time.Duration, max time.Duration) BackoffFunc {
	return func() (backoff, backoffReset) {
		const factor = 2
		duration := start

		return func() time.Duration {
				defer func() {
					duration *= time.Duration(factor)
					if (duration) >= max {
						duration = max
					}
				}()
				return duration
			}, func() {
				duration = start
			}
	}
}

// createReplJob converts `ReplicationEvent` into `ReplJob`.
// It is intermediate solution until `ReplJob` removed and code not adopted to `ReplicationEvent`.
func (r ReplMgr) createReplJob(event datastore.ReplicationEvent) (datastore.ReplJob, error) {
	targetNode, err := r.datastore.GetStorageNode(event.Job.TargetNodeStorage)
	if err != nil {
		return datastore.ReplJob{}, err
	}

	sourceNode, err := r.datastore.GetStorageNode(event.Job.SourceNodeStorage)
	if err != nil {
		return datastore.ReplJob{}, err
	}

	var correlationID string
	if val, found := event.Meta[metadatahandler.CorrelationIDKey]; found {
		correlationID, _ = val.(string)
	}

	replJob := datastore.ReplJob{
		Attempts:       event.Attempt,
		Change:         event.Job.Change,
		ID:             event.ID,
		VirtualStorage: event.Job.VirtualStorage,
		TargetNode:     targetNode,
		SourceNode:     sourceNode,
		RelativePath:   event.Job.RelativePath,
		Params:         event.Job.Params,
		CorrelationID:  correlationID,
		CreatedAt:      event.CreatedAt,
	}

	return replJob, nil
}

// ProcessBacklog starts processing of queued jobs.
// It will be processing jobs until ctx is Done.
func (r ReplMgr) ProcessBacklog(ctx context.Context, b BackoffFunc) {
	for _, virtualStorage := range r.virtualStorages {
		go r.processBacklog(ctx, b, virtualStorage)
	}
}

func (r ReplMgr) processBacklog(ctx context.Context, b BackoffFunc, virtualStorage string) {
	logger := r.log.WithField("virtual_storage", virtualStorage)
	backoff, reset := b()

	for {
		select {
		case <-ctx.Done():
			logger.WithError(ctx.Err()).Info("processing stopped")
			return // processing must be stopped
		default:
			// proceed with processing
		}

		var totalEvents int
		shard, err := r.nodeManager.GetShard(virtualStorage)
		if err == nil {
			targetNodes := shard.Secondaries
			if shard.IsReadOnly {
				targetNodes = append(targetNodes, shard.Primary)
			}

			for _, target := range targetNodes {
				events, err := r.datastore.Dequeue(ctx, virtualStorage, target.GetStorage(), 10)
				if err != nil {
					logger.WithField(logWithReplTarget, target.GetStorage()).WithError(err).Error("failed to dequeue replication events")
					continue
				}

				totalEvents += len(events)

				eventIDsByState := map[datastore.JobState][]uint64{}
				for _, event := range events {
					job, err := r.createReplJob(event)
					if err != nil {
						logger.WithField("event", event).WithError(err).Error("failed to restore replication job")
						eventIDsByState[datastore.JobStateFailed] = append(eventIDsByState[datastore.JobStateFailed], event.ID)
						continue
					}

					source, err := shard.GetNode(job.SourceNode.Storage)
					if err != nil {
						logger.WithField("event", event).WithError(err).Error("failed to get source node for replication job")
						eventIDsByState[datastore.JobStateFailed] = append(eventIDsByState[datastore.JobStateFailed], event.ID)
						continue
					}

					if err := r.processReplJob(ctx, job, source.GetConnection(), target.GetConnection()); err != nil {
						logger.WithFields(logrus.Fields{
							logWithReplJobID:   job.ID,
							logWithReplVirtual: job.VirtualStorage,
							logWithReplTarget:  job.TargetNode.Storage,
							logWithReplSource:  job.SourceNode.Storage,
							logWithReplChange:  job.Change,
							logWithReplPath:    job.RelativePath,
							logWithCorrID:      job.CorrelationID,
						}).WithError(err).Error("replication job failed")
						if job.Attempts == 0 {
							eventIDsByState[datastore.JobStateDead] = append(eventIDsByState[datastore.JobStateDead], event.ID)
						} else {
							eventIDsByState[datastore.JobStateFailed] = append(eventIDsByState[datastore.JobStateFailed], event.ID)
						}
						continue
					}
					eventIDsByState[datastore.JobStateCompleted] = append(eventIDsByState[datastore.JobStateCompleted], event.ID)
				}
				for state, eventIDs := range eventIDsByState {
					ackIDs, err := r.datastore.Acknowledge(ctx, state, eventIDs)
					if err != nil {
						logger.WithField("state", state).WithField("event_ids", eventIDs).WithError(err).Error("failed to acknowledge replication events")
						continue
					}

					notAckIDs := subtractUint64(ackIDs, eventIDs)
					if len(notAckIDs) > 0 {
						logger.WithField("state", state).WithField("event_ids", notAckIDs).WithError(err).Error("replication events were not acknowledged")
					}
				}
			}
		} else {
			logger.WithError(err).Error("error when getting primary and secondaries")
		}

		if totalEvents == 0 {
			select {
			case <-time.After(backoff()):
				continue
			case <-ctx.Done():
				logger.WithError(ctx.Err()).Info("processing stopped")
				return
			}
		}

		reset()
	}
}

func (r ReplMgr) processReplJob(ctx context.Context, job datastore.ReplJob, sourceCC, targetCC *grpc.ClientConn) error {
	l := r.log.
		WithField(logWithReplJobID, job.ID).
		WithField(logWithReplVirtual, job.VirtualStorage).
		WithField(logWithReplSource, job.SourceNode).
		WithField(logWithReplTarget, job.TargetNode).
		WithField(logWithReplPath, job.RelativePath).
		WithField(logWithReplChange, job.Change).
		WithField(logWithCorrID, job.CorrelationID)

	var replCtx context.Context
	var cancel func()

	if r.replJobTimeout > 0 {
		replCtx, cancel = context.WithTimeout(ctx, r.replJobTimeout)
	} else {
		replCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	injectedCtx, err := helper.InjectGitalyServers(replCtx, job.SourceNode.Storage, job.SourceNode.Address, job.SourceNode.Token)
	if err != nil {
		l.WithError(err).Error("unable to inject Gitaly servers into context for replication job")
		return err
	}

	if job.CorrelationID == "" {
		l.Warn("replication job missing correlation ID")
	}
	injectedCtx = grpccorrelation.InjectToOutgoingContext(injectedCtx, job.CorrelationID)

	replStart := time.Now()

	replDelay := replStart.Sub(job.CreatedAt)
	r.replDelayMetric.WithLabelValues(job.Change.String()).Observe(replDelay.Seconds())

	r.replQueueMetric.Inc()
	defer r.replQueueMetric.Dec()

	switch job.Change {
	case datastore.UpdateRepo:
		err = r.replicator.Replicate(injectedCtx, job, sourceCC, targetCC)
	case datastore.DeleteRepo:
		err = r.replicator.Destroy(injectedCtx, job, targetCC)
	case datastore.RenameRepo:
		err = r.replicator.Rename(injectedCtx, job, targetCC)
	case datastore.GarbageCollect:
		err = r.replicator.GarbageCollect(injectedCtx, job, targetCC)
	case datastore.RepackFull:
		err = r.replicator.RepackFull(injectedCtx, job, targetCC)
	case datastore.RepackIncremental:
		err = r.replicator.RepackIncremental(injectedCtx, job, targetCC)
	default:
		err = fmt.Errorf("unknown replication change type encountered: %q", job.Change)
	}
	if err != nil {
		l.WithError(err).Error("unable to replicate")
		return err
	}

	replDuration := time.Since(replStart)
	r.replLatencyMetric.WithLabelValues(job.Change.String()).Observe(replDuration.Seconds())

	return nil
}

// subtractUint64 returns new slice that has all elements from left that does not exist at right.
func subtractUint64(l, r []uint64) []uint64 {
	if len(l) == 0 {
		return nil
	}

	if len(r) == 0 {
		result := make([]uint64, len(l))
		copy(result, l)
		return result
	}

	excludeSet := make(map[uint64]struct{}, len(l))
	for _, v := range r {
		excludeSet[v] = struct{}{}
	}

	var result []uint64
	for _, v := range l {
		if _, found := excludeSet[v]; !found {
			result = append(result, v)
		}
	}

	return result
}
