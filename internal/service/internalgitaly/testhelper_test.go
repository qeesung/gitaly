package internalgitaly

import (
	"net"
	"testing"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/proto"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func runInternalGitalyServer(t *testing.T) (*grpc.Server, string) {
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()
	grpcServer := testhelper.NewTestGrpcServer(t, nil, nil)

	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	proto.RegisterInternalGitalyServer(grpcServer, NewServer(config.Config.Storages))
	reflection.Register(grpcServer)

	go grpcServer.Serve(listener)

	return grpcServer, "unix://" + serverSocketPath
}

func newInternalGitalyClient(t *testing.T, serverSocketPath string) (proto.InternalGitalyClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return proto.NewInternalGitalyClient(conn), conn
}
