package praefect

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/datastructure"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	gconfig "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/listenmux"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/proxy"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/nodes/tracker"
	serversvc "gitlab.com/gitlab-org/gitaly/v16/internal/praefect/service/server"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/service/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/promtest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v16/internal/version"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestNewBackchannelServerFactory(t *testing.T) {
	logger := testhelper.SharedLogger(t)
	mgr := transactions.NewManager(config.Config{}, logger)
	registry := backchannel.NewRegistry()

	lm := listenmux.New(insecure.NewCredentials())
	lm.Register(backchannel.NewServerHandshaker(logger, registry, nil))

	server := grpc.NewServer(
		grpc.Creds(lm),
		grpc.UnknownServiceHandler(func(srv interface{}, stream grpc.ServerStream) error {
			id, err := backchannel.GetPeerID(stream.Context())
			if !assert.NoError(t, err) {
				return err
			}

			backchannelConn, err := registry.Backchannel(id)
			if !assert.NoError(t, err) {
				return err
			}

			resp, err := gitalypb.NewRefTransactionClient(backchannelConn).VoteTransaction(
				stream.Context(), &gitalypb.VoteTransactionRequest{
					ReferenceUpdatesHash: voting.VoteFromData([]byte{}).Bytes(),
				},
			)
			assert.Nil(t, resp)

			return err
		}),
	)
	defer server.Stop()

	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	go testhelper.MustServe(t, server, ln)
	ctx := testhelper.Context(t)

	nodeSet, err := DialNodes(ctx, []*config.VirtualStorage{{
		Name:  "default",
		Nodes: []*config.Node{{Storage: "gitaly-1", Address: "tcp://" + ln.Addr().String()}},
	}}, nil, nil,
		backchannel.NewClientHandshaker(
			logger,
			NewBackchannelServerFactory(
				logger,
				transaction.NewServer(mgr),
				nil,
			),
			backchannel.DefaultConfiguration(),
		), nil, testhelper.SharedLogger(t))
	require.NoError(t, err)
	defer nodeSet.Close()

	clientConn := nodeSet["default"]["gitaly-1"].Connection
	testhelper.RequireGrpcError(t,
		status.Error(codes.NotFound, "transaction not found: 0"),
		clientConn.Invoke(ctx, "/Service/Method", &gitalypb.CreateBranchRequest{}, &gitalypb.CreateBranchResponse{}),
	)
}

func TestGitalyServerInfo(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	t.Run("gitaly responds with ok", func(t *testing.T) {
		firstCfg := testcfg.Build(t, testcfg.WithStorages("praefect-internal-1"))
		firstCfg.SocketPath = testserver.RunGitalyServer(t, firstCfg, setup.RegisterAll, testserver.WithDisablePraefect())

		secondCfg := testcfg.Build(t, testcfg.WithStorages("praefect-internal-2"))
		secondCfg.SocketPath = testserver.RunGitalyServer(t, secondCfg, setup.RegisterAll, testserver.WithDisablePraefect())

		require.NoError(t, storage.WriteMetadataFile(firstCfg.Storages[0].Path))
		firstMetadata, err := storage.ReadMetadataFile(firstCfg.Storages[0].Path)
		require.NoError(t, err)

		conf := config.Config{
			VirtualStorages: []*config.VirtualStorage{
				{
					Name: "passthrough-filesystem-id",
					Nodes: []*config.Node{
						{
							Storage: firstCfg.Storages[0].Name,
							Address: firstCfg.SocketPath,
						},
					},
				},
				{
					Name: "virtual-storage",
					Nodes: []*config.Node{
						{
							Storage: firstCfg.Storages[0].Name,
							Address: firstCfg.SocketPath,
						},
						{
							Storage: secondCfg.Storages[0].Name,
							Address: secondCfg.SocketPath,
						},
					},
				},
			},
		}

		nodeSet, err := DialNodes(ctx, conf.VirtualStorages, nil, nil, nil, nil, testhelper.SharedLogger(t))
		require.NoError(t, err)
		t.Cleanup(nodeSet.Close)

		cc, _, cleanup := RunPraefectServer(t, ctx, conf, BuildOptions{
			WithConnections: nodeSet.Connections(),
		})
		t.Cleanup(cleanup)

		gitVersion, err := gittest.NewCommandFactory(t, firstCfg).GitVersion(ctx)
		require.NoError(t, err)

		expected := &gitalypb.ServerInfoResponse{
			ServerVersion: version.GetVersion(),
			GitVersion:    gitVersion.String(),
			StorageStatuses: []*gitalypb.ServerInfoResponse_StorageStatus{
				{
					StorageName:       conf.VirtualStorages[0].Name,
					FilesystemId:      firstMetadata.GitalyFilesystemID,
					Readable:          true,
					Writeable:         true,
					ReplicationFactor: 1,
				},
				{
					StorageName:       conf.VirtualStorages[1].Name,
					FilesystemId:      serversvc.DeriveFilesystemID(conf.VirtualStorages[1].Name).String(),
					Readable:          true,
					Writeable:         true,
					ReplicationFactor: 2,
				},
			},
		}

		client := gitalypb.NewServerServiceClient(cc)
		actual, err := client.ServerInfo(ctx, &gitalypb.ServerInfoRequest{})
		require.NoError(t, err)
		for _, ss := range actual.StorageStatuses {
			ss.FsType = ""
		}

		// sort the storages by name so they match the expected
		sort.Slice(actual.StorageStatuses, func(i, j int) bool {
			return actual.StorageStatuses[i].StorageName < actual.StorageStatuses[j].StorageName
		})

		require.True(t, proto.Equal(expected, actual), "expected: %v, got: %v", expected, actual)
	})

	t.Run("gitaly responds with error", func(t *testing.T) {
		cfg := testcfg.Build(t)

		conf := config.Config{
			VirtualStorages: []*config.VirtualStorage{
				{
					Name: "virtual-storage",
					Nodes: []*config.Node{
						{
							Storage: cfg.Storages[0].Name,
							Token:   cfg.Auth.Token,
							Address: "unix:///invalid.addr",
						},
					},
				},
			},
		}

		nodeSet, err := DialNodes(ctx, conf.VirtualStorages, nil, nil, nil, nil, testhelper.SharedLogger(t))
		require.NoError(t, err)
		t.Cleanup(nodeSet.Close)

		cc, _, cleanup := RunPraefectServer(t, ctx, conf, BuildOptions{
			WithConnections: nodeSet.Connections(),
		})
		t.Cleanup(cleanup)

		client := gitalypb.NewServerServiceClient(cc)
		actual, err := client.ServerInfo(ctx, &gitalypb.ServerInfoRequest{})
		require.NoError(t, err, "we expect praefect's server info to fail open even if the gitaly calls result in an error")
		require.Empty(t, actual.StorageStatuses, "got: %v", actual)
	})
}

func TestGitalyServerInfoBadNode(t *testing.T) {
	t.Parallel()
	gitalySocket := testhelper.GetTemporaryGitalySocketFileName(t)
	healthSrv := testhelper.NewServerWithHealth(t, gitalySocket)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_UNKNOWN)

	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Nodes: []*config.Node{
					{
						Storage: "praefect-internal-1",
						Address: "unix://" + gitalySocket,
						Token:   "abc",
					},
				},
			},
		},
	}
	ctx := testhelper.Context(t)

	nodes, err := DialNodes(ctx, conf.VirtualStorages, nil, nil, nil, nil, testhelper.SharedLogger(t))
	require.NoError(t, err)
	defer nodes.Close()

	cc, _, cleanup := RunPraefectServer(t, ctx, conf, BuildOptions{
		WithConnections: nodes.Connections(),
	})
	defer cleanup()

	client := gitalypb.NewServerServiceClient(cc)

	metadata, err := client.ServerInfo(ctx, &gitalypb.ServerInfoRequest{})
	require.NoError(t, err)
	require.Len(t, metadata.GetStorageStatuses(), 0)
}

func TestDiskStatistics(t *testing.T) {
	t.Parallel()
	praefectCfg := config.Config{VirtualStorages: []*config.VirtualStorage{{Name: "praefect"}}}
	for _, name := range []string{"gitaly-1", "gitaly-2"} {
		gitalyCfg := testcfg.Build(t)

		gitalyAddr := testserver.RunGitalyServer(t, gitalyCfg, setup.RegisterAll, testserver.WithDisablePraefect())

		praefectCfg.VirtualStorages[0].Nodes = append(praefectCfg.VirtualStorages[0].Nodes, &config.Node{
			Storage: name,
			Address: gitalyAddr,
			Token:   gitalyCfg.Auth.Token,
		})
	}
	ctx := testhelper.Context(t)

	nodes, err := DialNodes(ctx, praefectCfg.VirtualStorages, nil, nil, nil, nil, testhelper.SharedLogger(t))
	require.NoError(t, err)
	defer nodes.Close()

	cc, _, cleanup := RunPraefectServer(t, ctx, praefectCfg, BuildOptions{
		WithConnections: nodes.Connections(),
	})
	defer cleanup()

	client := gitalypb.NewServerServiceClient(cc)

	diskStat, err := client.DiskStatistics(ctx, &gitalypb.DiskStatisticsRequest{})
	require.NoError(t, err)
	require.Len(t, diskStat.GetStorageStatuses(), 2)

	for _, storageStatus := range diskStat.GetStorageStatuses() {
		require.NotNil(t, storageStatus, "none of the storage statuses should be nil")
	}
}

func TestHealthCheck(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cc, _, cleanup := RunPraefectServer(t, ctx, config.Config{VirtualStorages: []*config.VirtualStorage{
		{
			Name:  "praefect",
			Nodes: []*config.Node{{Storage: "stub", Address: "unix:///stub-address", Token: ""}},
		},
	}}, BuildOptions{})
	defer cleanup()

	client := grpc_health_v1.NewHealthClient(cc)
	_, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
}

func TestRejectBadStorage(t *testing.T) {
	t.Parallel()
	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "praefect",
				Nodes: []*config.Node{
					{
						Storage: "praefect-internal-0",
						Address: "tcp://this-doesnt-matter",
					},
				},
			},
		},
	}
	ctx := testhelper.Context(t)

	cc, _, cleanup := RunPraefectServer(t, ctx, conf, BuildOptions{})
	defer cleanup()

	req := &gitalypb.OptimizeRepositoryRequest{
		Repository: &gitalypb.Repository{
			StorageName:  "bad-name",
			RelativePath: "/path/to/hashed/storage",
		},
	}

	_, err := gitalypb.NewRepositoryServiceClient(cc).OptimizeRepository(ctx, req)
	testhelper.RequireGrpcError(t, testhelper.ToInterceptedMetadata(
		structerr.New("%w", storage.NewStorageNotFoundError("bad-name")),
	), err)
}

func TestWarnDuplicateAddrs(t *testing.T) {
	t.Parallel()
	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "default",
				Nodes: []*config.Node{
					{
						Storage: "praefect-internal-0",
						Address: "tcp://abc",
					},
					{
						Storage: "praefect-internal-1",
						Address: "tcp://xyz",
					},
				},
			},
			{
				Name: "praefect",
				Nodes: []*config.Node{
					{
						Storage: "praefect-internal-0",
						Address: "tcp://abc",
					},
					{
						Storage: "praefect-internal-1",
						Address: "tcp://xyz",
					},
				},
			},
		},
	}
	ctx := testhelper.Context(t)

	tLogger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(tLogger)

	// instantiate a praefect server and trigger warning
	_, _, cleanup := RunPraefectServer(t, ctx, conf, BuildOptions{
		WithLogger:  tLogger,
		WithNodeMgr: nullNodeMgr{}, // to suppress node address issues
	})
	defer cleanup()

	for _, entry := range hook.AllEntries() {
		require.NotContains(t, entry.Message, "more than one backend node")
	}

	conf = config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "praefect",
				Nodes: []*config.Node{
					{
						Storage: "praefect-internal-0",
						Address: "tcp::/samesies",
					},
					{
						Storage: "praefect-internal-1",
						Address: "tcp::/samesies",
					},
				},
			},
		},
	}

	hook.Reset()

	// instantiate a praefect server and trigger warning
	_, _, cleanup = RunPraefectServer(t, ctx, conf, BuildOptions{
		WithLogger:  tLogger,
		WithNodeMgr: nullNodeMgr{}, // to suppress node address issues
	})
	defer cleanup()

	var found bool
	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, "more than one backend node") {
			found = true
			break
		}
	}
	require.True(t, found, "expected to find error log")

	conf = config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "default",
				Nodes: []*config.Node{
					{
						Storage: "praefect-internal-0",
						Address: "tcp://abc",
					},
					{
						Storage: "praefect-internal-1",
						Address: "tcp://xyz",
					},
				},
			},
			{
				Name: "praefect",
				Nodes: []*config.Node{
					{
						Storage: "praefect-internal-0",
						Address: "tcp://abc",
					},
					{
						Storage: "praefect-internal-2",
						Address: "tcp://xyz",
					},
				},
			},
		},
	}

	hook.Reset()

	// instantiate a praefect server and trigger warning
	_, _, cleanup = RunPraefectServer(t, ctx, conf, BuildOptions{
		WithLogger:  tLogger,
		WithNodeMgr: nullNodeMgr{}, // to suppress node address issues
	})
	defer cleanup()

	for _, entry := range hook.AllEntries() {
		require.NotContains(t, entry.Message, "more than one backend node")
	}
}

func TestRemoveRepository(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	testhelper.SkipQuarantinedTest(t, "https://gitlab.com/gitlab-org/gitaly/-/issues/4833", "TestRemoveRepository")

	gitalyCfgs := make([]gconfig.Cfg, 3)
	repos := make([]*gitalypb.Repository, 3)
	praefectCfg := config.Config{VirtualStorages: []*config.VirtualStorage{{Name: "praefect"}}}

	for i, name := range []string{"gitaly-1", "gitaly-2", "gitaly-3"} {
		gitalyCfgs[i] = testcfg.Build(t, testcfg.WithStorages(name))
		repos[i], _ = gittest.CreateRepository(t, ctx, gitalyCfgs[i], gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
			RelativePath:           t.Name(),
		})

		gitalyAddr := testserver.RunGitalyServer(t, gitalyCfgs[i], setup.RegisterAll, testserver.WithDisablePraefect())
		gitalyCfgs[i].SocketPath = gitalyAddr

		praefectCfg.VirtualStorages[0].Nodes = append(praefectCfg.VirtualStorages[0].Nodes, &config.Node{
			Storage: name,
			Address: gitalyAddr,
			Token:   gitalyCfgs[i].Auth.Token,
		})
	}

	verifyReposExistence := func(t *testing.T, code codes.Code) {
		for i, gitalyCfg := range gitalyCfgs {
			locator := gconfig.NewLocator(gitalyCfg)
			_, err := locator.GetRepoPath(repos[i])
			st, ok := status.FromError(err)
			require.True(t, ok)
			require.Equal(t, code, st.Code())
		}
	}

	verifyReposExistence(t, codes.OK)

	logger := testhelper.SharedLogger(t)
	queueInterceptor := datastore.NewReplicationEventQueueInterceptor(datastore.NewPostgresReplicationEventQueue(testdb.New(t)))
	repoStore := defaultRepoStore(praefectCfg)
	txMgr := defaultTxMgr(praefectCfg, logger)
	nodeMgr, err := nodes.NewManager(testhelper.SharedLogger(t), praefectCfg, nil,
		repoStore, promtest.NewMockHistogramVec(), protoregistry.GitalyProtoPreregistered,
		nil, backchannel.NewClientHandshaker(
			logger,
			NewBackchannelServerFactory(logger, transaction.NewServer(txMgr), nil),
			backchannel.DefaultConfiguration(),
		), nil,
	)
	require.NoError(t, err)
	nodeMgr.Start(0, time.Hour)
	defer nodeMgr.Stop()

	cc, _, cleanup := RunPraefectServer(t, ctx, praefectCfg, BuildOptions{
		WithQueue: queueInterceptor,
		WithRepoStore: datastore.MockRepositoryStore{
			GetConsistentStoragesFunc: func(ctx context.Context, virtualStorage, relativePath string) (string, *datastructure.Set[string], error) {
				return relativePath, datastructure.NewSet[string](), nil
			},
		},
		WithNodeMgr: nodeMgr,
		WithTxMgr:   txMgr,
	})
	defer cleanup()

	virtualRepo := proto.Clone(repos[0]).(*gitalypb.Repository)
	virtualRepo.StorageName = praefectCfg.VirtualStorages[0].Name

	_, err = gitalypb.NewRepositoryServiceClient(cc).RemoveRepository(ctx, &gitalypb.RemoveRepositoryRequest{
		Repository: virtualRepo,
	})
	require.NoError(t, err)

	require.NoError(t, queueInterceptor.Wait(time.Minute, func(i *datastore.ReplicationEventQueueInterceptor) bool {
		var compl int
		for _, ack := range i.GetAcknowledge() {
			if ack.State == datastore.JobStateCompleted {
				compl++
			}
		}
		return compl == 2
	}))

	verifyReposExistence(t, codes.NotFound)
}

type mockSmartHTTP struct {
	gitalypb.UnimplementedSmartHTTPServiceServer
	txMgr         *transactions.Manager
	m             sync.Mutex
	methodsCalled map[string]int
}

func (m *mockSmartHTTP) InfoRefsUploadPack(req *gitalypb.InfoRefsRequest, stream gitalypb.SmartHTTPService_InfoRefsUploadPackServer) error {
	m.m.Lock()
	defer m.m.Unlock()
	if m.methodsCalled == nil {
		m.methodsCalled = make(map[string]int)
	}

	m.methodsCalled["InfoRefsUploadPack"]++

	return stream.Send(&gitalypb.InfoRefsResponse{})
}

func (m *mockSmartHTTP) InfoRefsReceivePack(req *gitalypb.InfoRefsRequest, stream gitalypb.SmartHTTPService_InfoRefsReceivePackServer) error {
	m.m.Lock()
	defer m.m.Unlock()
	if m.methodsCalled == nil {
		m.methodsCalled = make(map[string]int)
	}

	m.methodsCalled["InfoRefsReceivePack"]++

	return stream.Send(&gitalypb.InfoRefsResponse{})
}

func (m *mockSmartHTTP) PostReceivePack(stream gitalypb.SmartHTTPService_PostReceivePackServer) error {
	m.m.Lock()
	defer m.m.Unlock()
	if m.methodsCalled == nil {
		m.methodsCalled = make(map[string]int)
	}

	m.methodsCalled["PostReceivePack"]++

	var err error
	var req *gitalypb.PostReceivePackRequest
	for {
		req, err = stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return structerr.NewInternal("%w", err)
		}

		if err := stream.Send(&gitalypb.PostReceivePackResponse{Data: req.GetData()}); err != nil {
			return structerr.NewInternal("%w", err)
		}
	}

	ctx := stream.Context()

	tx, err := txinfo.TransactionFromContext(ctx)
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	vote := voting.VoteFromData([]byte{})
	if err := m.txMgr.VoteTransaction(ctx, tx.ID, tx.Node, vote); err != nil {
		return structerr.NewInternal("%w", err)
	}

	return nil
}

func (m *mockSmartHTTP) Called(method string) int {
	m.m.Lock()
	defer m.m.Unlock()

	return m.methodsCalled[method]
}

func newSmartHTTPGrpcServer(t *testing.T, cfg gconfig.Cfg, smartHTTPService gitalypb.SmartHTTPServiceServer) string {
	return testserver.RunGitalyServer(t, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterSmartHTTPServiceServer(srv, smartHTTPService)
	}, testserver.WithDisablePraefect())
}

func TestProxyWrites(t *testing.T) {
	t.Parallel()

	logger := testhelper.SharedLogger(t)
	txMgr := transactions.NewManager(config.Config{}, logger)

	smartHTTP0, smartHTTP1, smartHTTP2 := &mockSmartHTTP{txMgr: txMgr}, &mockSmartHTTP{txMgr: txMgr}, &mockSmartHTTP{txMgr: txMgr}

	cfg0 := testcfg.Build(t, testcfg.WithStorages("praefect-internal-0"))
	addr0 := newSmartHTTPGrpcServer(t, cfg0, smartHTTP0)

	cfg1 := testcfg.Build(t, testcfg.WithStorages("praefect-internal-1"))
	addr1 := newSmartHTTPGrpcServer(t, cfg1, smartHTTP1)

	cfg2 := testcfg.Build(t, testcfg.WithStorages("praefect-internal-2"))
	addr2 := newSmartHTTPGrpcServer(t, cfg2, smartHTTP2)

	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: testhelper.DefaultStorageName,
				Nodes: []*config.Node{
					{
						Storage: cfg0.Storages[0].Name,
						Address: addr0,
					},
					{
						Storage: cfg1.Storages[0].Name,
						Address: addr1,
					},
					{
						Storage: cfg2.Storages[0].Name,
						Address: addr2,
					},
				},
			},
		},
	}

	queue := datastore.NewPostgresReplicationEventQueue(testdb.New(t))

	nodeMgr, err := nodes.NewManager(logger, conf, nil, nil, promtest.NewMockHistogramVec(), protoregistry.GitalyProtoPreregistered, nil, nil, nil)
	require.NoError(t, err)
	nodeMgr.Start(0, time.Hour)
	defer nodeMgr.Stop()
	ctx := testhelper.Context(t)

	relativePath := gittest.NewRepositoryName(t)
	repo := &gitalypb.Repository{
		StorageName:  testhelper.DefaultStorageName,
		RelativePath: relativePath,
	}
	for _, cfg := range []gconfig.Cfg{cfg0, cfg1, cfg2} {
		_, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
			RelativePath:           relativePath,
		})
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
	}

	rs := datastore.MockRepositoryStore{
		GetConsistentStoragesFunc: func(ctx context.Context, virtualStorage, relativePath string) (string, *datastructure.Set[string], error) {
			return relativePath, datastructure.SetFromValues(cfg0.Storages[0].Name, cfg1.Storages[0].Name, cfg2.Storages[0].Name), nil
		},
	}

	coordinator := NewCoordinator(
		logger,
		queue,
		rs,
		NewNodeManagerRouter(nodeMgr, rs),
		txMgr,
		conf,
		protoregistry.GitalyProtoPreregistered,
	)

	server := grpc.NewServer(
		grpc.ForceServerCodec(proxy.NewCodec()),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(coordinator.StreamDirector)),
	)

	socket := testhelper.GetTemporaryGitalySocketFileName(t)
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)

	go testhelper.MustServe(t, server, listener)
	defer server.Stop()

	client, _ := newSmartHTTPClient(t, "unix://"+socket)

	shard, err := nodeMgr.GetShard(ctx, conf.VirtualStorages[0].Name)
	require.NoError(t, err)

	for _, storage := range conf.VirtualStorages[0].Nodes {
		node, err := shard.GetNode(storage.Storage)
		require.NoError(t, err)
		waitNodeToChangeHealthStatus(t, ctx, node, true)
	}

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	payload := "some pack data"
	for i := 0; i < 10; i++ {
		require.NoError(t, stream.Send(&gitalypb.PostReceivePackRequest{
			Repository: repo,
			Data:       []byte(payload),
		}))
	}

	require.NoError(t, stream.CloseSend())

	var receivedData bytes.Buffer
	for {
		resp, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			require.FailNowf(t, "unexpected non io.EOF error: %v", err.Error())
		}

		_, err = receivedData.Write(resp.GetData())
		require.NoError(t, err)
	}

	assert.Equal(t, 1, smartHTTP0.Called("PostReceivePack"))
	assert.Equal(t, 1, smartHTTP1.Called("PostReceivePack"))
	assert.Equal(t, 1, smartHTTP2.Called("PostReceivePack"))
	assert.Equal(t, bytes.Repeat([]byte(payload), 10), receivedData.Bytes())
}

func TestErrorThreshold(t *testing.T) {
	t.Parallel()
	backendToken := ""
	backend, cleanup := newMockDownstream(t, backendToken, func(srv *grpc.Server) {
		service := &mockRepositoryService{
			ReplicateRepositoryFunc: func(ctx context.Context, req *gitalypb.ReplicateRepositoryRequest) (*gitalypb.ReplicateRepositoryResponse, error) {
				md, ok := metadata.FromIncomingContext(ctx)
				if !ok {
					return nil, errors.New("couldn't read metadata")
				}

				if md.Get("bad-header")[0] == "true" {
					return nil, structerr.NewInternal("something went wrong")
				}

				return &gitalypb.ReplicateRepositoryResponse{}, nil
			},
			RepositoryExistsFunc: func(ctx context.Context, req *gitalypb.RepositoryExistsRequest) (*gitalypb.RepositoryExistsResponse, error) {
				md, ok := metadata.FromIncomingContext(ctx)
				if !ok {
					return nil, errors.New("couldn't read metadata")
				}

				if md.Get("bad-header")[0] == "true" {
					return nil, structerr.NewInternal("something went wrong")
				}

				return &gitalypb.RepositoryExistsResponse{}, nil
			},
		}

		gitalypb.RegisterRepositoryServiceServer(srv, service)
	})
	defer cleanup()

	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "default",
				Nodes: []*config.Node{
					{
						Storage: "praefect-internal-0",
						Address: backend,
					},
				},
			},
		},
		Failover: config.Failover{
			Enabled:          true,
			ElectionStrategy: "local",
		},
	}
	ctx := testhelper.Context(t)

	queue := datastore.NewPostgresReplicationEventQueue(testdb.New(t))
	logger := testhelper.SharedLogger(t)

	testCases := []struct {
		desc     string
		accessor bool
		error    error
	}{
		{
			desc:     "read threshold reached",
			accessor: true,
			error:    errors.New(`read error threshold reached for storage "praefect-internal-0"`),
		},
		{
			desc:  "write threshold reached",
			error: errors.New(`write error threshold reached for storage "praefect-internal-0"`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			readThreshold := uint32(5000)
			writeThreshold := uint32(5)
			if tc.accessor {
				readThreshold = 5
				writeThreshold = 5000
			}

			errorTracker, err := tracker.NewErrors(ctx, func(_, _ time.Time) bool { return true }, readThreshold, writeThreshold)
			require.NoError(t, err)

			rs := datastore.MockRepositoryStore{}
			nodeMgr, err := nodes.NewManager(logger, conf, nil, rs, promtest.NewMockHistogramVec(), protoregistry.GitalyProtoPreregistered, errorTracker, nil, nil)
			require.NoError(t, err)
			defer nodeMgr.Stop()

			coordinator := NewCoordinator(
				logger,
				queue,
				rs,
				NewNodeManagerRouter(nodeMgr, rs),
				transactions.NewManager(conf, logger),
				conf,
				protoregistry.GitalyProtoPreregistered,
			)

			server := grpc.NewServer(
				grpc.ForceServerCodec(proxy.NewCodec()),
				grpc.UnknownServiceHandler(proxy.TransparentHandler(coordinator.StreamDirector)),
			)

			socket := testhelper.GetTemporaryGitalySocketFileName(t)
			listener, err := net.Listen("unix", socket)
			require.NoError(t, err)

			go testhelper.MustServe(t, server, listener)
			defer server.Stop()

			conn, err := dial("unix://"+socket, []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())})
			require.NoError(t, err)
			defer testhelper.MustClose(t, conn)
			cli := gitalypb.NewRepositoryServiceClient(conn)

			cfg := testcfg.Build(t)

			repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})

			node := nodeMgr.Nodes()["default"][0]
			require.Equal(t, "praefect-internal-0", node.GetStorage())

			// to get the node in a healthy starting state, three consecutive successful checks
			// are needed
			for i := 0; i < 3; i++ {
				healthy, err := node.CheckHealth(ctx)
				require.NoError(t, err)
				require.True(t, healthy)
			}

			for i := 0; i < 5; i++ {
				ctx := metadata.AppendToOutgoingContext(ctx, "bad-header", "true")

				healthy, err := node.CheckHealth(ctx)
				require.NoError(t, err)
				require.True(t, healthy)

				if tc.accessor {
					_, err = cli.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
						Repository: repo,
					})
				} else {
					_, err = cli.ReplicateRepository(ctx, &gitalypb.ReplicateRepositoryRequest{
						Repository: repo,
					})
				}
				testhelper.RequireGrpcError(t, status.Error(codes.Internal, "something went wrong"), err)
			}

			healthy, err := node.CheckHealth(ctx)
			require.Equal(t, tc.error, err)
			require.False(t, healthy)
		})
	}
}

func newSmartHTTPClient(t *testing.T, serverSocketPath string) (gitalypb.SmartHTTPServiceClient, *grpc.ClientConn) {
	t.Helper()

	conn, err := grpc.Dial(serverSocketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { testhelper.MustClose(t, conn) })

	return gitalypb.NewSmartHTTPServiceClient(conn), conn
}
