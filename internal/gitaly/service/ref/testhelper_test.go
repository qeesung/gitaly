//go:build !gitaly_test_sha256

package ref

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	hookservice "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/lines"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var localBranches = map[string]*gitalypb.GitCommit{
	"refs/heads/100%branch":      testhelper.GitLabTestCommit("1b12f15a11fc6e62177bef08f47bc7b5ce50b141"),
	"refs/heads/improve/awesome": testhelper.GitLabTestCommit("5937ac0a7beb003549fc5fd26fc247adbce4a52e"),
	"refs/heads/'test'":          testhelper.GitLabTestCommit("e56497bb5f03a90a51293fc6d516788730953899"),
}

func TestMain(m *testing.M) {
	testhelper.Run(m, testhelper.WithSetup(func() error {
		// Force small messages to test that fragmenting the
		// ref list works correctly
		lines.ItemsPerMessage = 3
		return nil
	}))
}

func setupRefService(ctx context.Context, tb testing.TB) (config.Cfg, *gitalypb.Repository, string, gitalypb.RefServiceClient) {
	cfg, client := setupRefServiceWithoutRepo(tb)
	repo, repoPath := gittest.CreateRepository(ctx, tb, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})
	return cfg, repo, repoPath, client
}

func setupRefServiceWithoutRepo(tb testing.TB) (config.Cfg, gitalypb.RefServiceClient) {
	cfg := testcfg.Build(tb)

	testcfg.BuildGitalyHooks(tb, cfg)

	serverSocketPath := runRefServiceServer(tb, cfg)
	cfg.SocketPath = serverSocketPath

	client, conn := newRefServiceClient(tb, serverSocketPath)
	tb.Cleanup(func() { conn.Close() })

	return cfg, client
}

func runRefServiceServer(tb testing.TB, cfg config.Cfg) string {
	return testserver.RunGitalyServer(tb, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterRefServiceServer(srv, NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetTxManager(),
			deps.GetCatfileCache(),
		))
		gitalypb.RegisterHookServiceServer(srv, hookservice.NewServer(deps.GetHookManager(), deps.GetGitCmdFactory(), deps.GetPackObjectsCache(), deps.GetPackObjectsConcurrencyTracker()))
		gitalypb.RegisterRepositoryServiceServer(srv, repository.NewServer(
			deps.GetCfg(),
			deps.GetRubyServer(),
			deps.GetLocator(),
			deps.GetTxManager(),
			deps.GetGitCmdFactory(),
			deps.GetCatfileCache(),
			deps.GetConnsPool(),
			deps.GetGit2goExecutor(),
			deps.GetHousekeepingManager(),
		))
	})
}

func newRefServiceClient(tb testing.TB, serverSocketPath string) (gitalypb.RefServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	require.NoError(tb, err)

	return gitalypb.NewRefServiceClient(conn), conn
}

func assertContainsLocalBranch(t *testing.T, branches []*gitalypb.FindLocalBranchResponse, branch *gitalypb.FindLocalBranchResponse) {
	t.Helper()

	for _, b := range branches {
		if bytes.Equal(branch.Name, b.Name) {
			if !findLocalBranchResponsesEqual(branch, b) {
				t.Errorf("Expected branch\n%v\ngot\n%v", branch, b)
			}

			testhelper.ProtoEqual(t, branch.Commit, b.Commit)
			return // Found the branch and it matches. Success!
		}
	}
	t.Errorf("Expected to find branch %q in local branches", branch.Name)
}

func findLocalBranchCommitAuthorsEqual(a *gitalypb.FindLocalBranchCommitAuthor, b *gitalypb.FindLocalBranchCommitAuthor) bool {
	return bytes.Equal(a.Name, b.Name) &&
		bytes.Equal(a.Email, b.Email) &&
		a.Date.Seconds == b.Date.Seconds
}

func findLocalBranchResponsesEqual(a *gitalypb.FindLocalBranchResponse, b *gitalypb.FindLocalBranchResponse) bool {
	return a.CommitId == b.CommitId &&
		bytes.Equal(a.CommitSubject, b.CommitSubject) &&
		findLocalBranchCommitAuthorsEqual(a.CommitAuthor, b.CommitAuthor) &&
		findLocalBranchCommitAuthorsEqual(a.CommitCommitter, b.CommitCommitter)
}

func assertContainsAllBranchesResponseBranch(t *testing.T, branches []*gitalypb.FindAllBranchesResponse_Branch, branch *gitalypb.FindAllBranchesResponse_Branch) {
	t.Helper()

	var branchNames [][]byte

	for _, b := range branches {
		if bytes.Equal(branch.Name, b.Name) {
			testhelper.ProtoEqual(t, b.Target, branch.Target)
			return // Found the branch and it matches. Success!
		}
		branchNames = append(branchNames, b.Name)
	}

	t.Errorf("Expected to find branch %q in branches %s", branch.Name, branchNames)
}

func assertContainsBranch(t *testing.T, branches []*gitalypb.Branch, branch *gitalypb.Branch) {
	t.Helper()

	var branchNames [][]byte

	for _, b := range branches {
		if bytes.Equal(branch.Name, b.Name) {
			testhelper.ProtoEqual(t, b.TargetCommit, branch.TargetCommit)
			return // Found the branch and it matches. Success!
		}
		branchNames = append(branchNames, b.Name)
	}

	t.Errorf("Expected to find branch %q in branches %s", branch.Name, branchNames)
}

func gitalyOrPraefect(gitalyMsg, praefectMsg string) string {
	if testhelper.IsPraefectEnabled() {
		return praefectMsg
	}
	return gitalyMsg
}
