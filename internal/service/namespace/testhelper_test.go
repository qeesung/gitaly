package namespace

import (
	"net"
	"testing"
	"time"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func runNamespaceServer(t *testing.T) (*grpc.Server, string) {
	server := testhelper.NewTestGrpcServer(t, nil, nil)
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()

	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	pb.RegisterNamespaceServiceServer(server, NewServer())
	reflection.Register(server)

	go server.Serve(listener)

	return server, serverSocketPath
}

func newNamespaceClient(t *testing.T, serverSocketPath string) (pb.NamespaceServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return pb.NewNamespaceServiceClient(conn), conn
}
