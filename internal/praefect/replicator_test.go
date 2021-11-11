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
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v14/auth"
	"gitlab.com/gitlab-org/gitaly/v14/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/objectpool"
	gconfig "gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v14/internal/middleware/metadatahandler"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/promtest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testassert"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"gitlab.com/gitlab-org/labkit/correlation"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func TestReplMgr_ProcessBacklog(t *testing.T) {
	t.Parallel()
	primaryCfg, testRepo, testRepoPath := testcfg.BuildWithRepo(t, testcfg.WithStorages("primary"))
	primaryCfg.SocketPath = testserver.RunGitalyServer(t, primaryCfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
	testcfg.BuildGitalySSH(t, primaryCfg)
	testcfg.BuildGitalyHooks(t, primaryCfg)

	backupCfg, _, _ := testcfg.BuildWithRepo(t, testcfg.WithStorages("backup"))
	backupCfg.SocketPath = testserver.RunGitalyServer(t, backupCfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
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
	objectPoolPath := gittest.NewObjectPoolName(t)
	pool, err := objectpool.NewObjectPool(
		primaryCfg,
		gconfig.NewLocator(primaryCfg),
		git.NewExecCommandFactory(primaryCfg),
		nil,
		transaction.NewManager(primaryCfg, backchannel.NewRegistry()),
		testRepo.GetStorageName(),
		objectPoolPath,
	)
	require.NoError(t, err)

	poolCtx, cancel := testhelper.Context()
	defer cancel()

	require.NoError(t, pool.Create(poolCtx, testRepo))
	require.NoError(t, pool.Link(poolCtx, testRepo))

	// replicate object pool repository to target node
	poolRepository := pool.ToProto().GetRepository()
	targetObjectPoolRepo := proto.Clone(poolRepository).(*gitalypb.Repository)
	targetObjectPoolRepo.StorageName = backupCfg.Storages[0].Name

	ctx, cancel := testhelper.Context()
	defer cancel()

	injectedCtx := metadata.NewOutgoingContext(ctx, testcfg.GitalyServersMetadataFromCfg(t, primaryCfg))

	repoClient := newRepositoryClient(t, backupCfg.SocketPath, backupCfg.Auth.Token)
	_, err = repoClient.ReplicateRepository(injectedCtx, &gitalypb.ReplicateRepositoryRequest{
		Repository: targetObjectPoolRepo,
		Source:     poolRepository,
	})
	require.NoError(t, err)

	entry := testhelper.DiscardTestEntry(t)

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
				RelativePath:      testRepo.GetRelativePath(),
			},
			State:   datastore.JobStateReady,
			Attempt: 3,
			Meta:    datastore.Params{metadatahandler.CorrelationIDKey: "correlation-id"},
		})
	}
	require.Len(t, events, 1)

	commitID := gittest.WriteCommit(t, primaryCfg, testRepoPath, gittest.WithBranch("master"))

	var mockReplicationLatencyHistogramVec promtest.MockHistogramVec
	var mockReplicationDelayHistogramVec promtest.MockHistogramVec

	logger := testhelper.DiscardTestLogger(t)
	loggerHook := test.NewLocal(logger)

	queue := datastore.NewReplicationEventQueueInterceptor(datastore.NewPostgresReplicationEventQueue(glsql.NewDB(t)))
	queue.OnAcknowledge(func(ctx context.Context, state datastore.JobState, ids []uint64, queue datastore.ReplicationEventQueue) ([]uint64, error) {
		cancel() // when it is called we know that replication is finished
		return queue.Acknowledge(ctx, state, ids)
	})

	loggerEntry := logger.WithField("test", t.Name())
	_, err = queue.Enqueue(ctx, events[0])
	require.NoError(t, err)

	db := glsql.NewDB(t)
	rs := datastore.NewPostgresRepositoryStore(db, conf.StorageNames())
	require.NoError(t, rs.CreateRepository(ctx, repositoryID, conf.VirtualStorages[0].Name, testRepo.GetRelativePath(), testRepo.GetRelativePath(), shard.Primary.GetStorage(), nil, nil, true, false))

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
		[]interface{}{logEntries[2].Message, logEntries[2].Data["virtual_storage"], logEntries[2].Data[logWithCorrID]},
	)

	dequeuedEvent := logEntries[2].Data["event"].(datastore.ReplicationEvent)
	require.Equal(t, datastore.JobStateInProgress, dequeuedEvent.State)
	require.Equal(t, []string{"backup", "primary"}, []string{dequeuedEvent.Job.TargetNodeStorage, dequeuedEvent.Job.SourceNodeStorage})

	require.Equal(t,
		[]interface{}{"replication job processing finished", "virtual", datastore.JobStateCompleted, "correlation-id"},
		[]interface{}{logEntries[3].Message, logEntries[3].Data["virtual_storage"], logEntries[3].Data["new_state"], logEntries[3].Data[logWithCorrID]},
	)

	replicatedPath := filepath.Join(backupCfg.Storages[0].Path, testRepo.GetRelativePath())

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

func TestReplicatorDowngradeAttempt(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

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

			logger := testhelper.DiscardTestLogger(t)
			hook := test.NewLocal(logger)
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

			require.Equal(t, logrus.InfoLevel, hook.LastEntry().Level)
			require.Equal(t, returnedErr, hook.LastEntry().Data["error"])
			require.Equal(t, "correlation-id", hook.LastEntry().Data[logWithCorrID])
			require.Equal(t, tc.expectedMessage, hook.LastEntry().Message)
		})
	}
}

func TestReplicator_PropagateReplicationJob(t *testing.T) {
	t.Parallel()
	primaryStorage, secondaryStorage := "internal-gitaly-0", "internal-gitaly-1"

	primCfg := testcfg.Build(t, testcfg.WithStorages(primaryStorage))
	primaryServer, primarySocketPath := runMockRepositoryServer(t, primCfg)

	secCfg := testcfg.Build(t, testcfg.WithStorages(secondaryStorage))
	secondaryServer, secondarySocketPath := runMockRepositoryServer(t, secCfg)

	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "default",
				Nodes: []*config.Node{
					{
						Storage: primaryStorage,
						Address: primarySocketPath,
					},
					{
						Storage: secondaryStorage,
						Address: secondarySocketPath,
					},
				},
			},
		},
	}

	// We need to await for the replication event to make a complete roundtrip to the remote.
	// Because send to the channel happens during in-flight request there are ongoing filesystem
	// operations related to caching. The cleanup happens before all IO cache operations finished
	// those resulting to:
	// unlinkat /tmp/gitaly-222007427/381349228/storages.d/internal-gitaly-1/+gitaly/state/path/to/repo: directory not empty
	// By using WaitGroup we are sure the test cleanup will be started after all replication
	// requests are completed, so no running cache IO operations happen.
	queue := datastore.NewReplicationEventQueueInterceptor(datastore.NewPostgresReplicationEventQueue(glsql.NewDB(t)))
	var wg sync.WaitGroup
	queue.OnEnqueue(func(ctx context.Context, event datastore.ReplicationEvent, queue datastore.ReplicationEventQueue) (datastore.ReplicationEvent, error) {
		wg.Add(1)
		return queue.Enqueue(ctx, event)
	})
	queue.OnAcknowledge(func(ctx context.Context, state datastore.JobState, eventIDs []uint64, queue datastore.ReplicationEventQueue) ([]uint64, error) {
		acknowledged, err := queue.Acknowledge(ctx, state, eventIDs)
		wg.Add(-len(eventIDs))
		return acknowledged, err
	})
	logEntry := testhelper.DiscardTestEntry(t)

	nodeMgr, err := nodes.NewManager(logEntry, conf, nil, nil, promtest.NewMockHistogramVec(), protoregistry.GitalyProtoPreregistered, nil, nil, nil)
	require.NoError(t, err)
	nodeMgr.Start(0, time.Hour)
	defer nodeMgr.Stop()

	txMgr := transactions.NewManager(conf)

	repositoryRelativePath := "/path/to/repo"

	rs := datastore.MockRepositoryStore{
		GetConsistentStoragesFunc: func(ctx context.Context, virtualStorage, relativePath string) (string, map[string]struct{}, error) {
			return repositoryRelativePath, nil, nil
		},
		GetReplicaPathFunc: func(ctx context.Context, repositoryID int64) (string, error) {
			return repositoryRelativePath, nil
		},
	}

	coordinator := NewCoordinator(
		queue,
		rs,
		NewNodeManagerRouter(nodeMgr, rs),
		txMgr,
		conf,
		protoregistry.GitalyProtoPreregistered,
	)

	replmgr := NewReplMgr(logEntry, conf.StorageNames(), queue, rs, nodeMgr, NodeSetFromNodeManager(nodeMgr))

	prf := NewGRPCServer(conf, logEntry, protoregistry.GitalyProtoPreregistered, coordinator.StreamDirector, nodeMgr, txMgr, queue, rs, nil, nil, nil, nil)

	listener, port := listenAvailPort(t)
	ctx, cancel := testhelper.Context()
	defer cancel()

	go prf.Serve(listener)
	defer prf.Stop()

	cc := dialLocalPort(t, port, false)
	repositoryClient := gitalypb.NewRepositoryServiceClient(cc)
	refClient := gitalypb.NewRefServiceClient(cc)
	defer listener.Close()
	defer cc.Close()

	repository := &gitalypb.Repository{
		StorageName:  conf.VirtualStorages[0].Name,
		RelativePath: repositoryRelativePath,
	}

	_, err = repositoryClient.GarbageCollect(ctx, &gitalypb.GarbageCollectRequest{
		Repository:   repository,
		CreateBitmap: true,
		Prune:        true,
	})
	require.NoError(t, err)

	_, err = repositoryClient.RepackFull(ctx, &gitalypb.RepackFullRequest{Repository: repository, CreateBitmap: false})
	require.NoError(t, err)

	_, err = repositoryClient.RepackIncremental(ctx, &gitalypb.RepackIncrementalRequest{Repository: repository})
	require.NoError(t, err)

	_, err = repositoryClient.Cleanup(ctx, &gitalypb.CleanupRequest{Repository: repository})
	require.NoError(t, err)

	_, err = repositoryClient.WriteCommitGraph(ctx, &gitalypb.WriteCommitGraphRequest{
		Repository: repository,
		// This is not a valid split strategy, but we currently only support a
		// single default split strategy with value 0. So we just test with an
		// invalid split strategy to check that a non-default value gets properly
		// replicated.
		SplitStrategy: 1,
	})
	require.NoError(t, err)

	_, err = repositoryClient.MidxRepack(ctx, &gitalypb.MidxRepackRequest{Repository: repository})
	require.NoError(t, err)

	_, err = repositoryClient.OptimizeRepository(ctx, &gitalypb.OptimizeRepositoryRequest{Repository: repository})
	require.NoError(t, err)

	_, err = refClient.PackRefs(ctx, &gitalypb.PackRefsRequest{
		Repository: repository,
		AllRefs:    true,
	})
	require.NoError(t, err)

	primaryRepository := &gitalypb.Repository{StorageName: primaryStorage, RelativePath: repositoryRelativePath}
	expectedPrimaryGcReq := &gitalypb.GarbageCollectRequest{
		Repository:   primaryRepository,
		CreateBitmap: true,
		Prune:        true,
	}
	expectedPrimaryRepackFullReq := &gitalypb.RepackFullRequest{
		Repository:   primaryRepository,
		CreateBitmap: false,
	}
	expectedPrimaryRepackIncrementalReq := &gitalypb.RepackIncrementalRequest{
		Repository: primaryRepository,
	}
	expectedPrimaryCleanup := &gitalypb.CleanupRequest{
		Repository: primaryRepository,
	}
	expectedPrimaryWriteCommitGraph := &gitalypb.WriteCommitGraphRequest{
		Repository:    primaryRepository,
		SplitStrategy: 1,
	}
	expectedPrimaryMidxRepack := &gitalypb.MidxRepackRequest{
		Repository: primaryRepository,
	}
	expectedPrimaryOptimizeRepository := &gitalypb.OptimizeRepositoryRequest{
		Repository: primaryRepository,
	}
	expectedPrimaryPackRefs := &gitalypb.PackRefsRequest{
		Repository: primaryRepository,
		AllRefs:    true,
	}

	replMgrDone := startProcessBacklog(ctx, replmgr)

	// ensure primary gitaly server received the expected requests
	waitForRequest(t, primaryServer.gcChan, expectedPrimaryGcReq, 5*time.Second)
	waitForRequest(t, primaryServer.repackIncrChan, expectedPrimaryRepackIncrementalReq, 5*time.Second)
	waitForRequest(t, primaryServer.repackFullChan, expectedPrimaryRepackFullReq, 5*time.Second)
	waitForRequest(t, primaryServer.cleanupChan, expectedPrimaryCleanup, 5*time.Second)
	waitForRequest(t, primaryServer.writeCommitGraphChan, expectedPrimaryWriteCommitGraph, 5*time.Second)
	waitForRequest(t, primaryServer.midxRepackChan, expectedPrimaryMidxRepack, 5*time.Second)
	waitForRequest(t, primaryServer.optimizeRepositoryChan, expectedPrimaryOptimizeRepository, 5*time.Second)
	waitForRequest(t, primaryServer.packRefsChan, expectedPrimaryPackRefs, 5*time.Second)

	secondaryRepository := &gitalypb.Repository{StorageName: secondaryStorage, RelativePath: repositoryRelativePath}

	expectedSecondaryGcReq := expectedPrimaryGcReq
	expectedSecondaryGcReq.Repository = secondaryRepository

	expectedSecondaryRepackFullReq := expectedPrimaryRepackFullReq
	expectedSecondaryRepackFullReq.Repository = secondaryRepository

	expectedSecondaryRepackIncrementalReq := expectedPrimaryRepackIncrementalReq
	expectedSecondaryRepackIncrementalReq.Repository = secondaryRepository

	expectedSecondaryCleanup := expectedPrimaryCleanup
	expectedSecondaryCleanup.Repository = secondaryRepository

	expectedSecondaryWriteCommitGraph := expectedPrimaryWriteCommitGraph
	expectedSecondaryWriteCommitGraph.Repository = secondaryRepository

	expectedSecondaryMidxRepack := expectedPrimaryMidxRepack
	expectedSecondaryMidxRepack.Repository = secondaryRepository

	expectedSecondaryOptimizeRepository := expectedPrimaryOptimizeRepository
	expectedSecondaryOptimizeRepository.Repository = secondaryRepository

	expectedSecondaryPackRefs := expectedPrimaryPackRefs
	expectedSecondaryPackRefs.Repository = secondaryRepository

	// ensure secondary gitaly server received the expected requests
	waitForRequest(t, secondaryServer.gcChan, expectedSecondaryGcReq, 5*time.Second)
	waitForRequest(t, secondaryServer.repackIncrChan, expectedSecondaryRepackIncrementalReq, 5*time.Second)
	waitForRequest(t, secondaryServer.repackFullChan, expectedSecondaryRepackFullReq, 5*time.Second)
	waitForRequest(t, secondaryServer.cleanupChan, expectedSecondaryCleanup, 5*time.Second)
	waitForRequest(t, secondaryServer.writeCommitGraphChan, expectedSecondaryWriteCommitGraph, 5*time.Second)
	waitForRequest(t, secondaryServer.midxRepackChan, expectedSecondaryMidxRepack, 5*time.Second)
	waitForRequest(t, secondaryServer.optimizeRepositoryChan, expectedSecondaryOptimizeRepository, 5*time.Second)
	waitForRequest(t, secondaryServer.packRefsChan, expectedSecondaryPackRefs, 5*time.Second)
	wg.Wait()
	cancel()
	<-replMgrDone
}

type mockServer struct {
	gcChan, repackFullChan, repackIncrChan, cleanupChan, writeCommitGraphChan, midxRepackChan, optimizeRepositoryChan, packRefsChan chan proto.Message

	gitalypb.UnimplementedRepositoryServiceServer
	gitalypb.UnimplementedRefServiceServer
}

func newMockRepositoryServer() *mockServer {
	return &mockServer{
		gcChan:                 make(chan proto.Message),
		repackFullChan:         make(chan proto.Message),
		repackIncrChan:         make(chan proto.Message),
		cleanupChan:            make(chan proto.Message),
		writeCommitGraphChan:   make(chan proto.Message),
		midxRepackChan:         make(chan proto.Message),
		optimizeRepositoryChan: make(chan proto.Message),
		packRefsChan:           make(chan proto.Message),
	}
}

func (m *mockServer) GarbageCollect(ctx context.Context, in *gitalypb.GarbageCollectRequest) (*gitalypb.GarbageCollectResponse, error) {
	go func() {
		m.gcChan <- in
	}()
	return &gitalypb.GarbageCollectResponse{}, nil
}

func (m *mockServer) RepackFull(ctx context.Context, in *gitalypb.RepackFullRequest) (*gitalypb.RepackFullResponse, error) {
	go func() {
		m.repackFullChan <- in
	}()
	return &gitalypb.RepackFullResponse{}, nil
}

func (m *mockServer) RepackIncremental(ctx context.Context, in *gitalypb.RepackIncrementalRequest) (*gitalypb.RepackIncrementalResponse, error) {
	go func() {
		m.repackIncrChan <- in
	}()
	return &gitalypb.RepackIncrementalResponse{}, nil
}

func (m *mockServer) Cleanup(ctx context.Context, in *gitalypb.CleanupRequest) (*gitalypb.CleanupResponse, error) {
	go func() {
		m.cleanupChan <- in
	}()
	return &gitalypb.CleanupResponse{}, nil
}

func (m *mockServer) WriteCommitGraph(ctx context.Context, in *gitalypb.WriteCommitGraphRequest) (*gitalypb.WriteCommitGraphResponse, error) {
	go func() {
		m.writeCommitGraphChan <- in
	}()
	return &gitalypb.WriteCommitGraphResponse{}, nil
}

func (m *mockServer) MidxRepack(ctx context.Context, in *gitalypb.MidxRepackRequest) (*gitalypb.MidxRepackResponse, error) {
	go func() {
		m.midxRepackChan <- in
	}()
	return &gitalypb.MidxRepackResponse{}, nil
}

func (m *mockServer) OptimizeRepository(ctx context.Context, in *gitalypb.OptimizeRepositoryRequest) (*gitalypb.OptimizeRepositoryResponse, error) {
	go func() {
		m.optimizeRepositoryChan <- in
	}()
	return &gitalypb.OptimizeRepositoryResponse{}, nil
}

func (m *mockServer) PackRefs(ctx context.Context, in *gitalypb.PackRefsRequest) (*gitalypb.PackRefsResponse, error) {
	go func() {
		m.packRefsChan <- in
	}()
	return &gitalypb.PackRefsResponse{}, nil
}

func runMockRepositoryServer(t *testing.T, cfg gconfig.Cfg) (*mockServer, string) {
	mockServer := newMockRepositoryServer()

	addr := testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterRepositoryServiceServer(srv, mockServer)
		gitalypb.RegisterRefServiceServer(srv, mockServer)
	}, testserver.WithDisablePraefect())
	return mockServer, addr
}

func waitForRequest(t *testing.T, ch chan proto.Message, expected proto.Message, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case req := <-ch:
		testassert.ProtoEqual(t, expected, req)
		close(ch)
	case <-timer.C:
		t.Fatal("timed out")
	}
}

func TestConfirmReplication(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	cfg, testRepoA, testRepoAPath := testcfg.BuildWithRepo(t)
	srvSocketPath := testserver.RunGitalyServer(t, cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())

	testRepoB, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])

	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(cfg.Auth.Token)),
	}
	conn, err := grpc.Dial(srvSocketPath, connOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	equal, err := confirmChecksums(ctx, testhelper.DiscardTestLogger(t), gitalypb.NewRepositoryServiceClient(conn), gitalypb.NewRepositoryServiceClient(conn), testRepoA, testRepoB)
	require.NoError(t, err)
	require.True(t, equal)

	gittest.WriteCommit(t, cfg, testRepoAPath, gittest.WithBranch("master"))

	equal, err = confirmChecksums(ctx, testhelper.DiscardTestLogger(t), gitalypb.NewRepositoryServiceClient(conn), gitalypb.NewRepositoryServiceClient(conn), testRepoA, testRepoB)
	require.NoError(t, err)
	require.False(t, equal)
}

func confirmChecksums(ctx context.Context, logger logrus.FieldLogger, primaryClient, replicaClient gitalypb.RepositoryServiceClient, primary, replica *gitalypb.Repository) (bool, error) {
	g, gCtx := errgroup.WithContext(ctx)

	var primaryChecksum, replicaChecksum string

	g.Go(getChecksumFunc(gCtx, primaryClient, primary, &primaryChecksum))
	g.Go(getChecksumFunc(gCtx, replicaClient, replica, &replicaChecksum))

	if err := g.Wait(); err != nil {
		return false, err
	}

	logger.WithFields(logrus.Fields{
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
	primaryCfg, testRepo, _ := testcfg.BuildWithRepo(t, testcfg.WithStorages("default"))
	primaryAddr := testserver.RunGitalyServer(t, primaryCfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())

	backupCfg, _, _ := testcfg.BuildWithRepo(t, testcfg.WithStorages("backup"))
	backupAddr := testserver.RunGitalyServer(t, backupCfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
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

	ctx, cancel := testhelper.Context()
	defer cancel()

	queueInterceptor := datastore.NewReplicationEventQueueInterceptor(datastore.NewPostgresReplicationEventQueue(glsql.NewDB(t)))

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

	logEntry := testhelper.DiscardTestEntry(t)

	nodeMgr, err := nodes.NewManager(logEntry, conf, nil, nil, promtest.NewMockHistogramVec(), protoregistry.GitalyProtoPreregistered, nil, nil, nil)
	require.NoError(t, err)
	nodeMgr.Start(0, time.Hour)
	defer nodeMgr.Stop()

	db := glsql.NewDB(t)
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
	primaryCfg, testRepo, _ := testcfg.BuildWithRepo(t, testcfg.WithStorages("primary"))
	primaryCfg.SocketPath = testserver.RunGitalyServer(t, primaryCfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
	testcfg.BuildGitalySSH(t, primaryCfg)
	testcfg.BuildGitalyHooks(t, primaryCfg)

	backupCfg, _, _ := testcfg.BuildWithRepo(t, testcfg.WithStorages("backup"))
	backupCfg.SocketPath = testserver.RunGitalyServer(t, backupCfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
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

	ctx, cancel := testhelper.Context()
	defer cancel()

	queueInterceptor := datastore.NewReplicationEventQueueInterceptor(datastore.NewPostgresReplicationEventQueue(glsql.NewDB(t)))
	queueInterceptor.OnAcknowledge(func(ctx context.Context, state datastore.JobState, ids []uint64, queue datastore.ReplicationEventQueue) ([]uint64, error) {
		ackIDs, err := queue.Acknowledge(ctx, state, ids)
		if len(ids) > 0 {
			assert.Equal(t, datastore.JobStateCompleted, state, "no fails expected")
			assert.Equal(t, []uint64{1, 3, 4}, ids, "all jobs must be processed at once")
		}
		return ackIDs, err
	})

	var healthUpdated int32
	queueInterceptor.OnStartHealthUpdate(func(ctx context.Context, trigger <-chan time.Time, events []datastore.ReplicationEvent) error {
		assert.Len(t, events, 3)
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

	_, err = queueInterceptor.Enqueue(ctx, eventType1)
	require.NoError(t, err)

	renameTo1 := filepath.Join(testRepo.GetRelativePath(), "..", filepath.Base(testRepo.GetRelativePath())+"-mv1")
	fullNewPath1 := filepath.Join(backupCfg.Storages[0].Path, renameTo1)

	renameTo2 := filepath.Join(renameTo1, "..", filepath.Base(testRepo.GetRelativePath())+"-mv2")
	fullNewPath2 := filepath.Join(backupCfg.Storages[0].Path, renameTo2)

	// Rename replication job
	eventType2 := datastore.ReplicationEvent{
		Job: datastore.ReplicationJob{
			Change:            datastore.RenameRepo,
			RelativePath:      testRepo.GetRelativePath(),
			TargetNodeStorage: secondary.Storage,
			SourceNodeStorage: primary.Storage,
			VirtualStorage:    conf.VirtualStorages[0].Name,
			Params:            datastore.Params{"RelativePath": renameTo1},
		},
	}

	_, err = queueInterceptor.Enqueue(ctx, eventType2)
	require.NoError(t, err)

	// Rename replication job
	eventType3 := datastore.ReplicationEvent{
		Job: datastore.ReplicationJob{
			Change:            datastore.RenameRepo,
			RelativePath:      renameTo1,
			TargetNodeStorage: secondary.Storage,
			SourceNodeStorage: primary.Storage,
			VirtualStorage:    conf.VirtualStorages[0].Name,
			Params:            datastore.Params{"RelativePath": renameTo2},
		},
	}
	require.NoError(t, err)

	_, err = queueInterceptor.Enqueue(ctx, eventType3)
	require.NoError(t, err)

	logEntry := testhelper.DiscardTestEntry(t)

	nodeMgr, err := nodes.NewManager(logEntry, conf, nil, nil, promtest.NewMockHistogramVec(), protoregistry.GitalyProtoPreregistered, nil, nil, nil)
	require.NoError(t, err)
	nodeMgr.Start(0, time.Hour)
	defer nodeMgr.Stop()

	db := glsql.NewDB(t)
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
		return len(ids) == 3
	}))
	cancel()
	<-replMgrDone

	require.NoDirExists(t, fullNewPath1, "repository must be moved from %q to the new location", fullNewPath1)
	require.True(t, storage.IsGitDirectory(fullNewPath2), "repository must exist at new last RenameRepository location")
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

	ctx, cancel := testhelper.Context()

	var mtx sync.Mutex
	expStorages := map[string]bool{conf.VirtualStorages[0].Nodes[0].Storage: true, conf.VirtualStorages[0].Nodes[2].Storage: true}
	queueInterceptor := datastore.NewReplicationEventQueueInterceptor(datastore.NewPostgresReplicationEventQueue(glsql.NewDB(t)))
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
		testhelper.DiscardTestEntry(t),
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
	ctx, cancel := testhelper.Context()
	defer cancel()

	const virtualStorage = "virtal-storage"
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

	queue := datastore.NewPostgresReplicationEventQueue(glsql.NewDB(t))
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

	db := glsql.NewDB(t)
	rs := datastore.NewPostgresRepositoryStore(db, conf.StorageNames())
	require.NoError(t, rs.CreateRepository(ctx, repositoryID, virtualStorage, "ignored", "ignored", primaryStorage, []string{secondaryStorage}, nil, true, false))

	replMgr := NewReplMgr(
		testhelper.DiscardTestEntry(t),
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
		grpc.WithInsecure(),
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
	logger := testhelper.DiscardTestLogger(t)
	hook := test.NewLocal(logger)

	queue := datastore.NewReplicationEventQueueInterceptor(nil)
	mgr := NewReplMgr(logger.WithField("test", t.Name()), nil, queue, datastore.MockRepositoryStore{}, nil, nil)

	var counter int32
	queue.OnAcknowledgeStale(func(ctx context.Context, duration time.Duration) error {
		counter++
		if counter > 2 {
			return assert.AnError
		}
		return nil
	})

	ctx, cancel := testhelper.Context()
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 350*time.Millisecond)
	defer cancel()

	done := mgr.ProcessStale(ctx, 100*time.Millisecond, time.Second)

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "execution had stuck")
	case <-done:
	}

	require.Equal(t, int32(3), counter)
	require.Len(t, hook.Entries, 1)
	require.Equal(t, logrus.ErrorLevel, hook.LastEntry().Level)
	require.Equal(t, "background periodical acknowledgement for stale replication jobs", hook.LastEntry().Message)
	require.Equal(t, "replication_manager", hook.LastEntry().Data["component"])
	require.Equal(t, assert.AnError, hook.LastEntry().Data["error"])
}
