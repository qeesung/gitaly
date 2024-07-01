package tempdir

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestNewRepositorySuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(testhelper.Context(t))

	cfg := testcfg.Build(t)
	locator := config.NewLocator(cfg)

	repo, tempDir, err := NewRepository(ctx, cfg.Storages[0].Name, testhelper.NewLogger(t), locator)
	require.NoError(t, err)
	require.Equal(t, cfg.Storages[0].Name, repo.StorageName)
	require.Contains(t, repo.RelativePath, tmpRootPrefix)

	calculatedPath, err := locator.GetRepoPath(ctx, repo, storage.WithRepositoryVerificationSkipped())
	require.NoError(t, err)
	require.Equal(t, tempDir.Path(), calculatedPath)

	require.NoError(t, os.WriteFile(filepath.Join(tempDir.Path(), "test"), []byte("hello"), perm.SharedFile))

	require.DirExists(t, tempDir.Path())

	cancel() // This should trigger async removal of the temporary directory
	tempDir.WaitForCleanup()

	require.NoDirExists(t, tempDir.Path())
}

func TestNewWithPrefix(t *testing.T) {
	cfg := testcfg.Build(t)
	locator := config.NewLocator(cfg)
	ctx := testhelper.Context(t)

	dir, err := NewWithPrefix(ctx, cfg.Storages[0].Name, "foobar-", testhelper.NewLogger(t), locator)
	require.NoError(t, err)

	require.Contains(t, dir.Path(), "/foobar-")
}

func TestNewAsRepositoryFailStorageUnknown(t *testing.T) {
	ctx := testhelper.Context(t)
	_, err := New(ctx, "does-not-exist", testhelper.NewLogger(t), config.NewLocator(config.Cfg{}))
	require.Error(t, err)
}
