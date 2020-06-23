package operations

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	gitalylog "gitlab.com/gitlab-org/gitaly/internal/log"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/service/commit"
	"gitlab.com/gitlab-org/gitaly/internal/service/hook"
	"gitlab.com/gitlab-org/gitaly/internal/service/ref"
	"gitlab.com/gitlab-org/gitaly/internal/service/repository"
	"gitlab.com/gitlab-org/gitaly/internal/service/ssh"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var (
	gitlabPreHooks  = []string{"pre-receive", "update"}
	gitlabPostHooks = []string{"post-receive"}
	GitlabPreHooks  = gitlabPreHooks
	GitlabHooks     []string
	RubyServer      = &rubyserver.Server{}
)

func init() {
	GitlabHooks = append(GitlabHooks, append(gitlabPreHooks, gitlabPostHooks...)...)
}

func TestMain(m *testing.M) {
	testhelper.Configure()
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	gitlabShellDir := filepath.Join(cwd, "testdata", "gitlab-shell")
	os.RemoveAll(gitlabShellDir)

	if err := os.MkdirAll(gitlabShellDir, 0755); err != nil {
		log.Fatal(err)
	}

	config.Config.GitlabShell.Dir = filepath.Join(cwd, "testdata", "gitlab-shell")

	testhelper.ConfigureGitalySSH()
	testhelper.ConfigureGitalyHooksBinary()

	defer func(token string) {
		config.Config.Auth.Token = token
	}(config.Config.Auth.Token)
	config.Config.Auth.Token = testhelper.RepositoryAuthToken

	cleanupSrv := setupAndStartGitlabServer(gitalylog.Default(), testhelper.TestUser.GlId, testhelper.GlRepository)
	defer cleanupSrv()

	if err := RubyServer.Start(); err != nil {
		log.Fatal(err)
	}
	defer RubyServer.Stop()

	return m.Run()
}

func runOperationServiceServer(t *testing.T) (string, func()) {
	return runOperationServiceServerWithRubyServer(t, RubyServer)
}

func runOperationServiceServerWithRubyServer(t *testing.T, ruby *rubyserver.Server) (string, func()) {
	srv := testhelper.NewServerWithAuth(t, nil, nil, config.Config.Auth.Token)

	internalSocket := config.GitalyInternalSocketPath()
	internalListener, err := net.Listen("unix", internalSocket)
	require.NoError(t, err)

	gitalypb.RegisterOperationServiceServer(srv.GrpcServer(), &server{ruby: ruby})
	gitalypb.RegisterHookServiceServer(srv.GrpcServer(), hook.NewServer(testhelper.GitlabAPIStub, config.Config.Hooks))
	gitalypb.RegisterRepositoryServiceServer(srv.GrpcServer(), repository.NewServer(ruby, config.NewLocator(config.Config), internalSocket))
	gitalypb.RegisterRefServiceServer(srv.GrpcServer(), ref.NewServer())
	gitalypb.RegisterCommitServiceServer(srv.GrpcServer(), commit.NewServer())
	gitalypb.RegisterSSHServiceServer(srv.GrpcServer(), ssh.NewServer())
	reflection.Register(srv.GrpcServer())

	require.NoError(t, srv.Start())

	go func() {
		srv.GrpcServer().Serve(internalListener)
	}()

	return "unix://" + srv.Socket(), srv.Stop
}

func newOperationClient(t *testing.T, serverSocketPath string) (gitalypb.OperationServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(config.Config.Auth.Token)),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewOperationServiceClient(conn), conn
}

func setupAndStartGitlabServer(t testhelper.FatalLogger, glID, glRepository string, gitPushOptions ...string) func() {
	url, cleanup := testhelper.SetupAndStartGitlabServer(t, &testhelper.GitlabTestServerOptions{
		SecretToken:                 "secretToken",
		GLID:                        glID,
		GLRepository:                glRepository,
		PostReceiveCounterDecreased: true,
		Protocol:                    "web",
		GitPushOptions:              gitPushOptions,
	})

	gitlabURL := config.Config.Gitlab.URL
	cleanupAll := func() {
		cleanup()
		config.Config.Gitlab.URL = gitlabURL
	}
	config.Config.Gitlab.URL = url

	return cleanupAll
}
