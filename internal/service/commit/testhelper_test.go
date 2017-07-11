package commit

import (
	"bytes"
	"net"
	"os"
	"testing"
	"time"

	log "github.com/Sirupsen/logrus"

	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/service/renameadapter"
)

var (
	serverSocketPath  = testhelper.GetTemporaryGitalySocketFileName()
	serviceSocketPath = testhelper.GetTemporaryGitalySocketFileName()
	testRepo          *pb.Repository
)

func TestMain(m *testing.M) {
	testRepo = testhelper.TestRepository()

	testhelper.ConfigureRuby()
	ruby, err := rubyserver.Start()
	if err != nil {
		log.WithError(err).Fatal("ruby spawn failed")
	}

	os.Exit(func() int {
		defer ruby.Stop()

		os.Remove(serverSocketPath)
		server := runCommitServer(m)
		defer func() {
			server.Stop()
			os.Remove(serverSocketPath)
		}()

		os.Remove(serviceSocketPath)
		service := runCommitServiceServer(m)
		defer func() {
			service.Stop()
			os.Remove(serviceSocketPath)
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

func runCommitServiceServer(m *testing.M) *grpc.Server {
	server := grpc.NewServer()
	listener, err := net.Listen("unix", serviceSocketPath)
	if err != nil {
		log.WithError(err).Fatal("failed to start server")
	}

	pb.RegisterCommitServiceServer(server, NewServer())
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

func newCommitServiceClient(t *testing.T) pb.CommitServiceClient {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithDialer(func(addr string, _ time.Duration) (net.Conn, error) {
			return net.Dial("unix", addr)
		}),
	}
	conn, err := grpc.Dial(serviceSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return pb.NewCommitServiceClient(conn)
}

func treeEntriesEqual(a, b *pb.TreeEntry) bool {
	return a.CommitOid == b.CommitOid && a.Oid == b.Oid && a.Mode == b.Mode &&
		bytes.Equal(a.Path, b.Path) && a.RootOid == b.RootOid && a.Type == b.Type
}
