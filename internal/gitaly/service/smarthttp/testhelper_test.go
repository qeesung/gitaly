package smarthttp

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/internal/backchannel"
	diskcache "gitlab.com/gitlab-org/gitaly/internal/cache"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/hook"
	hookservice "gitlab.com/gitlab-org/gitaly/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/internal/middleware/cache"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
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
	locator := config.NewLocator(cfg)
	keyer := diskcache.NewLeaseKeyer(locator)
	txManager := transaction.NewManager(cfg, backchannel.NewRegistry())
	hookManager := hook.NewManager(locator, txManager, hook.GitlabAPIStub, cfg)
	gitCmdFactory := git.NewExecCommandFactory(cfg)

	srv := testhelper.NewServer(t,
		[]grpc.StreamServerInterceptor{
			cache.StreamInvalidator(keyer, protoregistry.GitalyProtoPreregistered),
		},
		[]grpc.UnaryServerInterceptor{
			cache.UnaryInvalidator(keyer, protoregistry.GitalyProtoPreregistered),
		},
		testhelper.WithInternalSocket(cfg))

	gitalypb.RegisterSmartHTTPServiceServer(srv.GrpcServer(), NewServer(cfg, locator, gitCmdFactory, serverOpts...))
	gitalypb.RegisterHookServiceServer(srv.GrpcServer(), hookservice.NewServer(cfg, hookManager, gitCmdFactory))
	srv.Start(t)

	return "unix://" + srv.Socket(), srv.Stop
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
