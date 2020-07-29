package repository

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestWriteCommitGraph(t *testing.T) {
	locator := config.NewLocator(config.Config)
	s, stop := runRepoServer(t, locator)
	defer stop()

	c, conn := newRepositoryClient(t, s)
	defer conn.Close()

	testRepo, testRepoPath, cleanup := gittest.CloneRepo(t)
	defer cleanup()

	ctx, cancel := testhelper.Context()
	defer cancel()

	commitGraphPath := filepath.Join(testRepoPath, CommitGraphRelPath)

	_, err := os.Stat(commitGraphPath)
	require.True(t, os.IsNotExist(err))

	gittest.CreateCommit(
		t,
		testRepoPath,
		t.Name(),
		&gittest.CreateCommitOpts{Message: t.Name()},
	)

	res, err := c.WriteCommitGraph(ctx, &gitalypb.WriteCommitGraphRequest{Repository: testRepo})
	require.NoError(t, err)
	require.NotNil(t, res)

	require.FileExists(t, commitGraphPath)
}

func TestUpdateCommitGraph(t *testing.T) {
	locator := config.NewLocator(config.Config)
	s, stop := runRepoServer(t, locator)
	defer stop()

	c, conn := newRepositoryClient(t, s)
	defer conn.Close()

	testRepo, testRepoPath, cleanup := gittest.CloneRepo(t)
	defer cleanup()

	ctx, cancel := testhelper.Context()
	defer cancel()

	gittest.CreateCommit(
		t,
		testRepoPath,
		t.Name(),
		&gittest.CreateCommitOpts{Message: t.Name()},
	)

	commitGraphPath := filepath.Join(testRepoPath, CommitGraphRelPath)

	_, err := os.Stat(commitGraphPath)
	require.True(t, os.IsNotExist(err))

	res, err := c.WriteCommitGraph(ctx, &gitalypb.WriteCommitGraphRequest{Repository: testRepo})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.FileExists(t, commitGraphPath)

	// Reset the mtime of commit-graph file to use
	// as basis to detect changes
	require.NoError(t, os.Chtimes(commitGraphPath, time.Time{}, time.Time{}))
	info, err := os.Stat(commitGraphPath)
	require.NoError(t, err)
	mt := info.ModTime()

	gittest.CreateCommit(
		t,
		testRepoPath,
		t.Name(),
		&gittest.CreateCommitOpts{Message: t.Name()},
	)

	res, err = c.WriteCommitGraph(ctx, &gitalypb.WriteCommitGraphRequest{Repository: testRepo})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.FileExists(t, commitGraphPath)

	assertModTimeAfter(t, mt, commitGraphPath)
}
