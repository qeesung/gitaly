package remote_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/ref"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/remote"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/ssh"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

func TestSuccessfulFetchInternalRemote(t *testing.T) {
	cfg, gitaly0Repo, gitaly0RepoPath, cleanup := testcfg.BuildWithRepo(t)
	defer cleanup()

	testhelper.ConfigureGitalyHooksBin(t, cfg)

	gitaly0Addr, cleanup := testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterSSHServiceServer(srv, ssh.NewServer(deps.GetCfg(), deps.GetLocator(), deps.GetGitCmdFactory()))
		gitalypb.RegisterRefServiceServer(srv, ref.NewServer(deps.GetCfg(), deps.GetLocator(), deps.GetGitCmdFactory()))
		gitalypb.RegisterHookServiceServer(srv, hook.NewServer(deps.GetCfg(), deps.GetHookManager(), deps.GetGitCmdFactory()))
	})
	defer cleanup()

	remoteCfgBuilder := testcfg.NewGitalyCfgBuilder(testcfg.WithStorages("gitaly-1"))
	defer remoteCfgBuilder.Cleanup()
	remoteCfg, gitaly1Repos := remoteCfgBuilder.BuildWithRepoAt(t, "stub")
	gitaly1Repo := gitaly1Repos[0]

	testhelper.ConfigureGitalySSHBin(t, remoteCfg)

	getGitalySSHInvocationParams, cleanup := testhelper.ListenGitalySSHCalls(t, remoteCfg)
	defer cleanup()

	gitaly1Addr, cleanup := remote.RunRemoteServiceServer(t, remoteCfg)
	defer cleanup()

	gittest.CreateCommit(t, gitaly0RepoPath, "master", nil)

	gitaly1RepoPath := filepath.Join(remoteCfg.Storages[0].Path, gitaly1Repo.GetRelativePath())
	testhelper.MustRunCommand(t, nil, "git", "-C", gitaly1RepoPath, "symbolic-ref", "HEAD", "refs/heads/feature")

	client, conn := remote.NewRemoteClient(t, gitaly1Addr)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	ctx, err := helper.InjectGitalyServers(ctx, cfg.Storages[0].Name, gitaly0Addr, "")
	require.NoError(t, err)

	c, err := client.FetchInternalRemote(ctx, &gitalypb.FetchInternalRemoteRequest{
		Repository:       gitaly1Repo,
		RemoteRepository: gitaly0Repo,
	})
	require.NoError(t, err)
	require.True(t, c.GetResult())

	require.Equal(t,
		string(testhelper.MustRunCommand(t, nil, "git", "-C", gitaly0RepoPath, "show-ref", "--head")),
		string(testhelper.MustRunCommand(t, nil, "git", "-C", gitaly1RepoPath, "show-ref", "--head")),
	)

	gitalySSHInvocationParams := getGitalySSHInvocationParams()
	require.Len(t, gitalySSHInvocationParams, 1)
	require.Equal(t, []string{"upload-pack", "gitaly", "git-upload-pack", "'/internal.git'\n"}, gitalySSHInvocationParams[0].Args)
	require.Subset(t,
		gitalySSHInvocationParams[0].EnvVars,
		[]string{
			"GIT_TERMINAL_PROMPT=0",
			"GIT_SSH_VARIANT=simple",
			"LANG=en_US.UTF-8",
			"GITALY_ADDRESS=" + gitaly0Addr,
		},
	)
}

func TestFailedFetchInternalRemote(t *testing.T) {
	serverSocketPath, clean := runFullServer(t)
	defer clean()

	client, conn := remote.NewRemoteClient(t, serverSocketPath)
	defer conn.Close()

	repo, _, cleanupFn := gittest.InitBareRepo(t)
	defer cleanupFn()

	ctx, cancel := testhelper.Context()
	defer cancel()

	md := testhelper.GitalyServersMetadata(t, serverSocketPath)
	ctx = metadata.NewOutgoingContext(ctx, md)

	// Non-existing remote repo
	remoteRepo := &gitalypb.Repository{StorageName: "default", RelativePath: "fake.git"}

	request := &gitalypb.FetchInternalRemoteRequest{
		Repository:       repo,
		RemoteRepository: remoteRepo,
	}

	c, err := client.FetchInternalRemote(ctx, request)
	require.NoError(t, err, "FetchInternalRemote is not supposed to return an error when 'git fetch' fails")
	require.False(t, c.GetResult())
}

func TestFailedFetchInternalRemoteDueToValidations(t *testing.T) {
	serverSocketPath, clean := runFullServer(t)
	defer clean()

	client, conn := remote.NewRemoteClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	repo := &gitalypb.Repository{StorageName: "default", RelativePath: "repo.git"}

	testCases := []struct {
		desc    string
		request *gitalypb.FetchInternalRemoteRequest
	}{
		{
			desc:    "empty Repository",
			request: &gitalypb.FetchInternalRemoteRequest{RemoteRepository: repo},
		},
		{
			desc:    "empty Remote Repository",
			request: &gitalypb.FetchInternalRemoteRequest{Repository: repo},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.FetchInternalRemote(ctx, tc.request)

			testhelper.RequireGrpcError(t, err, codes.InvalidArgument)
			require.Contains(t, err.Error(), tc.desc)
		})
	}
}

func runFullServer(t *testing.T) (string, func()) {
	return testserver.RunGitalyServer(t, config.Config, remote.RubyServer, setup.RegisterAll)
}
