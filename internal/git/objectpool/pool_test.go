package objectpool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
)

func TestNewObjectPool(t *testing.T) {
	cfg := testcfg.Build(t)

	locator := config.NewLocator(cfg)

	_, err := NewObjectPool(locator, nil, nil, nil, cfg.Storages[0].Name, gittest.NewObjectPoolName(t))
	require.NoError(t, err)

	_, err = NewObjectPool(locator, nil, nil, nil, "mepmep", gittest.NewObjectPoolName(t))
	require.Error(t, err, "creating pool in storage that does not exist should fail")
}

func TestNewFromRepoSuccess(t *testing.T) {
	ctx := testhelper.Context(t)

	_, pool, testRepo := setupObjectPool(t, ctx)

	require.NoError(t, pool.Create(ctx, testRepo))
	require.NoError(t, pool.Link(ctx, testRepo))

	poolFromRepo, err := FromRepo(pool.locator, pool.gitCmdFactory, nil, nil, testRepo)
	require.NoError(t, err)
	require.Equal(t, pool.relativePath, poolFromRepo.relativePath)
	require.Equal(t, pool.storageName, poolFromRepo.storageName)
}

func TestNewFromRepoNoObjectPool(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, pool, testRepo := setupObjectPool(t, ctx)

	testRepoPath := filepath.Join(cfg.Storages[0].Path, testRepo.RelativePath)

	// no alternates file
	poolFromRepo, err := FromRepo(pool.locator, pool.gitCmdFactory, nil, nil, testRepo)
	require.Equal(t, ErrAlternateObjectDirNotExist, err)
	require.Nil(t, poolFromRepo)

	// with an alternates file
	testCases := []struct {
		desc        string
		fileContent []byte
		expectedErr error
	}{
		{
			desc:        "points to non existent path",
			fileContent: []byte("/tmp/invalid_path"),
			expectedErr: ErrInvalidPoolRepository,
		},
		{
			desc:        "empty file",
			fileContent: nil,
			expectedErr: nil,
		},
		{
			desc:        "first line commented out",
			fileContent: []byte("#/tmp/invalid/path"),
			expectedErr: ErrAlternateObjectDirNotExist,
		},
	}

	require.NoError(t, os.MkdirAll(filepath.Join(testRepoPath, "objects", "info"), 0o755))

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			alternateFilePath := filepath.Join(testRepoPath, "objects", "info", "alternates")
			require.NoError(t, os.WriteFile(alternateFilePath, tc.fileContent, 0o644))
			poolFromRepo, err := FromRepo(pool.locator, pool.gitCmdFactory, nil, nil, testRepo)
			require.Equal(t, tc.expectedErr, err)
			require.Nil(t, poolFromRepo)

			require.NoError(t, os.Remove(alternateFilePath))
		})
	}
}

func TestCreate(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, pool, testRepo := setupObjectPool(t, ctx)

	testRepoPath := filepath.Join(cfg.Storages[0].Path, testRepo.RelativePath)

	masterSha := gittest.Exec(t, cfg, "-C", testRepoPath, "show-ref", "master")

	err := pool.Create(ctx, testRepo)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, pool.Remove(ctx))
	}()

	require.True(t, pool.IsValid())

	// No hooks
	assert.NoDirExists(t, filepath.Join(pool.FullPath(), "hooks"))

	// origin is set
	out := gittest.Exec(t, cfg, "-C", pool.FullPath(), "remote", "get-url", "origin")
	assert.Equal(t, testRepoPath, strings.TrimRight(string(out), "\n"))

	// refs exist
	out = gittest.Exec(t, cfg, "-C", pool.FullPath(), "show-ref", "refs/heads/master")
	assert.Equal(t, masterSha, out)

	// No problems
	out = gittest.Exec(t, cfg, "-C", pool.FullPath(), "cat-file", "-s", "55bc176024cfa3baaceb71db584c7e5df900ea65")
	assert.Equal(t, "282\n", string(out))
}

func TestCreateSubDirsExist(t *testing.T) {
	ctx := testhelper.Context(t)

	_, pool, testRepo := setupObjectPool(t, ctx)

	err := pool.Create(ctx, testRepo)
	require.NoError(t, err)

	require.NoError(t, pool.Remove(ctx))

	// Recreate pool so the subdirs exist already
	err = pool.Create(ctx, testRepo)
	require.NoError(t, err)
}

func TestRemove(t *testing.T) {
	ctx := testhelper.Context(t)

	_, pool, testRepo := setupObjectPool(t, ctx)

	err := pool.Create(ctx, testRepo)
	require.NoError(t, err)

	require.True(t, pool.Exists())
	require.NoError(t, pool.Remove(ctx))
	require.False(t, pool.Exists())
}
