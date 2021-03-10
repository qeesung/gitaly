package ref

import (
	"bytes"
	"os"
	"testing"

	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	hookservice "gitlab.com/gitlab-org/gitaly/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/internal/helper/lines"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
)

var (
	localBranches = map[string]*gitalypb.GitCommit{
		"refs/heads/100%branch":      testhelper.GitLabTestCommit("1b12f15a11fc6e62177bef08f47bc7b5ce50b141"),
		"refs/heads/improve/awesome": testhelper.GitLabTestCommit("5937ac0a7beb003549fc5fd26fc247adbce4a52e"),
		"refs/heads/'test'":          testhelper.GitLabTestCommit("e56497bb5f03a90a51293fc6d516788730953899"),
	}
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	cleanup := testhelper.Configure()
	defer cleanup()

	testhelper.ConfigureGitalyHooksBinary(config.Config.BinDir)

	// Force small messages to test that fragmenting the
	// ref list works correctly
	lines.ItemsPerMessage = 3

	return m.Run()
}

func runRefServiceServer(t *testing.T) (func(), string) {
	addr, cleanup := testserver.RunGitalyServer(t, config.Config, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterRefServiceServer(srv, NewServer(deps.GetCfg(), deps.GetLocator(), deps.GetGitCmdFactory()))
		gitalypb.RegisterHookServiceServer(srv, hookservice.NewServer(deps.GetCfg(), deps.GetHookManager(), deps.GetGitCmdFactory()))
	})
	//cfg := config.Config
	//locator := config.NewLocator(cfg)
	//txManager := transaction.NewManager(cfg)
	//hookManager := hook.NewManager(locator, txManager, hook.GitlabAPIStub, cfg)
	//gitCmdFactory := git.NewExecCommandFactory(cfg)

	//srv := testhelper.NewServer(t, nil, nil, testhelper.WithInternalSocket(cfg))

	//srv.Start(t)

	//return srv.Stop, "unix://" + srv.Socket()
	return cleanup, addr
}

func newRefServiceClient(t *testing.T, serverSocketPath string) (gitalypb.RefServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

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
			return // Found the branch and it maches. Success!
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

func assertContainsBranch(t *testing.T, branches []*gitalypb.FindAllBranchesResponse_Branch, branch *gitalypb.FindAllBranchesResponse_Branch) {
	t.Helper()

	var branchNames [][]byte

	for _, b := range branches {
		if bytes.Equal(branch.Name, b.Name) {
			testhelper.ProtoEqual(t, b.Target, branch.Target)
			return // Found the branch and it maches. Success!
		}
		branchNames = append(branchNames, b.Name)
	}

	t.Errorf("Expected to find branch %q in branches %s", branch.Name, branchNames)
}
