//go:build !gitaly_test_sha256

package operations

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v15/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	internalclient "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/commit"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/ref"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/ssh"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	gitlabPreHooks  = []string{"pre-receive", "update"}
	gitlabPostHooks = []string{"post-receive"}
	GitlabPreHooks  = gitlabPreHooks
	GitlabHooks     []string
)

func TestMain(m *testing.M) {
	GitlabHooks = append(GitlabHooks, append(gitlabPreHooks, gitlabPostHooks...)...)
	testhelper.Run(m)
}

func setupOperationsService(tb testing.TB, ctx context.Context, options ...testserver.GitalyServerOpt) (context.Context, config.Cfg, *gitalypb.Repository, string, gitalypb.OperationServiceClient) {
	cfg := testcfg.Build(tb)
	ctx, cfg, repo, repoPath, client := setupOperationsServiceWithCfg(tb, ctx, cfg, options...)
	return ctx, cfg, repo, repoPath, client
}

func setupOperationsServiceWithCfg(
	tb testing.TB, ctx context.Context, cfg config.Cfg, options ...testserver.GitalyServerOpt,
) (context.Context, config.Cfg, *gitalypb.Repository, string, gitalypb.OperationServiceClient) {
	testcfg.BuildGitalySSH(tb, cfg)
	testcfg.BuildGitalyGit2Go(tb, cfg)
	testcfg.BuildGitalyHooks(tb, cfg)

	serverSocketPath := runOperationServiceServer(tb, cfg, options...)
	cfg.SocketPath = serverSocketPath

	client, conn := newOperationClient(tb, serverSocketPath)
	tb.Cleanup(func() { conn.Close() })

	md := testcfg.GitalyServersMetadataFromCfg(tb, cfg)
	ctx = testhelper.MergeOutgoingMetadata(ctx, md)

	repo, repoPath := gittest.CreateRepository(ctx, tb, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	return ctx, cfg, repo, repoPath, client
}

func setupOperationsServiceWithoutRepo(
	tb testing.TB, ctx context.Context, options ...testserver.GitalyServerOpt,
) (context.Context, config.Cfg, gitalypb.OperationServiceClient) {
	cfg := testcfg.Build(tb)

	testcfg.BuildGitalySSH(tb, cfg)
	testcfg.BuildGitalyGit2Go(tb, cfg)
	testcfg.BuildGitalyHooks(tb, cfg)

	serverSocketPath := runOperationServiceServer(tb, cfg, options...)
	cfg.SocketPath = serverSocketPath

	client, conn := newOperationClient(tb, serverSocketPath)
	tb.Cleanup(func() { conn.Close() })

	md := testcfg.GitalyServersMetadataFromCfg(tb, cfg)
	ctx = testhelper.MergeOutgoingMetadata(ctx, md)

	return ctx, cfg, client
}

func runOperationServiceServer(tb testing.TB, cfg config.Cfg, options ...testserver.GitalyServerOpt) string {
	tb.Helper()

	return testserver.RunGitalyServer(tb, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		operationServer := NewServer(
			deps.GetHookManager(),
			deps.GetTxManager(),
			deps.GetLocator(),
			deps.GetConnsPool(),
			deps.GetGit2goExecutor(),
			deps.GetGitCmdFactory(),
			deps.GetCatfileCache(),
			deps.GetUpdaterWithHooks(),
		)

		gitalypb.RegisterOperationServiceServer(srv, operationServer)
		gitalypb.RegisterHookServiceServer(srv, hook.NewServer(deps.GetHookManager(), deps.GetGitCmdFactory(), deps.GetPackObjectsCache(), deps.GetPackObjectsConcurrencyTracker()))
		gitalypb.RegisterRepositoryServiceServer(srv, repository.NewServer(
			deps.GetCfg(),
			nil,
			deps.GetLocator(),
			deps.GetTxManager(),
			deps.GetGitCmdFactory(),
			deps.GetCatfileCache(),
			deps.GetConnsPool(),
			deps.GetGit2goExecutor(),
			deps.GetHousekeepingManager(),
		))
		gitalypb.RegisterRefServiceServer(srv, ref.NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetTxManager(),
			deps.GetCatfileCache(),
		))
		gitalypb.RegisterCommitServiceServer(srv, commit.NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			nil,
			deps.GetCatfileCache(),
		))
		gitalypb.RegisterSSHServiceServer(srv, ssh.NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetTxManager(),
		))
	}, options...)
}

func newOperationClient(tb testing.TB, serverSocketPath string) (gitalypb.OperationServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		tb.Fatal(err)
	}

	return gitalypb.NewOperationServiceClient(conn), conn
}

func newMuxedOperationClient(t *testing.T, ctx context.Context, serverSocketPath, authToken string, handshaker internalclient.Handshaker) gitalypb.OperationServiceClient {
	conn, err := internalclient.Dial(ctx, serverSocketPath, []grpc.DialOption{grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(authToken))}, handshaker)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return gitalypb.NewOperationServiceClient(conn)
}

func setupAndStartGitlabServer(tb testing.TB, glID, glRepository string, cfg config.Cfg, gitPushOptions ...string) string {
	url, cleanup := gitlab.SetupAndStartGitlabServer(tb, cfg.GitlabShell.Dir, &gitlab.TestServerOptions{
		SecretToken:                 "secretToken",
		GLID:                        glID,
		GLRepository:                glRepository,
		PostReceiveCounterDecreased: true,
		Protocol:                    "web",
		GitPushOptions:              gitPushOptions,
	})

	tb.Cleanup(cleanup)

	return url
}
