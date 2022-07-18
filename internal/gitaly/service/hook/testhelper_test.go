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

func setupHookService(ctx context.Context, t testing.TB) (config.Cfg, *gitalypb.Repository, string, gitalypb.HookServiceClient) {
	t.Helper()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runHooksServer(t, cfg, nil)
	client, conn := newHooksClient(t, cfg.SocketPath)
	t.Cleanup(func() { conn.Close() })

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	return cfg, repo, repoPath, client
}

func newHooksClient(t testing.TB, serverSocketPath string) (gitalypb.HookServiceClient, *grpc.ClientConn) {
	t.Helper()

	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewHookServiceClient(conn), conn
}

type serverOption func(*server)

func runHooksServer(t testing.TB, cfg config.Cfg, opts []serverOption, serverOpts ...testserver.GitalyServerOpt) string {
	t.Helper()

	serverOpts = append(serverOpts, testserver.WithDisablePraefect())

	return testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
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
