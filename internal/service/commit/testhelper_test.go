package commit

import (
	"bytes"
	"net"
	"os"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/middleware/objectdirhandler"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	log "github.com/Sirupsen/logrus"
	"github.com/golang/protobuf/ptypes/timestamp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

var (
	testRepo         = testhelper.TestRepository()
	serverSocketPath = testhelper.GetTemporaryGitalySocketFileName()
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

type testServer struct {
	*grpc.Server
	socketPath string
}

func (ts *testServer) Stop() {
	if ts == nil {
		return
	}
	ts.Server.Stop()
}

func testMain(m *testing.M) int {
	testhelper.ConfigureRuby()
	ruby, err := rubyserver.Start()
	if err != nil {
		log.Fatal(err)
	}
	defer ruby.Stop()
	return m.Run()
}

func startTestServices(t *testing.T) *testServer {
	server := testhelper.NewTestGrpcServer(
		t,
		[]grpc.StreamServerInterceptor{objectdirhandler.Stream},
		[]grpc.UnaryServerInterceptor{objectdirhandler.Unary},
	)

	//	socketPath := testhelper.GetTemporaryGitalySocketFileName()
	socketPath := serverSocketPath
	if err := os.RemoveAll(socketPath); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal("failed to start server")
	}

	pb.RegisterCommitServiceServer(server, NewServer())
	reflection.Register(server)

	go server.Serve(listener)
	return &testServer{
		Server:     server,
		socketPath: socketPath,
	}
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

func newCommitServiceClient(t *testing.T, serviceSocketPath string) (pb.CommitServiceClient, *grpc.ClientConn) {
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

	return pb.NewCommitServiceClient(conn), conn
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
