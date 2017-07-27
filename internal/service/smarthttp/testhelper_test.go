package smarthttp

import (
	"net"
	"os"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"gitlab.com/gitlab-org/gitaly/internal/service/renameadapter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

const (
	testRepoRoot = "testdata/data"
	pktFlushStr  = "0000"
)

var (
	serverSocketPath = testhelper.GetTemporaryGitalySocketFileName()
	testRepo         *pb.Repository
)

func TestMain(m *testing.M) {
	testRepo = testhelper.TestRepository()

	os.Exit(func() int {
		return m.Run()
	}())
}

func runSmartHTTPServer(t *testing.T) *grpc.Server {
	server := grpc.NewServer()
	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	pb.RegisterSmartHTTPServer(server, renameadapter.NewSmartHTTPAdapter(NewServer()))
	reflection.Register(server)

	go server.Serve(listener)

	return server
}

func newSmartHTTPClient(t *testing.T) pb.SmartHTTPClient {
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

	return pb.NewSmartHTTPClient(conn)
}

func newRefServiceClient(t *testing.T) pb.RefServiceClient {
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

	return pb.NewRefServiceClient(conn)
}
