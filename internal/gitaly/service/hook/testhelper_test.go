package hook

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func setupHookService(t testing.TB) (config.Cfg, *gitalypb.Repository, string, gitalypb.HookServiceClient) {
	t.Helper()

	cfg, repo, repoPath := testcfg.BuildWithRepo(t)
	serverSocketPath := runHooksServer(t, cfg, nil)
	client, conn := newHooksClient(t, serverSocketPath)
	t.Cleanup(func() { conn.Close() })

	return cfg, repo, repoPath, client
}

func newHooksClient(t testing.TB, serverSocketPath string) (gitalypb.HookServiceClient, *grpc.ClientConn) {
	t.Helper()

	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
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

	return testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		hookServer := NewServer(
			gitalyhook.NewManager(deps.GetCfg(), deps.GetLocator(), deps.GetGitCmdFactory(), deps.GetTxManager(), deps.GetGitlabClient()),
			deps.GetGitCmdFactory(),
			deps.GetPackObjectsCache(),
		)
		for _, opt := range opts {
			opt(hookServer.(*server))
		}

		gitalypb.RegisterHookServiceServer(srv, hookServer)
	}, serverOpts...)
}
