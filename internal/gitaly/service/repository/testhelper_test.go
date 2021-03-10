package repository

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	hookservice "gitlab.com/gitlab-org/gitaly/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/internal/storage"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
)

// Stamp taken from https://golang.org/pkg/time/#pkg-constants
const testTimeString = "200601021504.05"

var (
	testTime   = time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)
	RubyServer *rubyserver.Server
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	cleanup := testhelper.Configure()
	defer cleanup()

	config.Config.Auth.Token = testhelper.RepositoryAuthToken

	var err error
	config.Config.GitlabShell.Dir, err = filepath.Abs("testdata/gitlab-shell")
	if err != nil {
		log.Error(err)
		return 1
	}

	testhelper.ConfigureGitalyHooksBinary(config.Config.BinDir)
	testhelper.ConfigureGitalySSH(config.Config.BinDir)

	RubyServer = rubyserver.New(config.Config)
	if err := RubyServer.Start(); err != nil {
		log.Error(err)
		return 1
	}
	defer RubyServer.Stop()

	return m.Run()
}

func newRepositoryClient(t *testing.T, serverSocketPath string) (gitalypb.RepositoryServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(config.Config.Auth.Token)),
	}
	conn, err := client.Dial(serverSocketPath, connOpts)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewRepositoryServiceClient(conn), conn
}

var NewRepositoryClient = newRepositoryClient
var RunRepoServer = runRepoServer

func newSecureRepoClient(t *testing.T, serverSocketPath string, pool *x509.CertPool) (gitalypb.RepositoryServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		})),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(config.Config.Auth.Token)),
	}

	conn, err := client.Dial(serverSocketPath, connOpts)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewRepositoryServiceClient(conn), conn
}

var NewSecureRepoClient = newSecureRepoClient

func runRepoServerWithConfig(t *testing.T, cfg config.Cfg, locator storage.Locator) (string, func()) {
	return testserver.RunGitalyServer(t, cfg, RubyServer, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterRepositoryServiceServer(srv, NewServer(deps.GetCfg(), deps.GetRubyServer(), deps.GetLocator(), deps.GetTxManager(), deps.GetGitCmdFactory()))
		gitalypb.RegisterHookServiceServer(srv, hookservice.NewServer(deps.GetCfg(), deps.GetHookManager(), deps.GetGitCmdFactory()))
	}, testserver.WithLocator(locator), testserver.WithLogger(testhelper.NewTestLogger(t)))
}

func runRepoServer(t *testing.T, locator storage.Locator) (string, func()) {
	return runRepoServerWithConfig(t, config.Config, locator)
}

func TestRepoNoAuth(t *testing.T) {
	locator := config.NewLocator(config.Config)
	socket, stop := runRepoServer(t, locator)
	defer stop()

	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
	}

	conn, err := grpc.Dial(socket, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := testhelper.Context()
	defer cancel()

	client := gitalypb.NewRepositoryServiceClient(conn)
	_, err = client.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{Repository: &gitalypb.Repository{StorageName: "default", RelativePath: "new/project/path"}})

	testhelper.RequireGrpcError(t, err, codes.Unauthenticated)
}

func assertModTimeAfter(t *testing.T, afterTime time.Time, paths ...string) bool {
	// NOTE: Since some filesystems don't have sub-second precision on `mtime`
	//       we're rounding the times to seconds
	afterTime = afterTime.Round(time.Second)
	for _, path := range paths {
		s, err := os.Stat(path)
		assert.NoError(t, err)

		if !s.ModTime().Round(time.Second).After(afterTime) {
			t.Errorf("ModTime is not after afterTime: %q < %q", s.ModTime().Round(time.Second).String(), afterTime.String())
		}
	}
	return t.Failed()
}
