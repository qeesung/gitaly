package commit

import (
	"bytes"
	"net"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/supervisor"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	"github.com/golang/protobuf/ptypes/timestamp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

var (
	testRepo *pb.Repository
)

func startTestServices(t *testing.T) (service *grpc.Server, ruby *supervisor.Process, serverSocketPath string) {
	testRepo = testhelper.TestRepository()

	testhelper.ConfigureRuby()
	ruby, err := rubyserver.Start()
	if err != nil {
		t.Fatal("ruby spawn failed")
	}

	service, serverSocketPath = runCommitServiceServer(t)
	return
}

func stopTestServices(service *grpc.Server, ruby *supervisor.Process) {
	ruby.Stop()
	service.Stop()
}

func runCommitServiceServer(t *testing.T) (server *grpc.Server, serviceSocketPath string) {
	server = grpc.NewServer()
	serviceSocketPath = testhelper.GetTemporaryGitalySocketFileName()

	listener, err := net.Listen("unix", serviceSocketPath)
	if err != nil {
		t.Fatal("failed to start server")
	}

	pb.RegisterCommitServiceServer(server, NewServer())
	reflection.Register(server)

	go server.Serve(listener)
	return
}

func newCommitClient(t *testing.T, serviceSocketPath string) pb.CommitClient {
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

	return pb.NewCommitClient(conn)
}

func newCommitServiceClient(t *testing.T, serviceSocketPath string) pb.CommitServiceClient {
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

func dummyCommitAuthor(ts int64) *pb.CommitAuthor {
	return &pb.CommitAuthor{
		Name:  []byte("Ahmad Sherif"),
		Email: []byte("ahmad+gitlab-test@gitlab.com"),
		Date:  &timestamp.Timestamp{Seconds: ts},
	}
}
