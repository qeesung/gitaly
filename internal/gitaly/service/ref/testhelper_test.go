package ref

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	hookservice "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/lines"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestMain(m *testing.M) {
	testhelper.Run(m, testhelper.WithSetup(func() error {
		// Force small messages to test that fragmenting the
		// ref list works correctly
		lines.ItemsPerMessage = 3
		return nil
	}))
}

func setupRefService(tb testing.TB) (config.Cfg, gitalypb.RefServiceClient) {
	cfg := testcfg.Build(tb)

	testcfg.BuildGitalyHooks(tb, cfg)

	serverSocketPath := runRefServiceServer(tb, cfg)
	cfg.SocketPath = serverSocketPath

	client, conn := newRefServiceClient(tb, serverSocketPath)
	tb.Cleanup(func() { conn.Close() })

	return cfg, client
}

func runRefServiceServer(tb testing.TB, cfg config.Cfg) string {
	return testserver.RunGitalyServer(tb, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterRefServiceServer(srv, NewServer(deps))
		gitalypb.RegisterHookServiceServer(srv, hookservice.NewServer(deps))
		gitalypb.RegisterRepositoryServiceServer(srv, repository.NewServer(deps))
	})
}

func newRefServiceClient(tb testing.TB, serverSocketPath string) (gitalypb.RefServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	require.NoError(tb, err)

	return gitalypb.NewRefServiceClient(conn), conn
}

func writeCommit(
	tb testing.TB,
	ctx context.Context,
	cfg config.Cfg,
	repoProto *gitalypb.Repository,
	opts ...gittest.WriteCommitOption,
) (git.ObjectID, *gitalypb.GitCommit) {
	tb.Helper()

	repo := localrepo.NewTestRepo(tb, cfg, repoProto)
	repoPath, err := repo.Path(ctx)
	require.NoError(tb, err)

	commitID := gittest.WriteCommit(tb, cfg, repoPath, opts...)
	commitProto, err := repo.ReadCommit(ctx, commitID.Revision())
	require.NoError(tb, err)

	return commitID, commitProto
}
