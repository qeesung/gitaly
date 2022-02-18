package commit

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

// setupCommitService makes a basic configuration and starts the service with the client.
func setupCommitService(ctx context.Context, t testing.TB) (config.Cfg, gitalypb.CommitServiceClient) {
	cfg, _, _, client := setupCommitServiceCreateRepo(ctx, t, func(ctx context.Context, tb testing.TB, cfg config.Cfg) (*gitalypb.Repository, string) {
		return nil, ""
	})
	return cfg, client
}

// setupCommitServiceWithRepo makes a basic configuration, creates a test repository and starts the service with the client.
func setupCommitServiceWithRepo(
	ctx context.Context, t testing.TB, bare bool,
) (config.Cfg, *gitalypb.Repository, string, gitalypb.CommitServiceClient) {
	return setupCommitServiceCreateRepo(ctx, t, func(ctx context.Context, tb testing.TB, cfg config.Cfg) (*gitalypb.Repository, string) {
		repo, repoPath := gittest.CreateRepository(ctx, tb, cfg, gittest.CreateRepositoryConfig{
			Seed: gittest.SeedGitLabTest,
		})

		if !bare {
			gittest.AddWorktree(t, cfg, repoPath, "worktree")
			repoPath = filepath.Join(repoPath, "worktree")
			gittest.Exec(t, cfg, "-C", repoPath, "checkout", "master")
		}

		return repo, repoPath
	})
}

func setupCommitServiceCreateRepo(
	ctx context.Context,
	t testing.TB,
	createRepo func(context.Context, testing.TB, config.Cfg) (*gitalypb.Repository, string),
) (config.Cfg, *gitalypb.Repository, string, gitalypb.CommitServiceClient) {
	cfg := testcfg.Build(t)

	cfg.SocketPath = startTestServices(t, cfg)
	client := newCommitServiceClient(t, cfg.SocketPath)

	repo, repoPath := createRepo(ctx, t, cfg)

	return cfg, repo, repoPath, client
}

func startTestServices(t testing.TB, cfg config.Cfg) string {
	t.Helper()
	return testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterCommitServiceServer(srv, NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetLinguist(),
			deps.GetCatfileCache(),
		))
		gitalypb.RegisterRepositoryServiceServer(srv, repository.NewServer(
			cfg,
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

func newCommitServiceClient(t testing.TB, serviceSocketPath string) gitalypb.CommitServiceClient {
	t.Helper()

	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
	}
	conn, err := grpc.Dial(serviceSocketPath, connOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return gitalypb.NewCommitServiceClient(conn)
}

func dummyCommitAuthor(ts int64) *gitalypb.CommitAuthor {
	return &gitalypb.CommitAuthor{
		Name:     []byte("Ahmad Sherif"),
		Email:    []byte("ahmad+gitlab-test@gitlab.com"),
		Date:     &timestamppb.Timestamp{Seconds: ts},
		Timezone: []byte("+0200"),
	}
}

type gitCommitsGetter interface {
	GetCommits() []*gitalypb.GitCommit
}

func createCommits(t testing.TB, cfg config.Cfg, repoPath, branch string, commitCount int, parent git.ObjectID) git.ObjectID {
	for i := 0; i < commitCount; i++ {
		var parents []git.ObjectID
		if parent != "" {
			parents = append(parents, parent)
		}

		parent = gittest.WriteCommit(t, cfg, repoPath,
			gittest.WithBranch(branch),
			gittest.WithMessage(fmt.Sprintf("%s branch Empty commit %d", branch, i)),
			gittest.WithParents(parents...),
		)
	}

	return parent
}

func getAllCommits(t testing.TB, getter func() (gitCommitsGetter, error)) []*gitalypb.GitCommit {
	t.Helper()

	var commits []*gitalypb.GitCommit
	for {
		resp, err := getter()
		if err == io.EOF {
			return commits
		}
		require.NoError(t, err)

		commits = append(commits, resp.GetCommits()...)
	}
}
