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

	chainPath := filepath.Join(testRepoPath, CommitGraphChainRelPath)

	_, err := os.Stat(chainPath)
	require.True(t, os.IsNotExist(err))

	gittest.CreateCommit(
		t,
		testRepoPath,
		t.Name(),
		&gittest.CreateCommitOpts{Message: t.Name()},
	)

	res, err := c.WriteCommitGraph(ctx, &gitalypb.WriteCommitGraphRequest{
		Repository:    testRepo,
		SplitStrategy: gitalypb.WriteCommitGraphRequest_SizeMultiple,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	require.FileExists(t, chainPath)
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
		&gittest.CreateCommitOpts{Message: t.Name() + time.Now().String()},
	)

	chainPath := filepath.Join(testRepoPath, CommitGraphChainRelPath)

	_, err := os.Stat(chainPath)
	require.True(t, os.IsNotExist(err))

	res, err := c.WriteCommitGraph(ctx, &gitalypb.WriteCommitGraphRequest{
		Repository:    testRepo,
		SplitStrategy: gitalypb.WriteCommitGraphRequest_SizeMultiple,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.FileExists(t, chainPath)

	// Reset the mtime of commit-graph-chain file to use
	// as basis to detect changes
	require.NoError(t, os.Chtimes(chainPath, time.Time{}, time.Time{}))
	info, err := os.Stat(chainPath)
	require.NoError(t, err)
	mt := info.ModTime()

	gittest.CreateCommit(
		t,
		testRepoPath,
		t.Name(),
		&gittest.CreateCommitOpts{Message: t.Name() + time.Now().String()},
	)

	res, err = c.WriteCommitGraph(ctx, &gitalypb.WriteCommitGraphRequest{
		Repository:    testRepo,
		SplitStrategy: gitalypb.WriteCommitGraphRequest_SizeMultiple,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.FileExists(t, chainPath)

	assertModTimeAfter(t, mt, chainPath)
}
