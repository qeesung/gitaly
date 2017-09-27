package smarthttp

import (
	"net"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

const (
	pktFlushStr = "0000"
)

var (
	serverSocketPath = testhelper.GetTemporaryGitalySocketFileName()
	testRepo         = testhelper.TestRepository()
)

func runSmartHTTPServer(t *testing.T) *grpc.Server {
	server := testhelper.NewTestGrpcServer(t, nil, nil)
	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	pb.RegisterSmartHTTPServiceServer(server, NewServer())
	reflection.Register(server)

	go server.Serve(listener)

	return server
}

func newSmartHTTPClient(t *testing.T) (pb.SmartHTTPServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithDialer(func(addr string, _ time.Duration) (net.Conn, error) {
			return net.Dial("unix", addr)
		}),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return pb.NewSmartHTTPServiceClient(conn), conn
}
