package blob

import (
	"net"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

var (
	testRepo = testhelper.TestRepository()
)

func runBlobServer(t *testing.T) (*grpc.Server, string) {
	server := testhelper.NewTestGrpcServer(t, nil, nil)

	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()
	listener, err := net.Listen("unix", serverSocketPath)

	if err != nil {
		t.Fatal(err)
	}

	pb.RegisterBlobServiceServer(server, NewServer())
	reflection.Register(server)

	go server.Serve(listener)

	return server, serverSocketPath
}

func newBlobClient(t *testing.T, serverSocketPath string) (pb.BlobServiceClient, *grpc.ClientConn) {
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

	return pb.NewBlobServiceClient(conn), conn
}
