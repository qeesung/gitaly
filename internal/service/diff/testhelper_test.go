package diff

import (
	"net"
	"os"
	"testing"
	"time"

	log "github.com/Sirupsen/logrus"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

var (
	serverSocketPath = testhelper.GetTemporaryGitalySocketFileName()
	testRepo         *pb.Repository
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	testRepo = testhelper.TestRepository()

	testhelper.ConfigureRuby()
	ruby, err := rubyserver.Start()
	if err != nil {
		log.Fatal(err)
	}
	defer ruby.Stop()

	return m.Run()
}

func runDiffServer(t *testing.T) *grpc.Server {
	server := testhelper.NewTestGrpcServer(t, nil, nil)
	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	pb.RegisterDiffServiceServer(server, NewServer())
	reflection.Register(server)

	go server.Serve(listener)

	return server
}

func newDiffClient(t *testing.T) (pb.DiffServiceClient, *grpc.ClientConn) {
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

	return pb.NewDiffServiceClient(conn), conn
}
