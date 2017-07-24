package ref

import (
	"net"
	"os"
	"testing"
	"time"

	log "github.com/Sirupsen/logrus"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/helper/lines"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/service/renameadapter"
)

var (
	serverSocketPath = testhelper.GetTemporaryGitalySocketFileName()
	testRepo         *pb.Repository
	testRepoPath     string
)

func TestMain(m *testing.M) {
	var err error

	testRepo = testhelper.TestRepository()
	testRepoPath, err = helper.GetRepoPath(testRepo)
	if err != nil {
		log.Fatal(err)
	}

	// Use 100 bytes as the maximum message size to test that fragmenting the
	// ref list works correctly
	lines.MaxMsgSize = 100

	os.Exit(func() int {
		return m.Run()
	}())
}

func runRefServer(t *testing.T) *grpc.Server {
	os.Remove(serverSocketPath)
	grpcServer := grpc.NewServer()
	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	pb.RegisterRefServer(grpcServer, renameadapter.NewRefAdapter(&server{}))
	reflection.Register(grpcServer)

	go grpcServer.Serve(listener)

	return grpcServer
}

func newRefClient(t *testing.T) pb.RefClient {
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

	return pb.NewRefClient(conn)
}
