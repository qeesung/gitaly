package repository

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestWriteCommitGraph(t *testing.T) {
	cfg, repo, repoPath, client := setupRepositoryService(t)

	ctx, cancel := testhelper.Context()
	defer cancel()

	commitGraphPath := filepath.Join(repoPath, CommitGraphRelPath)

	require.NoFileExists(t, commitGraphPath)

	gittest.WriteCommit(
		t,
		cfg,
		repoPath,
		gittest.WithBranch(t.Name()),
	)

	res, err := client.WriteCommitGraph(ctx, &gitalypb.WriteCommitGraphRequest{Repository: repo})
	require.NoError(t, err)
	require.NotNil(t, res)

	require.FileExists(t, commitGraphPath)
}

func TestUpdateCommitGraph(t *testing.T) {
	cfg, repo, repoPath, client := setupRepositoryService(t)

	ctx, cancel := testhelper.Context()
	defer cancel()

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(t.Name()))

	commitGraphPath := filepath.Join(repoPath, CommitGraphRelPath)

	require.NoFileExists(t, commitGraphPath)

	res, err := client.WriteCommitGraph(ctx, &gitalypb.WriteCommitGraphRequest{Repository: repo})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.FileExists(t, commitGraphPath)

	// Reset the mtime of commit-graph file to use
	// as basis to detect changes
	require.NoError(t, os.Chtimes(commitGraphPath, time.Time{}, time.Time{}))
	info, err := os.Stat(commitGraphPath)
	require.NoError(t, err)
	mt := info.ModTime()

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(t.Name()))

	res, err = client.WriteCommitGraph(ctx, &gitalypb.WriteCommitGraphRequest{Repository: repo})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.FileExists(t, commitGraphPath)

	assertModTimeAfter(t, mt, commitGraphPath)
}
