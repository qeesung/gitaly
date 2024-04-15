package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

//nolint:staticcheck
func TestSetFullPath(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, client := setupRepositoryService(t)

	t.Run("missing repository", func(t *testing.T) {
		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: nil,
			Path:       "my/repo",
		})

		require.Nil(t, response)
		if testhelper.IsWALEnabled() {
			testhelper.RequireGrpcError(t, structerr.NewUnimplemented("SetFullPath RPC is not supported by WAL"), err)
		} else {
			testhelper.RequireGrpcError(t, structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet), err)
		}
	})

	t.Run("missing path", func(t *testing.T) {
		repo, _ := gittest.CreateRepository(t, ctx, cfg)

		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: repo,
			Path:       "",
		})
		require.Nil(t, response)
		if testhelper.IsWALEnabled() {
			testhelper.RequireGrpcError(t, structerr.NewUnimplemented("SetFullPath RPC is not supported by WAL"), err)
		} else {
			testhelper.RequireGrpcError(t, structerr.NewInvalidArgument("no path provided"), err)
		}
	})

	t.Run("invalid storage", func(t *testing.T) {
		repo, _ := gittest.CreateRepository(t, ctx, cfg)
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

		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: repo,
			Path:       "my/repo",
		})
		require.Nil(t, response)

		if testhelper.IsWALEnabled() {
			testhelper.RequireGrpcError(t, structerr.NewUnimplemented("SetFullPath RPC is not supported by WAL"), err)
		} else {
			testhelper.RequireGrpcError(t, testhelper.ToInterceptedMetadata(
				structerr.New("%w", storage.NewRepositoryNotFoundError(repo.StorageName, repo.RelativePath)),
			), err)
		}
	})

	t.Run("normal repo", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: repo,
			Path:       "foo/bar",
		})
		if testhelper.IsWALEnabled() {
			testhelper.RequireGrpcError(t, structerr.NewUnimplemented("SetFullPath RPC is not supported by WAL"), err)
		} else {
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.SetFullPathResponse{}, response)

			fullPath := gittest.Exec(t, cfg, "-C", repoPath, "config", fullPathKey)
			require.Equal(t, "foo/bar", text.ChompBytes(fullPath))
		}
	})

	t.Run("missing config", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		configPath := filepath.Join(repoPath, "config")
		require.NoError(t, os.Remove(configPath))

		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: repo,
			Path:       "foo/bar",
		})

		if testhelper.IsWALEnabled() {
			testhelper.RequireGrpcError(t, structerr.NewUnimplemented("SetFullPath RPC is not supported by WAL"), err)
		} else {
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.SetFullPathResponse{}, response)

			fullPath := gittest.Exec(t, cfg, "-C", repoPath, "config", fullPathKey)
			require.Equal(t, "foo/bar", text.ChompBytes(fullPath))
		}
	})

	t.Run("multiple times", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		for i := 0; i < 5; i++ {
			response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
				Repository: repo,
				Path:       fmt.Sprintf("foo/%d", i),
			})
			if testhelper.IsWALEnabled() {
				testhelper.RequireGrpcError(t, structerr.NewUnimplemented("SetFullPath RPC is not supported by WAL"), err)
			} else {
				require.NoError(t, err)
				testhelper.ProtoEqual(t, &gitalypb.SetFullPathResponse{}, response)
			}
		}

		if !testhelper.IsWALEnabled() {
			fullPath := gittest.Exec(t, cfg, "-C", repoPath, "config", "--get-all", fullPathKey)
			require.Equal(t, "foo/4", text.ChompBytes(fullPath))
		}
	})

	t.Run("multiple preexisting paths", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		for i := 0; i < 5; i++ {
			gittest.Exec(t, cfg, "-C", repoPath, "config", "--add", fullPathKey, fmt.Sprintf("foo/%d", i))
		}

		response, err := client.SetFullPath(ctx, &gitalypb.SetFullPathRequest{
			Repository: repo,
			Path:       "replace",
		})

		if testhelper.IsWALEnabled() {
			testhelper.RequireGrpcError(t, structerr.NewUnimplemented("SetFullPath RPC is not supported by WAL"), err)
		} else {
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.SetFullPathResponse{}, response)

			fullPath := gittest.Exec(t, cfg, "-C", repoPath, "config", "--get-all", fullPathKey)
			require.Equal(t, "replace", text.ChompBytes(fullPath))
		}
	})
}

//nolint:staticcheck
func TestFullPath(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, client := setupRepositoryService(t)

	t.Run("missing repository", func(t *testing.T) {
		response, err := client.FullPath(ctx, &gitalypb.FullPathRequest{
			Repository: nil,
		})
		require.Nil(t, response)
		testhelper.RequireGrpcError(t, structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet), err)
	})

	t.Run("nonexistent repository", func(t *testing.T) {
		repo := &gitalypb.Repository{
			RelativePath: "/path/to/repo.git",
			StorageName:  cfg.Storages[0].Name,
		}

		response, err := client.FullPath(ctx, &gitalypb.FullPathRequest{
			Repository: repo,
		})
		require.Nil(t, response)
		testhelper.RequireGrpcError(t, testhelper.ToInterceptedMetadata(
			structerr.New("%w", storage.NewRepositoryNotFoundError(repo.StorageName, repo.RelativePath)),
		), err)
	})

	t.Run("missing config", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		configPath := filepath.Join(repoPath, "config")
		require.NoError(t, os.Remove(configPath))

		response, err := client.FullPath(ctx, &gitalypb.FullPathRequest{
			Repository: repo,
		})
		require.Nil(t, response)
		require.EqualError(t, err, "rpc error: code = Internal desc = fetch config: exit status 1")
		testhelper.RequireGrpcCode(t, err, codes.Internal)
	})

	t.Run("existing config", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		gittest.Exec(t, cfg, "-C", repoPath, "config", "--add", fullPathKey, "foo/bar")

		response, err := client.FullPath(ctx, &gitalypb.FullPathRequest{
			Repository: repo,
		})
		require.NoError(t, err)
		testhelper.ProtoEqual(t, &gitalypb.FullPathResponse{Path: "foo/bar"}, response)
	})
}
