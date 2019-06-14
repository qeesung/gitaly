package objectpool

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestLink(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	pool, err := NewObjectPool(testRepo.GetStorageName(), testhelper.NewTestObjectPoolName(t))
	require.NoError(t, err)

	require.NoError(t, pool.Remove(ctx), "make sure pool does not exist prior to creation")
	require.NoError(t, pool.Create(ctx, testRepo), "create pool")

	altPath, err := git.InfoAlternatesPath(testRepo)
	require.NoError(t, err)
	_, err = os.Stat(altPath)
	require.True(t, os.IsNotExist(err))

	require.NoError(t, pool.Link(ctx, testRepo))

	require.FileExists(t, altPath, "alternates file must exist after Link")

	content, err := ioutil.ReadFile(altPath)
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(string(content), "../"), "expected %q to be relative path", content)

	require.NoError(t, pool.Link(ctx, testRepo))

	newContent, err := ioutil.ReadFile(altPath)
	require.NoError(t, err)

	require.Equal(t, content, newContent)

	require.True(t, testhelper.RemoteExists(t, pool.FullPath(), testRepo.GetGlRepository()), "pool remotes should include %v", testRepo)
}

func TestLinkRemoveBitmap(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	pool, err := NewObjectPool(testRepo.GetStorageName(), testhelper.NewTestObjectPoolName(t))
	require.NoError(t, err)

	require.NoError(t, pool.Init(ctx))

	gitPool := []string{"-C", pool.FullPath()}
	testhelper.MustRunCommand(t, nil, "git", append(gitPool, "fetch", testRepoPath, "+refs/*:refs/*")...)

	testhelper.MustRunCommand(t, nil, "git", append(gitPool, "repack", "-adb")...)
	require.Len(t, listBitmaps(t, pool.FullPath()), 1, "pool bitmaps before")

	testhelper.MustRunCommand(t, nil, "git", "-C", testRepoPath, "repack", "-adb")
	require.Len(t, listBitmaps(t, testRepoPath), 1, "member bitmaps before")

	require.NoError(t, pool.Link(ctx, testRepo))

	require.Len(t, listBitmaps(t, pool.FullPath()), 1, "pool bitmaps after")
	require.Len(t, listBitmaps(t, testRepoPath), 0, "member bitmaps after")
}

func listBitmaps(t *testing.T, repoPath string) []string {
	entries, err := ioutil.ReadDir(filepath.Join(repoPath, "objects/pack"))
	require.NoError(t, err)

	var bitmaps []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bitmap") {
			bitmaps = append(bitmaps, entry.Name())
		}
	}

	return bitmaps
}

func TestUnlink(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	pool, err := NewObjectPool(testRepo.GetStorageName(), t.Name())
	require.NoError(t, err)
	defer pool.Remove(ctx)

	require.Error(t, pool.Unlink(ctx, testRepo), "removing a non-existing pool should be an error")

	require.NoError(t, pool.Create(ctx, testRepo), "create pool")
	require.NoError(t, pool.Link(ctx, testRepo), "link test repo to pool")

	require.True(t, testhelper.RemoteExists(t, pool.FullPath(), testRepo.GetGlRepository()), "pool remotes should include %v", testRepo)

	require.NoError(t, pool.Unlink(ctx, testRepo), "unlink repo")
	require.False(t, testhelper.RemoteExists(t, pool.FullPath(), testRepo.GetGlRepository()), "pool remotes should no longer include %v", testRepo)
}
