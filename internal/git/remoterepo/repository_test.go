package remoterepo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/git/remoterepo"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/commit"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/ref"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
)

func TestRepository(t *testing.T) {
	cfg := testcfg.Build(t)

	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
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
		gitalypb.RegisterCommitServiceServer(srv, commit.NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetLinguist(),
			deps.GetCatfileCache(),
		))
		gitalypb.RegisterRefServiceServer(srv, ref.NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetTxManager(),
			deps.GetCatfileCache(),
		))
	})

	pool := client.NewPool()
	defer pool.Close()

	gittest.TestRepository(t, cfg, func(ctx context.Context, t testing.TB, seeded bool) (git.Repository, string) {
		t.Helper()

		seed := ""
		if seeded {
			seed = gittest.SeedGitLabTest
		}

		ctx, err := storage.InjectGitalyServers(ctx, "default", cfg.SocketPath, cfg.Auth.Token)
		require.NoError(t, err)

		pbRepo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			Seed: seed,
		})

		repo, err := remoterepo.New(metadata.OutgoingToIncoming(ctx), pbRepo, pool)
		require.NoError(t, err)
		return repo, repoPath
	})
}
