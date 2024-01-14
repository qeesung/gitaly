package objectpool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	hookservice "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/ssh"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

// clientWithConn allows for passing through the ClientConn to tests which need
// to access other services than ObjectPoolService.
type clientWithConn struct {
	gitalypb.ObjectPoolServiceClient
	conn *grpc.ClientConn
}

// extractConn returns the underlying ClientConn from the client.
func extractConn(client gitalypb.ObjectPoolServiceClient) *grpc.ClientConn {
	return client.(clientWithConn).conn
}

func setup(t *testing.T, ctx context.Context, opts ...testserver.GitalyServerOpt) (config.Cfg, *gitalypb.Repository, string, storage.Locator, gitalypb.ObjectPoolServiceClient) {
	return setupWithConfig(t, ctx, testcfg.Build(t), opts...)
}

func setupWithConfig(t *testing.T, ctx context.Context, cfg config.Cfg, opts ...testserver.GitalyServerOpt) (config.Cfg, *gitalypb.Repository, string, storage.Locator, gitalypb.ObjectPoolServiceClient) {
	t.Helper()

	testcfg.BuildGitalyHooks(t, cfg)

	locator := config.NewLocator(cfg)
	cfg.SocketPath = runObjectPoolServer(t, cfg, locator, testhelper.SharedLogger(t), opts...)

	conn, err := grpc.Dial(cfg.SocketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { testhelper.MustClose(t, conn) })

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	return cfg, repo, repoPath, locator, clientWithConn{ObjectPoolServiceClient: gitalypb.NewObjectPoolServiceClient(conn), conn: conn}
}

func runObjectPoolServer(t *testing.T, cfg config.Cfg, locator storage.Locator, logger log.Logger, opts ...testserver.GitalyServerOpt) string {
	return testserver.RunGitalyServer(t, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterObjectPoolServiceServer(srv, NewServer(deps))
		gitalypb.RegisterHookServiceServer(srv, hookservice.NewServer(deps))
		gitalypb.RegisterRepositoryServiceServer(srv, repository.NewServer(deps))
		gitalypb.RegisterSSHServiceServer(srv, ssh.NewServer(deps))
	}, append(opts, testserver.WithLocator(locator), testserver.WithLogger(logger))...)
}

// createObjectPool creates a new object pool from the given source repository. It returns the
// Protobuf representation used for gRPC calls and the rewritten ObjectPool used for direct access.
func createObjectPool(
	tb testing.TB,
	ctx context.Context,
	cfg config.Cfg,
	source *gitalypb.Repository,
) (*gitalypb.ObjectPool, *objectpool.ObjectPool, string) {
	tb.Helper()

	poolProto, poolProtoPath := gittest.CreateObjectPool(tb, ctx, cfg, source)

	logger := testhelper.SharedLogger(tb)
	txManager := transaction.NewManager(cfg, logger, nil)
	catfileCache := catfile.NewCache(cfg)
	tb.Cleanup(catfileCache.Stop)

	pool, err := objectpool.FromProto(
		logger,
		config.NewLocator(cfg),
		gittest.NewCommandFactory(tb, cfg),
		catfileCache,
		txManager,
		housekeeping.NewManager(cfg.Prometheus, logger, txManager),
		&gitalypb.ObjectPool{
			Repository: &gitalypb.Repository{
				StorageName:  cfg.Storages[0].Name,
				RelativePath: gittest.GetReplicaPath(tb, ctx, cfg, poolProto.GetRepository()),
			},
		},
	)
	require.NoError(tb, err)

	return poolProto, pool, poolProtoPath
}
