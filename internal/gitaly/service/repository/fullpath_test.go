package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

func TestSetFullPath(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, client := setupRepositoryServiceWithoutRepo(t)

	t.Run("missing repository", func(t *testing.T) {
		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: nil,
			Path:       "my/repo",
		})
		require.Nil(t, response)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty Repository")
	})

	t.Run("missing path", func(t *testing.T) {
		repo, _ := gittest.CreateRepository(ctx, t, cfg)

		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: repo,
			Path:       "",
		})
		require.Nil(t, response)
		testhelper.RequireGrpcError(t, helper.ErrInvalidArgumentf("no path provided"), err)
	})

	t.Run("invalid storage", func(t *testing.T) {
		repo, _ := gittest.CreateRepository(ctx, t, cfg)
		repo.StorageName = ""

		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: repo,
			Path:       "my/repo",
		})
		require.Nil(t, response)
		// We can't assert a concrete error given that they're different when running with
		// Praefect or without Praefect.
		require.Error(t, err)
	})

	t.Run("nonexistent repo", func(t *testing.T) {
		repo := &gitalypb.Repository{
			RelativePath: "/path/to/repo.git",
			StorageName:  cfg.Storages[0].Name,
		}
		repoPath, err := config.NewLocator(cfg).GetPath(repo)
		require.NoError(t, err)

		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: repo,
			Path:       "my/repo",
		})

		require.Nil(t, response)

		expectedErr := fmt.Sprintf("rpc error: code = NotFound desc = setting config: rpc "+
			"error: code = NotFound desc = GetRepoPath: not a git repository: %q", repoPath)
		if testhelper.IsPraefectEnabled() {
			expectedErr = `rpc error: code = NotFound desc = mutator call: route repository mutator: get repository id: repository "default"/"/path/to/repo.git" not found`
		}

		require.EqualError(t, err, expectedErr)
	})

	t.Run("normal repo", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: repo,
			Path:       "foo/bar",
		})
		require.NoError(t, err)
		testhelper.ProtoEqual(t, &gitalypb.SetFullPathResponse{}, response)

		fullPath := gittest.Exec(t, cfg, "-C", repoPath, "config", fullPathKey)
		require.Equal(t, "foo/bar", text.ChompBytes(fullPath))
	})

	t.Run("missing config", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

		configPath := filepath.Join(repoPath, "config")
		require.NoError(t, os.Remove(configPath))

		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: repo,
			Path:       "foo/bar",
		})
		require.NoError(t, err)
		testhelper.ProtoEqual(t, &gitalypb.SetFullPathResponse{}, response)

		fullPath := gittest.Exec(t, cfg, "-C", repoPath, "config", fullPathKey)
		require.Equal(t, "foo/bar", text.ChompBytes(fullPath))
	})

	t.Run("multiple times", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

		for i := 0; i < 5; i++ {
			response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
				Repository: repo,
				Path:       fmt.Sprintf("foo/%d", i),
			})
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.SetFullPathResponse{}, response)
		}

		fullPath := gittest.Exec(t, cfg, "-C", repoPath, "config", "--get-all", fullPathKey)
		require.Equal(t, "foo/4", text.ChompBytes(fullPath))
	})

	t.Run("multiple preexisting paths", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

		for i := 0; i < 5; i++ {
			gittest.Exec(t, cfg, "-C", repoPath, "config", "--add", fullPathKey, fmt.Sprintf("foo/%d", i))
		}

		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: repo,
			Path:       "replace",
		})
		require.NoError(t, err)
		testhelper.ProtoEqual(t, &gitalypb.SetFullPathResponse{}, response)

		fullPath := gittest.Exec(t, cfg, "-C", repoPath, "config", "--get-all", fullPathKey)
		require.Equal(t, "replace", text.ChompBytes(fullPath))
	})
}
