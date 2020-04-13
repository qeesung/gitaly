package smarthttp

import (
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/git/hooks"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

const (
	pktFlushStr = "0000"
)

func TestMain(m *testing.M) {
	testhelper.Configure()
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	hooks.Override = "/"

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	config.Config.Ruby.Dir = filepath.Join(cwd, "../../../ruby")

	testhelper.ConfigureGitalyHooksBinary()

	return m.Run()
}

func runSmartHTTPServer(t *testing.T, serverOpts ...ServerOpt) (string, func()) {
	srv := testhelper.NewServerWithAuth(t, nil, nil, config.Config.Auth.Token)

	gitalypb.RegisterSmartHTTPServiceServer(srv.GrpcServer(), NewServer(serverOpts...))
	reflection.Register(srv.GrpcServer())

	require.NoError(t, srv.Start())

	return "unix://" + srv.Socket(), srv.Stop
}

func newSmartHTTPClient(t *testing.T, serverSocketPath string) (gitalypb.SmartHTTPServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentials(config.Config.Auth.Token)),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewSmartHTTPServiceClient(conn), conn
}
