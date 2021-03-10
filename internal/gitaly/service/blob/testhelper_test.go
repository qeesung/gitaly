package blob

import (
	"os"
	"testing"

	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/internal/storage"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
)

var rubyServer *rubyserver.Server

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	cleanup := testhelper.Configure()
	defer cleanup()

	if err := testhelper.ConfigureRuby(&config.Config); err != nil {
		log.Error(err)
		return 1
	}

	rubyServer = rubyserver.New(config.Config)
	if err := rubyServer.Start(); err != nil {
		log.Error(err)
		return 1
	}
	defer rubyServer.Stop()

	return m.Run()
}

func runBlobServer(t *testing.T, locator storage.Locator) (func(), string) {
	t.Helper()

	addr, cleanup := testserver.RunGitalyServer(t, config.Config, rubyServer, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterBlobServiceServer(srv, NewServer(rubyServer, deps.GetCfg(), deps.GetLocator(), deps.GetGitCmdFactory()))
	}, testserver.WithLocator(locator))
	return cleanup, addr
}

func newBlobClient(t *testing.T, serverSocketPath string) (gitalypb.BlobServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewBlobServiceClient(conn), conn
}
