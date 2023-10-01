package repository

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/quarantine"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

func TestRepositorySize_poolMember(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionalLinkRepository).Run(t, testRepositorySizePoolMember)
}

func testRepositorySizePoolMember(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg, client := setupRepositoryService(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	// Write a large, reachable blob that would get pulled into the object pool. Note that the data must be part of
	// a packfile or otherwise it won't get pulled into the object pool. We thus repack the repository first before
	// linking it to the pool repository.
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch), gittest.WithTreeEntries(
		gittest.TreeEntry{Mode: "100644", Path: "16kbblob", Content: string(uncompressibleData(16 * 1000))},
	))
	gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Adl")
	requireRepositorySize(t, ctx, client, repo, 17)

	// We create an object pool now and link the repository to it. When repacking, this should cause us to
	// deduplicate all objects and thus reduce the size of the repository.
	gittest.CreateObjectPool(t, ctx, cfg, repo, gittest.CreateObjectPoolConfig{
		LinkRepositoryToObjectPool: true,
	})
	gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Adl")

	// The blob has been deduplicated, so the repository should now be basically empty again.
	requireRepositorySize(t, ctx, client, repo, 0)
}

func TestRepositorySize_normalRepository(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	// An empty repository should have a size of zero. This is not quite true as there are some data structures like
	// the gitconfig, but they do not exceed 1kB of data.
	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	requireRepositorySize(t, ctx, client, repo, 0)

	// When writing a largish blob into the repository it's expected to grow.
	gittest.WriteBlob(t, cfg, repoPath, uncompressibleData(16*1024))
	requireRepositorySize(t, ctx, client, repo, 16)

	// Also, updating any other files should cause a size increase.
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "packed-refs"), uncompressibleData(7*1024), perm.PrivateFile))
	requireRepositorySize(t, ctx, client, repo, 23)

	// Even garbage should increase the size.
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "garbage"), uncompressibleData(5*1024), perm.PrivateFile))
	requireRepositorySize(t, ctx, client, repo, 28)
}

func TestRepositorySize_failure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, client := setupRepositoryService(t)

	for _, tc := range []struct {
		description string
		repo        *gitalypb.Repository
		expectedErr error
	}{
		{
			description: "no repository provided",
			repo:        nil,
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
	} {
		t.Run(tc.description, func(t *testing.T) {
			_, err := client.RepositorySize(ctx, &gitalypb.RepositorySizeRequest{
				Repository: tc.repo,
			})
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func BenchmarkRepositorySize(b *testing.B) {
	ctx := testhelper.Context(b)
	cfg, client := setupRepositoryService(b)

	for _, tc := range []struct {
		desc  string
		setup func(b *testing.B) *gitalypb.Repository
	}{
		{
			desc: "empty repository",
			setup: func(b *testing.B) *gitalypb.Repository {
				repo, _ := gittest.CreateRepository(b, ctx, cfg)
				return repo
			},
		},
		{
			desc: "benchmark repository",
			setup: func(b *testing.B) *gitalypb.Repository {
				repo, _ := gittest.CreateRepository(b, ctx, cfg, gittest.CreateRepositoryConfig{
					Seed: "benchmark.git",
				})
				return repo
			},
		},
	} {
		b.Run(tc.desc, func(b *testing.B) {
			repo := tc.setup(b)

			b.StartTimer()

			for i := 0; i < b.N; i++ {
				_, err := client.RepositorySize(ctx, &gitalypb.RepositorySizeRequest{
					Repository: repo,
				})
				require.NoError(b, err)
			}
		})
	}
}

func TestGetObjectDirectorySize_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo.GitObjectDirectory = "objects/"

	// Initially, the object directory should be empty and thus have a size of zero.
	requireObjectDirectorySize(t, ctx, client, repo, 0)

	// Writing an object into the repository should increase the size accordingly.
	gittest.WriteBlob(t, cfg, repoPath, uncompressibleData(16*1024))
	requireObjectDirectorySize(t, ctx, client, repo, 16)
}

func TestGetObjectDirectorySize_quarantine(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)
	locator := config.NewLocator(cfg)
	logger := testhelper.NewLogger(t)

	t.Run("quarantined repo", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
		repo.GitObjectDirectory = "objects/"
		gittest.WriteBlob(t, cfg, repoPath, uncompressibleData(16*1024))
		requireObjectDirectorySize(t, ctx, client, repo, 16)

		quarantine, _, err := quarantine.New(ctx, gittest.RewrittenRepository(t, ctx, cfg, repo), logger, locator)
		require.NoError(t, err)

		// quarantine.New in Gitaly would receive an already rewritten repository. Gitaly would then calculate
		// the quarantine directories based on the rewritten relative path. That quarantine would then be looped
		// through Rails, which would then send a request with the quarantine object directories set based on the
		// rewritten relative path but with the original relative path of the repository. Since we're using the production
		// helpers here, we need to manually substitute the rewritten relative path with the original one when sending
		// it back through the API.
		quarantinedRepo := quarantine.QuarantinedRepo()
		quarantinedRepo.RelativePath = repo.RelativePath

		// The size of the quarantine directory should be zero.
		requireObjectDirectorySize(t, ctx, client, quarantinedRepo, 0)
	})

	t.Run("quarantined repo with different relative path", func(t *testing.T) {
		repo1, _ := gittest.CreateRepository(t, ctx, cfg)
		quarantine1, _, err := quarantine.New(ctx, gittest.RewrittenRepository(t, ctx, cfg, repo1), logger, locator)
		require.NoError(t, err)

		repo2, _ := gittest.CreateRepository(t, ctx, cfg)
		quarantine2, _, err := quarantine.New(ctx, gittest.RewrittenRepository(t, ctx, cfg, repo2), logger, locator)
		require.NoError(t, err)

		// We swap out the the object directories of both quarantines. So while both are
		// valid, we still expect that this RPC call fails because we detect that the
		// swapped-in quarantine directory does not belong to our repository.
		repo := proto.Clone(quarantine1.QuarantinedRepo()).(*gitalypb.Repository)
		repo.GitObjectDirectory = quarantine2.QuarantinedRepo().GetGitObjectDirectory()
		// quarantine.New in Gitaly would receive an already rewritten repository. Gitaly would then calculate
		// the quarantine directories based on the rewritten relative path. That quarantine would then be looped
		// through Rails, which would then send a request with the quarantine object directories set based on the
		// rewritten relative path but with the original relative path of the repository. Since we're using the production
		// helpers here, we need to manually substitute the rewritten relative path with the original one when sending
		// it back through the API.
		repo.RelativePath = repo1.RelativePath

		response, err := client.GetObjectDirectorySize(ctx, &gitalypb.GetObjectDirectorySizeRequest{
			Repository: repo,
		})
		require.Error(t, err, "rpc error: code = InvalidArgument desc = GetObjectDirectoryPath: relative path escapes root directory")
		require.Nil(t, response)
	})
}

func requireRepositorySize(tb testing.TB, ctx context.Context, client gitalypb.RepositoryServiceClient, repo *gitalypb.Repository, expectedSize int64) {
	tb.Helper()

	response, err := client.RepositorySize(ctx, &gitalypb.RepositorySizeRequest{
		Repository: repo,
	})
	require.NoError(tb, err)
	require.Equal(tb, expectedSize, response.GetSize())
}

func requireObjectDirectorySize(tb testing.TB, ctx context.Context, client gitalypb.RepositoryServiceClient, repo *gitalypb.Repository, expectedSize int64) {
	tb.Helper()

	response, err := client.GetObjectDirectorySize(ctx, &gitalypb.GetObjectDirectorySizeRequest{
		Repository: repo,
	})
	require.NoError(tb, err)
	require.Equal(tb, expectedSize, response.GetSize())
}

// uncompressibleData returns data that will not be easily compressible by Git. This is required because
// well-compressible objects would not lead to a repository size increase due to the zlib compression used for Git
// objects.
func uncompressibleData(bytes int) []byte {
	data := make([]byte, bytes)
	_, _ = rand.Read(data[:])
	return data
}
