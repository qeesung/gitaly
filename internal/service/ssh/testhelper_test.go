package ssh

import (
	"net"
	"os"
	"path"
	"testing"

	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/git/hooks"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

const (
	testPath     = "testdata/scratch"
	testRepoRoot = testPath + "/data"
)

var (
	testRepo      *gitalypb.Repository
	gitalySSHPath string
	cwd           string
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	hooks.Override = "/"
	cwd = mustGetCwd()

	err := os.RemoveAll(testPath)
	if err != nil {
		log.Fatal(err)
	}

	testRepo = testhelper.TestRepository()

	testhelper.ConfigureGitalySSH()
	gitalySSHPath = path.Join(config.Config.BinDir, "gitaly-ssh")

	return m.Run()
}

func mustGetCwd() string {
	wd, err := os.Getwd()
	if err != nil {
		log.Panic(err)
	}
	return wd
}

func runSSHServer(t *testing.T, serverOpts ...ServerOpt) (*grpc.Server, string) {
	server := testhelper.NewTestGrpcServer(t, nil, nil)

	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()
	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	gitalypb.RegisterSSHServiceServer(server, NewServer(serverOpts...))
	reflection.Register(server)

	go server.Serve(listener)

	return server, "unix://" + serverSocketPath
}

func newSSHClient(t *testing.T, serverSocketPath string) (gitalypb.SSHServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewSSHServiceClient(conn), conn
}
