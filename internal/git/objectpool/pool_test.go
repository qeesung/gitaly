package objectpool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestFromProto(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	logger := testhelper.NewLogger(t)
	locator := config.NewLocator(cfg)

	t.Run("successful", func(t *testing.T) {
		cfg, pool, _ := setupObjectPool(t, ctx)
		locator := config.NewLocator(cfg)

		_, err := FromProto(ctx, logger, locator, nil, nil, nil, nil, pool.ToProto())
		require.NoError(t, err)
	})

	t.Run("nonexistent", func(t *testing.T) {
		_, err := FromProto(ctx, logger, locator, nil, nil, nil, nil, &gitalypb.ObjectPool{
			Repository: &gitalypb.Repository{
				StorageName:  cfg.Storages[0].Name,
				RelativePath: gittest.NewObjectPoolName(t),
			},
		})
		require.Equal(t, ErrInvalidPoolRepository, err)
	})

	t.Run("unknown storage", func(t *testing.T) {
		_, err := FromProto(ctx, logger, locator, nil, nil, nil, nil, &gitalypb.ObjectPool{
			Repository: &gitalypb.Repository{
				StorageName:  "mepmep",
				RelativePath: gittest.NewObjectPoolName(t),
			},
		})
		require.Equal(t, storage.NewStorageNotFoundError("mepmep"), err)
	})
}

func TestFromRepo_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, pool, repo := setupObjectPool(t, ctx)
	logger := testhelper.NewLogger(t)
	locator := config.NewLocator(cfg)

	require.NoError(t, pool.Link(ctx, repo))

	poolFromRepo, err := FromRepo(ctx, logger, locator, pool.gitCmdFactory, nil, nil, nil, repo)
	require.NoError(t, err)
	require.Equal(t, pool.GetRelativePath(), poolFromRepo.GetRelativePath())
	require.Equal(t, pool.GetStorageName(), poolFromRepo.GetStorageName())
}

func TestFromRepo_failures(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	t.Run("without alternates file", func(t *testing.T) {
		cfg, pool, repo := setupObjectPool(t, ctx)
		logger := testhelper.NewLogger(t)
		locator := config.NewLocator(cfg)

		poolFromRepo, err := FromRepo(ctx, logger, locator, pool.gitCmdFactory, nil, nil, nil, repo)
		require.Equal(t, ErrAlternateObjectDirNotExist, err)
		require.Nil(t, poolFromRepo)
	})

	for _, tc := range []struct {
		desc        string
		fileContent []byte
		expectedErr error
	}{
		{
			desc:        "alternates points to non existent path",
			fileContent: []byte("/tmp/invalid_path"),
			expectedErr: ErrInvalidPoolRepository,
		},
		{
			desc:        "alternates is empty",
			fileContent: nil,
			expectedErr: ErrAlternateObjectDirNotExist,
		},
		{
			desc:        "alternates is commented",
			fileContent: []byte("#/tmp/invalid/path"),
			expectedErr: ErrAlternateObjectDirNotExist,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, pool, repo := setupObjectPool(t, ctx)
			logger := testhelper.NewLogger(t)
			locator := config.NewLocator(cfg)
			repoPath, err := repo.Path(ctx)
			require.NoError(t, err)

			require.NoError(t, os.MkdirAll(filepath.Join(repoPath, "objects", "info"), perm.SharedDir))
			alternateFilePath := filepath.Join(repoPath, "objects", "info", "alternates")
			require.NoError(t, os.WriteFile(alternateFilePath, tc.fileContent, perm.SharedFile))
			poolFromRepo, err := FromRepo(ctx, logger, locator, pool.gitCmdFactory, nil, nil, nil, repo)
			require.Equal(t, tc.expectedErr, err)
			require.Nil(t, poolFromRepo)

			require.NoError(t, os.Remove(alternateFilePath))
		})
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	_, pool, _ := setupObjectPool(t, ctx)

	require.True(t, pool.Exists(ctx))
	require.NoError(t, pool.Remove(ctx))
	require.False(t, pool.Exists(ctx))
}
