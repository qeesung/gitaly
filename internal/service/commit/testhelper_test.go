package commit

import (
	"net"
	"os"
	"testing"

	"github.com/golang/protobuf/ptypes/timestamp"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var ()

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

var rubyServer = &rubyserver.Server{}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	if err := rubyServer.Start(); err != nil {
		log.Fatal(err)
	}
	defer rubyServer.Stop()

	return m.Run()
}

func startTestServices(t testing.TB) (*grpc.Server, string) {
	server := testhelper.NewTestGrpcServer(t, nil, nil)
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()

	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal("failed to start server")
	}

	gitalypb.RegisterCommitServiceServer(server, NewServer(rubyServer))
	reflection.Register(server)

	go server.Serve(listener)
	return server, "unix://" + serverSocketPath
}

func newCommitServiceClient(t testing.TB, serviceSocketPath string) (gitalypb.CommitServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
	}
	conn, err := grpc.Dial(serviceSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewCommitServiceClient(conn), conn
}

func dummyCommitAuthor(ts int64) *gitalypb.CommitAuthor {
	return &gitalypb.CommitAuthor{
		Name:  []byte("Ahmad Sherif"),
		Email: []byte("ahmad+gitlab-test@gitlab.com"),
		Date:  &timestamp.Timestamp{Seconds: ts},
		Tz:    []byte("+0200"),
	}
}
