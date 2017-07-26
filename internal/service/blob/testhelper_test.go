package blob

import (
	"net"
	"os"
	"testing"
	"time"

	log "github.com/Sirupsen/logrus"

	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

var (
	serverSocketPath = testhelper.GetTemporaryGitalySocketFileName()
	testRepo         *pb.Repository
)

func TestMain(m *testing.M) {
	testRepo = testhelper.TestRepository()

	server := runBlobServer(m)
	os.Exit(func() int {
		defer func() {
			server.Stop()
			os.Remove(serverSocketPath)
		}()

		return m.Run()
	}())
}

func runBlobServer(m *testing.M) *grpc.Server {
	server := grpc.NewServer()
	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		log.WithError(err).Fatal("failed to start server")
	}

	pb.RegisterBlobServiceServer(server, NewServer())
	reflection.Register(server)

	go server.Serve(listener)

	return server
}

func newBlobClient(t *testing.T) pb.BlobServiceClient {
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

	return pb.NewBlobServiceClient(conn)
}
