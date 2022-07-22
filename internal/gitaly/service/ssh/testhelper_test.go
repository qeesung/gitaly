//go:build !gitaly_test_sha256

package ssh

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	hookservice "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func runSSHServer(t *testing.T, cfg config.Cfg, serverOpts ...testserver.GitalyServerOpt) string {
	return runSSHServerWithOptions(t, cfg, nil, serverOpts...)
}

func runSSHServerWithOptions(t *testing.T, cfg config.Cfg, opts []ServerOpt, serverOpts ...testserver.GitalyServerOpt) string {
	return startSSHServerWithOptions(t, cfg, opts, serverOpts...).Address()
}

func startSSHServerWithOptions(t *testing.T, cfg config.Cfg, opts []ServerOpt, serverOpts ...testserver.GitalyServerOpt) testserver.GitalyServer {
	return testserver.StartGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterSSHServiceServer(srv, NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetTxManager(),
			opts...))
		gitalypb.RegisterHookServiceServer(srv,
			hookservice.NewServer(
				deps.GetHookManager(),
				deps.GetGitCmdFactory(),
				deps.GetPackObjectsCache(),
				deps.GetPackObjectsConcurrencyTracker(),
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
	}, serverOpts...)
}

func newSSHClient(t *testing.T, serverSocketPath string) (gitalypb.SSHServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewSSHServiceClient(conn), conn
}
