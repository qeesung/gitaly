//go:build !gitaly_test_sha256

package smarthttp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v15/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/client"
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

const (
	pktFlushStr = "0000"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func startSmartHTTPServerWithOptions(t *testing.T, cfg config.Cfg, opts []ServerOpt, serverOpts []testserver.GitalyServerOpt) testserver.GitalyServer {
	return testserver.StartGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterSmartHTTPServiceServer(srv, NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetTxManager(),
			deps.GetDiskCache(),
			opts...,
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
		gitalypb.RegisterHookServiceServer(srv, hookservice.NewServer(deps.GetHookManager(), deps.GetGitCmdFactory(), deps.GetPackObjectsCache(), deps.GetPackObjectsConcurrencyTracker()))
	}, serverOpts...)
}

func startSmartHTTPServer(t *testing.T, cfg config.Cfg, opts ...ServerOpt) testserver.GitalyServer {
	return startSmartHTTPServerWithOptions(t, cfg, opts, nil)
}

func runSmartHTTPServer(t *testing.T, cfg config.Cfg, opts ...ServerOpt) string {
	gitalyServer := startSmartHTTPServer(t, cfg, opts...)
	return gitalyServer.Address()
}

func newSmartHTTPClient(t *testing.T, serverSocketPath, token string) (gitalypb.SmartHTTPServiceClient, *grpc.ClientConn) {
	t.Helper()

	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(token)),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	require.NoError(t, err)

	return gitalypb.NewSmartHTTPServiceClient(conn), conn
}

func newMuxedSmartHTTPClient(t *testing.T, ctx context.Context, serverSocketPath, token string, serverFactory backchannel.ServerFactory) gitalypb.SmartHTTPServiceClient {
	t.Helper()

	conn, err := client.Dial(
		ctx,
		serverSocketPath,
		[]grpc.DialOption{grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(token))},
		backchannel.NewClientHandshaker(testhelper.NewDiscardingLogEntry(t), serverFactory),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return gitalypb.NewSmartHTTPServiceClient(conn)
}
