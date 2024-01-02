package praefect

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v16/auth"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	gitalycfg "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/promtest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/labkit/correlation"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func TestReplMgr_ProcessBacklog(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	primaryCfg := testcfg.Build(t, testcfg.WithStorages("primary"))
	testRepoProto, testRepoPath := gittest.CreateRepository(t, ctx, primaryCfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	primaryCfg.SocketPath = testserver.RunGitalyServer(t, primaryCfg, setup.RegisterAll, testserver.WithDisablePraefect())
	testcfg.BuildGitalySSH(t, primaryCfg)
	testcfg.BuildGitalyHooks(t, primaryCfg)

	backupCfg := testcfg.Build(t, testcfg.WithStorages("backup"))
	backupCfg.SocketPath = testserver.RunGitalyServer(t, backupCfg, setup.RegisterAll, testserver.WithDisablePraefect())
	testcfg.BuildGitalySSH(t, backupCfg)
	testcfg.BuildGitalyHooks(t, backupCfg)

	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{{
			Name: "virtual",
			Nodes: []*config.Node{
				{
					Storage: primaryCfg.Storages[0].Name,
					Address: primaryCfg.SocketPath,
				},
				{
					Storage: backupCfg.Storages[0].Name,
					Address: backupCfg.SocketPath,
				},
			},
		}},
	}

	// create object pool on the source
	poolRepository, _ := gittest.CreateObjectPool(t, ctx, primaryCfg, testRepoProto, gittest.CreateObjectPoolConfig{
		LinkRepositoryToObjectPool: true,
	})

	// replicate object pool repository to target node
	targetObjectPoolRepo := proto.Clone(poolRepository).(*gitalypb.ObjectPool)
	targetObjectPoolRepo.Repository.StorageName = backupCfg.Storages[0].Name
	ctx, cancel := context.WithCancel(ctx)

	injectedCtx := metadata.NewOutgoingContext(ctx, testcfg.GitalyServersMetadataFromCfg(t, primaryCfg))

	repoClient := newRepositoryClient(t, backupCfg.SocketPath, backupCfg.Auth.Token)
	_, err := repoClient.ReplicateRepository(injectedCtx, &gitalypb.ReplicateRepositoryRequest{
		Repository: targetObjectPoolRepo.Repository,
		Source:     poolRepository.Repository,
	})
	require.NoError(t, err)

	entry := testhelper.SharedLogger(t)

	nodeMgr, err := nodes.NewManager(entry, conf, nil, nil, promtest.NewMockHistogramVec(), protoregistry.GitalyProtoPreregistered, nil, nil, nil)
	require.NoError(t, err)
	nodeMgr.Start(1*time.Millisecond, 5*time.Millisecond)
	defer nodeMgr.Stop()

	shard, err := nodeMgr.GetShard(ctx, conf.VirtualStorages[0].Name)
	require.NoError(t, err)
	require.Len(t, shard.Secondaries, 1)

	const repositoryID = 1
	var events []datastore.ReplicationEvent
	for _, secondary := range shard.Secondaries {
		events = append(events, datastore.ReplicationEvent{
			Job: datastore.ReplicationJob{
				RepositoryID:      repositoryID,
				VirtualStorage:    conf.VirtualStorages[0].Name,
				Change:            datastore.UpdateRepo,
				TargetNodeStorage: secondary.GetStorage(),
				SourceNodeStorage: shard.Primary.GetStorage(),
				RelativePath:      testRepoProto.GetRelativePath(),
			},
			State:   datastore.JobStateReady,
			Attempt: 3,
			Meta:    datastore.Params{datastore.CorrelationIDKey: "correlation-id"},
		})
	}
	require.Len(t, events, 1)

	commitID := gittest.WriteCommit(t, primaryCfg, testRepoPath, gittest.WithBranch("master"))

	var mockReplicationLatencyHistogramVec promtest.MockHistogramVec
	var mockReplicationDelayHistogramVec promtest.MockHistogramVec

	logger := testhelper.SharedLogger(t)
	loggerHook := testhelper.AddLoggerHook(logger)

	queue := datastore.NewReplicationEventQueueInterceptor(datastore.NewPostgresReplicationEventQueue(testdb.New(t)))
	queue.OnAcknowledge(func(ctx context.Context, state datastore.JobState, ids []uint64, queue datastore.ReplicationEventQueue) ([]uint64, error) {
		cancel() // when it is called we know that replication is finished
		return queue.Acknowledge(ctx, state, ids)
	})

	loggerEntry := logger.WithField("test", t.Name())
	_, err = queue.Enqueue(ctx, events[0])
	require.NoError(t, err)

	db := testdb.New(t)
	rs := datastore.NewPostgresRepositoryStore(db, conf.StorageNames())
	require.NoError(t, rs.CreateRepository(ctx, repositoryID, conf.VirtualStorages[0].Name, testRepoProto.GetRelativePath(), testRepoProto.GetRelativePath(), shard.Primary.GetStorage(), nil, nil, true, false))

	replMgr := NewReplMgr(
		loggerEntry,
		conf.StorageNames(),
		queue,
		rs,
		nodeMgr,
		NodeSetFromNodeManager(nodeMgr),
		WithLatencyMetric(&mockReplicationLatencyHistogramVec),
		WithDelayMetric(&mockReplicationDelayHistogramVec),
		WithParallelStorageProcessingWorkers(100),
	)

	replMgr.ProcessBacklog(ctx, noopBackoffFactory{})

	logEntries := loggerHook.AllEntries()
	require.True(t, len(logEntries) > 4, "expected at least 5 log entries to be present")
	require.Equal(t,
		[]interface{}{`parallel processing workers decreased from 100 configured with config to 1 according to minumal amount of storages in the virtual storage "virtual"`},
		[]interface{}{logEntries[0].Message},
	)

	require.Equal(t,
		[]interface{}{"processing started", "virtual"},
		[]interface{}{logEntries[1].Message, logEntries[1].Data["virtual_storage"]},
	)

	require.Equal(t,
		[]interface{}{"replication job processing started", "virtual", "correlation-id"},
		[]interface{}{logEntries[2].Message, logEntries[2].Data["virtual_storage"], logEntries[2].Data[correlation.FieldName]},
	)

	dequeuedEvent := logEntries[2].Data["event"].(datastore.ReplicationEvent)
	require.Equal(t, datastore.JobStateInProgress, dequeuedEvent.State)
	require.Equal(t, []string{"backup", "primary"}, []string{dequeuedEvent.Job.TargetNodeStorage, dequeuedEvent.Job.SourceNodeStorage})

	require.Equal(t,
		[]interface{}{"replication job processing finished", "virtual", datastore.JobStateCompleted, "correlation-id"},
		[]interface{}{logEntries[3].Message, logEntries[3].Data["virtual_storage"], logEntries[3].Data["new_state"], logEntries[3].Data[correlation.FieldName]},
	)

	replicatedPath := filepath.Join(backupCfg.Storages[0].Path, testRepoProto.GetRelativePath())

	gittest.Exec(t, backupCfg, "-C", replicatedPath, "cat-file", "-e", commitID.String())
	gittest.Exec(t, backupCfg, "-C", replicatedPath, "gc")
	require.Less(t, gittest.GetGitPackfileDirSize(t, replicatedPath), int64(100), "expect a small pack directory")

	require.Equal(t, mockReplicationLatencyHistogramVec.LabelsCalled(), [][]string{{"update"}})
	require.Equal(t, mockReplicationDelayHistogramVec.LabelsCalled(), [][]string{{"update"}})
	require.NoError(t, testutil.CollectAndCompare(replMgr, strings.NewReader(`
# HELP gitaly_praefect_replication_jobs Number of replication jobs in flight.
# TYPE gitaly_praefect_replication_jobs gauge
gitaly_praefect_replication_jobs{change_type="update",gitaly_storage="backup",virtual_storage="virtual"} 0
`)))
}

func TestDefaultReplicator_Replicate(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	newGitalyConn := func(t *testing.T, config gitalycfg.Cfg) *grpc.ClientConn {
		t.Helper()

		conn, err := grpc.Dial(
			config.SocketPath,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(config.Auth.Token)),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		return conn
	}

	const sourceStorage = "source"
	const targetStorage = "target"

	sourceCfg := testcfg.Build(t, testcfg.WithStorages(sourceStorage))
	targetCfg := testcfg.Build(t, testcfg.WithStorages(targetStorage))

	// Set up source and target Gitaly servers to simulate the Praefect replicator performing a
	// replication job.
	sourceAddr := testserver.RunGitalyServer(t, sourceCfg, setup.RegisterAll, testserver.WithDisablePraefect())
	targetAddr := testserver.RunGitalyServer(t, targetCfg, setup.RegisterAll, testserver.WithDisablePraefect())

	sourceCfg.SocketPath = sourceAddr
	targetCfg.SocketPath = targetAddr

	sourceConn := newGitalyConn(t, sourceCfg)
	targetConn := newGitalyConn(t, targetCfg)

	testcfg.BuildGitalySSH(t, targetCfg)

	// For the repository to be replicated from the source Gitaly, the target Gitaly must know
	// how to connect to the source. This is done by injecting Gitaly server information into
	// the context.
	ctx, err := storage.InjectGitalyServers(ctx, sourceStorage, sourceAddr, sourceCfg.Auth.Token)
	require.NoError(t, err)

	// Configure repository store operations to function as no-ops.
	repoStore := datastore.MockRepositoryStore{
		GetReplicatedGenerationFunc: func(context.Context, int64, string, string) (int, error) {
			return 0, nil
		},
		SetGenerationFunc: func(context.Context, int64, string, string, int) error {
			return nil
		},
	}

	type setupData struct {
		job          datastore.ReplicationJob
		expectedPool *gitalypb.ObjectPool
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "source does not have alternate link",
			setup: func(t *testing.T) setupData {
				// Create repository on source Gitaly that does not have a link to
				// an object pool.
				sourceRepo, _ := gittest.CreateRepository(t, ctx, sourceCfg)
				return setupData{
					job: datastore.ReplicationJob{
						ReplicaPath:       sourceRepo.RelativePath,
						TargetNodeStorage: targetStorage,
						SourceNodeStorage: sourceStorage,
					},
					expectedPool: nil,
				}
			},
		},
		{
			desc: "source alternate link replicated to target",
			setup: func(t *testing.T) setupData {
				// Create repository on source Gitaly that is linked to object pool.
				sourceRepo, _ := gittest.CreateRepository(t, ctx, sourceCfg)
				sourcePool, _ := gittest.CreateObjectPool(t, ctx, sourceCfg, sourceRepo, gittest.CreateObjectPoolConfig{
					LinkRepositoryToObjectPool: true,
				})

				// Create object pool on target Gitaly with the same relative path
				// as the source object pool.
				gittest.CreateRepository(t, ctx, targetCfg, gittest.CreateRepositoryConfig{
					RelativePath: sourcePool.Repository.RelativePath,
				})

				expectedPool := &gitalypb.ObjectPool{
					Repository: &gitalypb.Repository{
						StorageName:  targetStorage,
						RelativePath: sourcePool.Repository.RelativePath,
					},
				}

				if testhelper.IsWALEnabled() {
					// See the replicator's replicate method for reasoning behind the pool
					// not being connected with transactions.
					expectedPool = nil
				}

				return setupData{
					job: datastore.ReplicationJob{
						ReplicaPath:       sourceRepo.RelativePath,
						TargetNodeStorage: targetStorage,
						SourceNodeStorage: sourceStorage,
					},
					expectedPool: expectedPool,
				}
			},
		},
		{
			desc: "target disconnected from alternate to match source",
			setup: func(t *testing.T) setupData {
				// Create repository on source Gitaly that does not have a link to
				// an object pool.
				sourceRepo, _ := gittest.CreateRepository(t, ctx, sourceCfg)

				// Create repository on target Gitaly with the same relative path as
				// the source repository and link it to an object pool.
				targetRepo, _ := gittest.CreateRepository(t, ctx, targetCfg, gittest.CreateRepositoryConfig{
					RelativePath: sourceRepo.RelativePath,
				})
				gittest.CreateObjectPool(t, ctx, targetCfg, targetRepo, gittest.CreateObjectPoolConfig{
					LinkRepositoryToObjectPool: true,
				})

				return setupData{
					job: datastore.ReplicationJob{
						ReplicaPath:       sourceRepo.RelativePath,
						TargetNodeStorage: targetStorage,
						SourceNodeStorage: sourceStorage,
					},
					expectedPool: nil,
				}
			},
		},
		{
			desc: "target disconnected and linked to match source",
			setup: func(t *testing.T) setupData {
				// Create repository on source Gitaly that does not have a link to an object pool.
				sourceRepo, _ := gittest.CreateRepository(t, ctx, sourceCfg)
				sourcePool, _ := gittest.CreateObjectPool(t, ctx, sourceCfg, sourceRepo, gittest.CreateObjectPoolConfig{
					LinkRepositoryToObjectPool: true,
				})

				// Create repository on target Gitaly with a relative path different from the source
				// repository and link it to an object pool.
				targetRepo, _ := gittest.CreateRepository(t, ctx, targetCfg, gittest.CreateRepositoryConfig{
					RelativePath: sourceRepo.RelativePath,
				})
				gittest.CreateObjectPool(t, ctx, targetCfg, targetRepo, gittest.CreateObjectPoolConfig{
					LinkRepositoryToObjectPool: true,
				})

				// Create object pool on target Gitaly with the same relative path as the source
				// object pool. This repository will eventually be linked to the target repository.
				gittest.CreateRepository(t, ctx, targetCfg, gittest.CreateRepositoryConfig{
					RelativePath: sourcePool.Repository.RelativePath,
				})

				expectedPool := &gitalypb.ObjectPool{
					Repository: &gitalypb.Repository{
						StorageName:  targetStorage,
						RelativePath: sourcePool.Repository.RelativePath,
					},
				}

				if testhelper.IsWALEnabled() {
					// See the replicator's replicate method for reasoning behind the pool
					// not being connected with transactions.
					expectedPool = nil
				}

				return setupData{
					job: datastore.ReplicationJob{
						ReplicaPath:       sourceRepo.RelativePath,
						TargetNodeStorage: targetStorage,
						SourceNodeStorage: sourceStorage,
					},
					expectedPool: expectedPool,
				}
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			testSetup := tc.setup(t)

			replicator := &defaultReplicator{
				rs:  repoStore,
				log: testhelper.SharedLogger(t),
			}
			err := replicator.Replicate(ctx, datastore.ReplicationEvent{Job: testSetup.job}, sourceConn, targetConn)
			require.NoError(t, err)

			targetRepo := &gitalypb.Repository{
				StorageName:  targetStorage,
				RelativePath: testSetup.job.ReplicaPath,
			}

			// After the replicator has completed, the target storage should have the repository.
			targetRepoClient := gitalypb.NewRepositoryServiceClient(targetConn)
			existsResp, err := targetRepoClient.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
				Repository: targetRepo,
			})
			require.NoError(t, err)
			require.True(t, existsResp.Exists)

			// If the source repository has linked to an object pool, the target should
			// also be linked to an object pool with the same relative path.
			targetPoolClient := gitalypb.NewObjectPoolServiceClient(targetConn)
			poolResp, err := targetPoolClient.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{
				Repository: targetRepo,
			})
			require.NoError(t, err)
			require.Equal(t, testSetup.expectedPool, poolResp.GetObjectPool())
		})
	}
}

func TestReplicatorDowngradeAttempt(t *testing.T) {
	ctx := testhelper.Context(t)

	ctx = correlation.ContextWithCorrelation(ctx, "correlation-id")

	for _, tc := range []struct {
		desc                string
		attemptedGeneration int
		expectedMessage     string
	}{
		{
			desc:                "same generation attempted",
			attemptedGeneration: 1,
			expectedMessage:     "target repository already on the same generation, skipping replication job",
		},
		{
			desc:                "lower generation attempted",
			attemptedGeneration: 0,
			expectedMessage:     "repository downgrade prevented",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			returnedErr := datastore.DowngradeAttemptedError{
				Storage:             "gitaly-2",
				CurrentGeneration:   1,
				AttemptedGeneration: tc.attemptedGeneration,
			}

			rs := datastore.MockRepositoryStore{
				GetReplicatedGenerationFunc: func(ctx context.Context, repositoryID int64, source, target string) (int, error) {
					return 0, returnedErr
				},
			}

			logger := testhelper.SharedLogger(t)
			hook := testhelper.AddLoggerHook(logger)
			r := &defaultReplicator{rs: rs, log: logger}

			require.NoError(t, r.Replicate(ctx, datastore.ReplicationEvent{
				Job: datastore.ReplicationJob{
					ReplicaPath:       "relative-path-1",
					VirtualStorage:    "virtual-storage-1",
					RelativePath:      "relative-path-1",
					SourceNodeStorage: "gitaly-1",
					TargetNodeStorage: "gitaly-2",
				},
			}, nil, nil))

			entries := hook.AllEntries()
			require.Len(t, entries, 1)
			lastEntry := entries[0]
			require.Equal(t, logrus.InfoLevel, lastEntry.Level)
			require.Equal(t, returnedErr, lastEntry.Data["error"])
			require.Equal(t, "correlation-id", lastEntry.Data[correlation.FieldName])
			require.Equal(t, tc.expectedMessage, lastEntry.Message)
		})
	}
}

func TestConfirmReplication(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	testRepoA, testRepoAPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	srvSocketPath := testserver.RunGitalyServer(t, cfg, setup.RegisterAll, testserver.WithDisablePraefect())

	testRepoB, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(cfg.Auth.Token)),
	}
	conn, err := grpc.Dial(srvSocketPath, connOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	equal, err := confirmChecksums(ctx, testhelper.SharedLogger(t), gitalypb.NewRepositoryServiceClient(conn), gitalypb.NewRepositoryServiceClient(conn), testRepoA, testRepoB)
	require.NoError(t, err)
	require.True(t, equal)

	gittest.WriteCommit(t, cfg, testRepoAPath, gittest.WithBranch("master"))

	equal, err = confirmChecksums(ctx, testhelper.SharedLogger(t), gitalypb.NewRepositoryServiceClient(conn), gitalypb.NewRepositoryServiceClient(conn), testRepoA, testRepoB)
	require.NoError(t, err)
	require.False(t, equal)
}

func confirmChecksums(ctx context.Context, logger log.Logger, primaryClient, replicaClient gitalypb.RepositoryServiceClient, primary, replica *gitalypb.Repository) (bool, error) {
	g, gCtx := errgroup.WithContext(ctx)

	var primaryChecksum, replicaChecksum string

	g.Go(getChecksumFunc(gCtx, primaryClient, primary, &primaryChecksum))
	g.Go(getChecksumFunc(gCtx, replicaClient, replica, &replicaChecksum))

	if err := g.Wait(); err != nil {
		return false, err
	}

	logger.WithFields(log.Fields{
		"primary_checksum": primaryChecksum,
		"replica_checksum": replicaChecksum,
	}).Info("checksum comparison completed")

	return primaryChecksum == replicaChecksum, nil
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

func TestProcessBacklog_FailedJobs(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(testhelper.Context(t))

	primaryCfg := testcfg.Build(t, testcfg.WithStorages("default"))
	testRepo, _ := gittest.CreateRepository(t, ctx, primaryCfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	primaryAddr := testserver.RunGitalyServer(t, primaryCfg, setup.RegisterAll, testserver.WithDisablePraefect())

	backupCfg := testcfg.Build(t, testcfg.WithStorages("backup"))
	backupAddr := testserver.RunGitalyServer(t, backupCfg, setup.RegisterAll, testserver.WithDisablePraefect())
	testcfg.BuildGitalySSH(t, backupCfg)
	testcfg.BuildGitalyHooks(t, backupCfg)

	primary := config.Node{
		Storage: primaryCfg.Storages[0].Name,
		Address: primaryAddr,
	}

	secondary := config.Node{
		Storage: backupCfg.Storages[0].Name,
		Address: backupAddr,
	}

	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "praefect",
				Nodes: []*config.Node{
					&primary,
					&secondary,
				},
			},
		},
	}

	queueInterceptor := datastore.NewReplicationEventQueueInterceptor(datastore.NewPostgresReplicationEventQueue(testdb.New(t)))

	// this job exists to verify that replication works
	okJob := datastore.ReplicationJob{
		RepositoryID:      1,
		Change:            datastore.UpdateRepo,
		RelativePath:      testRepo.RelativePath,
		TargetNodeStorage: secondary.Storage,
		SourceNodeStorage: primary.Storage,
		VirtualStorage:    "praefect",
	}
	event1, err := queueInterceptor.Enqueue(ctx, datastore.ReplicationEvent{Job: okJob})
	require.NoError(t, err)
	require.Equal(t, uint64(1), event1.ID)

	// this job checks flow for replication event that fails
	failJob := okJob
	failJob.Change = "invalid-operation"
	event2, err := queueInterceptor.Enqueue(ctx, datastore.ReplicationEvent{Job: failJob})
	require.NoError(t, err)
	require.Equal(t, uint64(2), event2.ID)

	logEntry := testhelper.SharedLogger(t)

	nodeMgr, err := nodes.NewManager(logEntry, conf, nil, nil, promtest.NewMockHistogramVec(), protoregistry.GitalyProtoPreregistered, nil, nil, nil)
	require.NoError(t, err)
	nodeMgr.Start(0, time.Hour)
	defer nodeMgr.Stop()

	db := testdb.New(t)
	rs := datastore.NewPostgresRepositoryStore(db, conf.StorageNames())
	require.NoError(t, rs.CreateRepository(ctx, okJob.RepositoryID, okJob.VirtualStorage, okJob.RelativePath, okJob.RelativePath, okJob.SourceNodeStorage, nil, nil, true, false))

	replMgr := NewReplMgr(
		logEntry,
		conf.StorageNames(),
		queueInterceptor,
		rs,
		nodeMgr,
		NodeSetFromNodeManager(nodeMgr),
	)
	replMgrDone := startProcessBacklog(ctx, replMgr)

	require.NoError(t, queueInterceptor.Wait(time.Minute, func(i *datastore.ReplicationEventQueueInterceptor) bool {
		return len(i.GetAcknowledgeResult()) == 4
	}))
	cancel()
	<-replMgrDone

	var dequeueCalledEffectively int
	for _, res := range queueInterceptor.GetDequeuedResult() {
		if len(res) > 0 {
			dequeueCalledEffectively++
		}
	}
	require.Equal(t, 3, dequeueCalledEffectively, "expected 1 deque to get [okJob, failJob] and 2 more for [failJob] only")

	expAcks := map[datastore.JobState][]uint64{
		datastore.JobStateFailed:    {2, 2},
		datastore.JobStateDead:      {2},
		datastore.JobStateCompleted: {1},
	}
	acks := map[datastore.JobState][]uint64{}
	for _, ack := range queueInterceptor.GetAcknowledge() {
		acks[ack.State] = append(acks[ack.State], ack.IDs...)
	}
	require.Equal(t, expAcks, acks)
}

func TestProcessBacklog_Success(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(testhelper.Context(t))

	primaryCfg := testcfg.Build(t, testcfg.WithStorages("primary"))
	primaryCfg.SocketPath = testserver.RunGitalyServer(t, primaryCfg, setup.RegisterAll, testserver.WithDisablePraefect())
	testcfg.BuildGitalySSH(t, primaryCfg)
	testcfg.BuildGitalyHooks(t, primaryCfg)

	testRepo, _ := gittest.CreateRepository(t, ctx, primaryCfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	backupCfg := testcfg.Build(t, testcfg.WithStorages("backup"))
	backupCfg.SocketPath = testserver.RunGitalyServer(t, backupCfg, setup.RegisterAll, testserver.WithDisablePraefect())
	backupLocator := gitalycfg.NewLocator(backupCfg)
	testcfg.BuildGitalySSH(t, backupCfg)
	testcfg.BuildGitalyHooks(t, backupCfg)

	primary := config.Node{
		Storage: primaryCfg.Storages[0].Name,
		Address: primaryCfg.SocketPath,
	}

	secondary := config.Node{
		Storage: backupCfg.Storages[0].Name,
		Address: backupCfg.SocketPath,
	}

	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "virtual",
				Nodes: []*config.Node{
					&primary,
					&secondary,
				},
			},
		},
	}

	queueInterceptor := datastore.NewReplicationEventQueueInterceptor(datastore.NewPostgresReplicationEventQueue(testdb.New(t)))
	queueInterceptor.OnAcknowledge(func(ctx context.Context, state datastore.JobState, ids []uint64, queue datastore.ReplicationEventQueue) ([]uint64, error) {
		ackIDs, err := queue.Acknowledge(ctx, state, ids)
		if len(ids) > 0 {
			assert.Equal(t, datastore.JobStateCompleted, state, "no fails expected")
			assert.Equal(t, []uint64{1}, ids, "all jobs must be processed at once")
		}
		return ackIDs, err
	})

	var healthUpdated int32
	queueInterceptor.OnStartHealthUpdate(func(ctx context.Context, trigger <-chan time.Time, events []datastore.ReplicationEvent) error {
		assert.Len(t, events, 1)
		atomic.AddInt32(&healthUpdated, 1)
		return nil
	})

	// Update replication job
	eventType1 := datastore.ReplicationEvent{
		Job: datastore.ReplicationJob{
			RepositoryID:      1,
			Change:            datastore.UpdateRepo,
			RelativePath:      testRepo.GetRelativePath(),
			TargetNodeStorage: secondary.Storage,
			SourceNodeStorage: primary.Storage,
			VirtualStorage:    conf.VirtualStorages[0].Name,
		},
	}

	_, err := queueInterceptor.Enqueue(ctx, eventType1)
	require.NoError(t, err)

	logEntry := testhelper.SharedLogger(t)

	nodeMgr, err := nodes.NewManager(logEntry, conf, nil, nil, promtest.NewMockHistogramVec(), protoregistry.GitalyProtoPreregistered, nil, nil, nil)
	require.NoError(t, err)
	nodeMgr.Start(0, time.Hour)
	defer nodeMgr.Stop()

	db := testdb.New(t)
	rs := datastore.NewPostgresRepositoryStore(db, conf.StorageNames())
	require.NoError(t, rs.CreateRepository(ctx, eventType1.Job.RepositoryID, eventType1.Job.VirtualStorage, eventType1.Job.VirtualStorage, eventType1.Job.RelativePath, eventType1.Job.SourceNodeStorage, nil, nil, true, false))

	replMgr := NewReplMgr(
		logEntry,
		conf.StorageNames(),
		queueInterceptor,
		rs,
		nodeMgr,
		NodeSetFromNodeManager(nodeMgr),
	)
	replMgrDone := startProcessBacklog(ctx, replMgr)

	require.NoError(t, queueInterceptor.Wait(time.Minute, func(i *datastore.ReplicationEventQueueInterceptor) bool {
		var ids []uint64
		for _, params := range i.GetAcknowledge() {
			ids = append(ids, params.IDs...)
		}
		return len(ids) == 1
	}))
	cancel()
	<-replMgrDone

	require.NoError(t, backupLocator.ValidateRepository(&gitalypb.Repository{
		StorageName:  backupCfg.Storages[0].Name,
		RelativePath: testRepo.GetRelativePath(),
	}), "repository must exist at the relative path")
}

func TestReplMgrProcessBacklog_OnlyHealthyNodes(t *testing.T) {
	t.Parallel()
	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "default",
				Nodes: []*config.Node{
					{Storage: "node-1"},
					{Storage: "node-2"},
					{Storage: "node-3"},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(testhelper.Context(t))

	var mtx sync.Mutex
	expStorages := map[string]bool{conf.VirtualStorages[0].Nodes[0].Storage: true, conf.VirtualStorages[0].Nodes[2].Storage: true}
	queueInterceptor := datastore.NewReplicationEventQueueInterceptor(datastore.NewPostgresReplicationEventQueue(testdb.New(t)))
	queueInterceptor.OnDequeue(func(_ context.Context, virtualStorageName string, storageName string, _ int, _ datastore.ReplicationEventQueue) ([]datastore.ReplicationEvent, error) {
		select {
		case <-ctx.Done():
			return nil, nil
		default:
			mtx.Lock()
			defer mtx.Unlock()
			assert.Equal(t, conf.VirtualStorages[0].Name, virtualStorageName)
			assert.True(t, expStorages[storageName], storageName, storageName)
			delete(expStorages, storageName)
			if len(expStorages) == 0 {
				cancel()
			}
			return nil, nil
		}
	})

	virtualStorage := conf.VirtualStorages[0].Name
	node1 := Node{Storage: conf.VirtualStorages[0].Nodes[0].Storage}
	node2 := Node{Storage: conf.VirtualStorages[0].Nodes[1].Storage}
	node3 := Node{Storage: conf.VirtualStorages[0].Nodes[2].Storage}

	replMgr := NewReplMgr(
		testhelper.SharedLogger(t),
		conf.StorageNames(),
		queueInterceptor,
		nil,
		StaticHealthChecker{virtualStorage: {node1.Storage, node3.Storage}},
		NodeSet{
			virtualStorage: {
				node1.Storage: node1,
				node2.Storage: node2,
				node3.Storage: node3,
			},
		},
	)
	replMgrDone := startProcessBacklog(ctx, replMgr)

	select {
	case <-ctx.Done():
		// completed by scenario
	case <-time.After(30 * time.Second):
		// strongly depends on the processing capacity
		t.Fatal("time limit expired for job to complete")
	}
	<-replMgrDone
}

type mockReplicator struct {
	Replicator
	ReplicateFunc func(ctx context.Context, event datastore.ReplicationEvent, source, target *grpc.ClientConn) error
}

func (m mockReplicator) Replicate(ctx context.Context, event datastore.ReplicationEvent, source, target *grpc.ClientConn) error {
	return m.ReplicateFunc(ctx, event, source, target)
}

func TestProcessBacklog_ReplicatesToReadOnlyPrimary(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(testhelper.Context(t))
	defer cancel()

	const virtualStorage = "virtual-storage"
	const primaryStorage = "storage-1"
	const secondaryStorage = "storage-2"
	const repositoryID = 1

	primaryConn := &grpc.ClientConn{}
	secondaryConn := &grpc.ClientConn{}

	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: virtualStorage,
				Nodes: []*config.Node{
					{Storage: primaryStorage},
					{Storage: secondaryStorage},
				},
			},
		},
	}

	queue := datastore.NewPostgresReplicationEventQueue(testdb.New(t))
	_, err := queue.Enqueue(ctx, datastore.ReplicationEvent{
		Job: datastore.ReplicationJob{
			RepositoryID:      1,
			Change:            datastore.UpdateRepo,
			RelativePath:      "ignored",
			TargetNodeStorage: primaryStorage,
			SourceNodeStorage: secondaryStorage,
			VirtualStorage:    virtualStorage,
		},
	})
	require.NoError(t, err)

	db := testdb.New(t)
	rs := datastore.NewPostgresRepositoryStore(db, conf.StorageNames())
	require.NoError(t, rs.CreateRepository(ctx, repositoryID, virtualStorage, "ignored", "ignored", primaryStorage, []string{secondaryStorage}, nil, true, false))

	replMgr := NewReplMgr(
		testhelper.SharedLogger(t),
		conf.StorageNames(),
		queue,
		rs,
		StaticHealthChecker{virtualStorage: {primaryStorage, secondaryStorage}},
		NodeSet{virtualStorage: {
			primaryStorage:   {Storage: primaryStorage, Connection: primaryConn},
			secondaryStorage: {Storage: secondaryStorage, Connection: secondaryConn},
		}},
	)

	processed := make(chan struct{})
	replMgr.replicator = mockReplicator{
		ReplicateFunc: func(ctx context.Context, event datastore.ReplicationEvent, source, target *grpc.ClientConn) error {
			require.True(t, primaryConn == target)
			require.True(t, secondaryConn == source)
			close(processed)
			return nil
		},
	}
	replMgrDone := startProcessBacklog(ctx, replMgr)
	select {
	case <-processed:
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatalf("replication job targeting read-only primary was not processed before timeout")
	}
	<-replMgrDone
}

func TestBackoffFactory(t *testing.T) {
	start := 1 * time.Microsecond
	max := 6 * time.Microsecond
	expectedBackoffs := []time.Duration{
		1 * time.Microsecond,
		2 * time.Microsecond,
		4 * time.Microsecond,
		6 * time.Microsecond,
		6 * time.Microsecond,
		6 * time.Microsecond,
	}
	b, reset := ExpBackoffFactory{Start: start, Max: max}.Create()
	for _, expectedBackoff := range expectedBackoffs {
		require.Equal(t, expectedBackoff, b())
	}

	reset()
	require.Equal(t, start, b())
}

func newRepositoryClient(t *testing.T, serverSocketPath, token string) gitalypb.RepositoryServiceClient {
	t.Helper()

	conn, err := grpc.Dial(
		serverSocketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(token)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	return gitalypb.NewRepositoryServiceClient(conn)
}

func TestSubtractUint64(t *testing.T) {
	testCases := []struct {
		desc  string
		left  []uint64
		right []uint64
		exp   []uint64
	}{
		{desc: "empty left", left: nil, right: []uint64{1, 2}, exp: nil},
		{desc: "empty right", left: []uint64{1, 2}, right: []uint64{}, exp: []uint64{1, 2}},
		{desc: "some exists", left: []uint64{1, 2, 3, 4, 5}, right: []uint64{2, 4, 5}, exp: []uint64{1, 3}},
		{desc: "nothing exists", left: []uint64{10, 20}, right: []uint64{100, 200}, exp: []uint64{10, 20}},
		{desc: "duplicates exists", left: []uint64{1, 1, 2, 3, 3, 4, 4, 5}, right: []uint64{3, 4, 4, 5}, exp: []uint64{1, 1, 2}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			require.Equal(t, testCase.exp, subtractUint64(testCase.left, testCase.right))
		})
	}
}

func TestReplMgr_ProcessStale(t *testing.T) {
	logger := testhelper.SharedLogger(t)
	hook := testhelper.AddLoggerHook(logger)

	queue := datastore.NewReplicationEventQueueInterceptor(nil)
	mgr := NewReplMgr(logger.WithField("test", t.Name()), nil, queue, datastore.MockRepositoryStore{}, nil, nil)
	ctx, cancel := context.WithCancel(testhelper.Context(t))

	const iterations = 3
	var counter int
	queue.OnAcknowledgeStale(func(context.Context, time.Duration) (int64, error) {
		counter++
		if counter >= iterations {
			cancel()
			return 0, assert.AnError
		}
		return 0, nil
	})

	ticker := helper.NewManualTicker()

	done := mgr.ProcessStale(ctx, ticker, time.Second)
	for i := 0; i < iterations; i++ {
		ticker.Tick()
	}
	<-done

	require.Equal(t, iterations, counter)
	entries := hook.AllEntries()
	require.Len(t, entries, 1)
	lastEntry := entries[0]
	require.Equal(t, logrus.ErrorLevel, lastEntry.Level)
	require.Equal(t, "background periodical acknowledgement for stale replication jobs", lastEntry.Message)
	require.Equal(t, "replication_manager", lastEntry.Data["component"])
	require.Equal(t, assert.AnError, lastEntry.Data["error"])
}
