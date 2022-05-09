package diff

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func setupDiffService(ctx context.Context, t testing.TB, opt ...testserver.GitalyServerOpt) (config.Cfg, *gitalypb.Repository, string, gitalypb.DiffServiceClient) {
	cfg := testcfg.Build(t)

	addr := testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterDiffServiceServer(srv, NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetCatfileCache(),
		))
		gitalypb.RegisterRepositoryServiceServer(srv, repository.NewServer(
			cfg,
			deps.GetRubyServer(),
			deps.GetLocator(),
			deps.GetTxManager(),
			deps.GetGitCmdFactory(),
			deps.GetCatfileCache(),
			deps.GetConnsPool(),
			deps.GetGit2goExecutor(),
			deps.GetHousekeepingManager(),
		))
	}, opt...)
	cfg.SocketPath = addr

	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	require.NoError(t, err)
	t.Cleanup(func() { testhelper.MustClose(t, conn) })

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	return cfg, repo, repoPath, gitalypb.NewDiffServiceClient(conn)
}
