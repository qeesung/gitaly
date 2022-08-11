//go:build !gitaly_test_sha256

package hook

import (
	"context"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func setupHookService(ctx context.Context, tb testing.TB) (config.Cfg, *gitalypb.Repository, string, gitalypb.HookServiceClient) {
	tb.Helper()

	cfg := testcfg.Build(tb)
	cfg.SocketPath = runHooksServer(tb, cfg, nil)
	client, conn := newHooksClient(tb, cfg.SocketPath)
	tb.Cleanup(func() { conn.Close() })

	repo, repoPath := gittest.CreateRepository(ctx, tb, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	return cfg, repo, repoPath, client
}

func newHooksClient(tb testing.TB, serverSocketPath string) (gitalypb.HookServiceClient, *grpc.ClientConn) {
	tb.Helper()

	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		tb.Fatal(err)
	}

	return gitalypb.NewHookServiceClient(conn), conn
}

type serverOption func(*server)

func runHooksServer(tb testing.TB, cfg config.Cfg, opts []serverOption, serverOpts ...testserver.GitalyServerOpt) string {
	tb.Helper()

	serverOpts = append(serverOpts, testserver.WithDisablePraefect())

	return testserver.RunGitalyServer(tb, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		hookServer := NewServer(
			gitalyhook.NewManager(deps.GetCfg(), deps.GetLocator(), deps.GetGitCmdFactory(), deps.GetTxManager(), deps.GetGitlabClient()),
			deps.GetGitCmdFactory(),
			deps.GetPackObjectsCache(),
			deps.GetPackObjectsConcurrencyTracker(),
		)
		for _, opt := range opts {
			opt(hookServer.(*server))
		}

		gitalypb.RegisterHookServiceServer(srv, hookServer)
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
	}, serverOpts...)
}
