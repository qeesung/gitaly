package internalgitaly

import (
	"net"
	"os"
	"testing"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func TestMain(m *testing.M) {
	testhelper.Configure()
	os.Exit(m.Run())
}

func runInternalGitalyServer(t *testing.T) (*grpc.Server, string) {
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()
	grpcServer := testhelper.NewTestGrpcServer(t, nil, nil)

	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	gitalypb.RegisterInternalGitalyServer(grpcServer, NewServer(config.Config.Storages))
	reflection.Register(grpcServer)

	go grpcServer.Serve(listener)

	return grpcServer, "unix://" + serverSocketPath
}

func newInternalGitalyClient(t *testing.T, serverSocketPath string) (gitalypb.InternalGitalyClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewInternalGitalyClient(conn), conn
}
