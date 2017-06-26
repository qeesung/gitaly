package commit

import (
	"net"
	"os"
	"path"
	"testing"
	"time"

	log "github.com/Sirupsen/logrus"

	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/service/renameadapter"
)

const scratchDir = "testdata/scratch"

var (
	serverSocketPath = path.Join(scratchDir, "gitaly.sock")
	testRepo         *pb.Repository
)

func TestMain(m *testing.M) {
	testRepo = testhelper.TestRepository()

	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		log.WithError(err).Fatal("mkdirall failed")
	}

	os.Exit(func() int {
		os.Remove(serverSocketPath)
		server := runCommitServer(m)
		defer func() {
			server.Stop()
			os.Remove(serverSocketPath)
		}()

		return m.Run()
	}())
}

func runCommitServer(m *testing.M) *grpc.Server {
	server := grpc.NewServer()
	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		log.WithError(err).Fatal("failed to start server")
	}

	pb.RegisterCommitServer(server, renameadapter.NewCommitAdapter(NewServer()))
	reflection.Register(server)

	go server.Serve(listener)

	return server
}

func newCommitClient(t *testing.T) pb.CommitClient {
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

	return pb.NewCommitClient(conn)
}
