package diff

import (
	"net"
	"os"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

var (
	testRepo     *pb.Repository
	testRepoPath string
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

var rubyServer *rubyserver.Server

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	testRepo = testhelper.TestRepository()

	var err error
	testRepoPath, err = helper.GetRepoPath(testRepo)
	if err != nil {
		log.Fatal(err)
	}

	testhelper.ConfigureRuby()
	rubyServer, err = rubyserver.Start()
	if err != nil {
		log.Fatal(err)
	}
	defer rubyServer.Stop()

	return m.Run()
}

func runDiffServer(t *testing.T) (*grpc.Server, string) {
	server := testhelper.NewTestGrpcServer(t, nil, nil)

	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()
	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	pb.RegisterDiffServiceServer(server, NewServer(rubyServer))
	reflection.Register(server)

	go server.Serve(listener)

	return server, serverSocketPath
}

func newDiffClient(t *testing.T, serverSocketPath string) (pb.DiffServiceClient, *grpc.ClientConn) {
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

	return pb.NewDiffServiceClient(conn), conn
}
