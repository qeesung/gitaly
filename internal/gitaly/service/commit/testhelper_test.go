package commit

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

// setupCommitService makes a basic configuration and starts the service with the client.
func setupCommitService(
	tb testing.TB,
	ctx context.Context,
	opts ...testserver.GitalyServerOpt,
) (config.Cfg, gitalypb.CommitServiceClient) {
	cfg := testcfg.Build(tb)
	cfg.SocketPath = startTestServices(tb, cfg, opts...)

	return cfg, newCommitServiceClient(tb, cfg.SocketPath)
}

func startTestServices(tb testing.TB, cfg config.Cfg, opts ...testserver.GitalyServerOpt) string {
	tb.Helper()
	return testserver.RunGitalyServer(tb, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterCommitServiceServer(srv, NewServer(deps))
		gitalypb.RegisterRepositoryServiceServer(srv, repository.NewServer(deps))
	}, opts...)
}

func newCommitServiceClient(tb testing.TB, serviceSocketPath string) gitalypb.CommitServiceClient {
	tb.Helper()

	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	conn, err := grpc.Dial(serviceSocketPath, connOpts...)
	require.NoError(tb, err)
	tb.Cleanup(func() { conn.Close() })

	return gitalypb.NewCommitServiceClient(conn)
}

type gitCommitsGetter interface {
	GetCommits() []*gitalypb.GitCommit
}

func createCommits(tb testing.TB, cfg config.Cfg, repoPath, branch string, commitCount int, parent git.ObjectID) git.ObjectID {
	for i := 0; i < commitCount; i++ {
		var parents []git.ObjectID
		if parent != "" {
			parents = append(parents, parent)
		}

		parent = gittest.WriteCommit(tb, cfg, repoPath,
			gittest.WithBranch(branch),
			gittest.WithMessage(fmt.Sprintf("%s branch Empty commit %d", branch, i)),
			gittest.WithParents(parents...),
		)
	}

	return parent
}

func getAllCommits(tb testing.TB, getter func() (gitCommitsGetter, error)) []*gitalypb.GitCommit {
	tb.Helper()

	var commits []*gitalypb.GitCommit
	for {
		resp, err := getter()
		if err == io.EOF {
			return commits
		}
		require.NoError(tb, err)

		commits = append(commits, resp.GetCommits()...)
	}
}

func writeCommit(
	tb testing.TB,
	ctx context.Context,
	cfg config.Cfg,
	repo *localrepo.Repo,
	opts ...gittest.WriteCommitOption,
) (git.ObjectID, *gitalypb.GitCommit) {
	tb.Helper()

	repoPath, err := repo.Path()
	require.NoError(tb, err)

	commitID := gittest.WriteCommit(tb, cfg, repoPath, opts...)
	commitProto, err := repo.ReadCommit(ctx, commitID.Revision())
	require.NoError(tb, err)

	return commitID, commitProto
}

func requireErrorMsgWhenGitLogError(tb testing.TB, commitID string, entries ...*logrus.Entry) {
	tb.Helper()

	for _, entry := range entries {
		if entry.Level == logrus.ErrorLevel {
			require.Contains(tb, entry.Data, "stderr",
				fmt.Sprintf("fatal: bad object %s", commitID))
			return
		}
	}
}
