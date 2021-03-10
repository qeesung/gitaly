package smarthttp

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	hookservice "gitlab.com/gitlab-org/gitaly/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
)

const (
	pktFlushStr = "0000"
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	cleanup := testhelper.Configure()
	defer cleanup()

	return m.Run()
}

func runSmartHTTPServer(t *testing.T, cfg config.Cfg, serverOpts ...ServerOpt) (string, func()) {
	return testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterSmartHTTPServiceServer(srv, NewServer(cfg, deps.GetLocator(), deps.GetGitCmdFactory(), serverOpts...))
		gitalypb.RegisterHookServiceServer(srv, hookservice.NewServer(cfg, deps.GetHookManager(), deps.GetGitCmdFactory()))
	})
}

func newSmartHTTPClient(t *testing.T, serverSocketPath, token string) (gitalypb.SmartHTTPServiceClient, *grpc.ClientConn) {
	t.Helper()

	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(token)),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	require.NoError(t, err)

	return gitalypb.NewSmartHTTPServiceClient(conn), conn
}
