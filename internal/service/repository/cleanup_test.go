package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestCleanupDeletesRefsLocks(t *testing.T) {
	server, serverSocketPath := runRepoServer(t)
	defer server.Stop()

	client, conn := newRepositoryClient(t, serverSocketPath)
	defer conn.Close()

	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	ctx, cancel := testhelper.Context()
	defer cancel()

	req := &gitalypb.CleanupRequest{Repository: testRepo}
	refsPath := filepath.Join(testRepoPath, "refs")

	keepRefPath := filepath.Join(refsPath, "heads", "keepthis")
	createFileWithTimes(keepRefPath, freshTime)
	keepOldRefPath := filepath.Join(refsPath, "heads", "keepthisalso")
	createFileWithTimes(keepOldRefPath, oldTime)
	keepDeceitfulRef := filepath.Join(refsPath, "heads", " .lock.not-actually-a-lock.lock ")
	createFileWithTimes(keepDeceitfulRef, oldTime)

	keepLockPath := filepath.Join(refsPath, "heads", "keepthis.lock")
	createFileWithTimes(keepLockPath, freshTime)

	deleteLockPath := filepath.Join(refsPath, "heads", "deletethis.lock")
	createFileWithTimes(deleteLockPath, oldTime)

	c, err := client.Cleanup(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, c)

	// Sanity checks
	assert.FileExists(t, keepRefPath)
	assert.FileExists(t, keepOldRefPath)
	assert.FileExists(t, keepDeceitfulRef)

	assert.FileExists(t, keepLockPath)

	testhelper.AssertFileNotExists(t, deleteLockPath)
}

func TestCleanupDeletesPackedRefsLock(t *testing.T) {
	server, serverSocketPath := runRepoServer(t)
	defer server.Stop()

	client, conn := newRepositoryClient(t, serverSocketPath)
	defer conn.Close()

	testCases := []struct {
		desc        string
		lockTime    *time.Time
		shouldExist bool
	}{
		{
			desc:        "with a recent lock",
			lockTime:    &freshTime,
			shouldExist: true,
		},
		{
			desc:        "with an old lock",
			lockTime:    &oldTime,
			shouldExist: false,
		},
		{
			desc:        "with a non-existing lock",
			lockTime:    nil,
			shouldExist: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
			defer cleanupFn()

			// Force the packed-refs file to have an old time to test that even
			// in that case it doesn't get deleted
			packedRefsPath := filepath.Join(testRepoPath, "packed-refs")
			os.Chtimes(packedRefsPath, oldTime, oldTime)

			req := &gitalypb.CleanupRequest{Repository: testRepo}
			lockPath := filepath.Join(testRepoPath, "packed-refs.lock")

			if tc.lockTime != nil {
				createFileWithTimes(lockPath, *tc.lockTime)
			}

			ctx, cancel := testhelper.Context()
			defer cancel()

			c, err := client.Cleanup(ctx, req)

			// Sanity checks
			assert.FileExists(t, filepath.Join(testRepoPath, "HEAD")) // For good measure
			assert.FileExists(t, packedRefsPath)

			if tc.shouldExist {
				assert.FileExists(t, lockPath)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, c)

				testhelper.AssertFileNotExists(t, lockPath)
			}
		})
	}
}

func TestCleanupDeletesStaleWorktrees(t *testing.T) {
	server, serverSocketPath := runRepoServer(t)
	defer server.Stop()

	client, conn := newRepositoryClient(t, serverSocketPath)
	defer conn.Close()

	testCases := []struct {
		desc         string
		worktreeTime time.Time
		shouldExist  bool
	}{
		{
			desc:         "with a recent worktree",
			worktreeTime: freshTime,
			shouldExist:  true,
		},
		{
			desc:         "with a slightly old worktree",
			worktreeTime: oldTime,
			shouldExist:  true,
		},
		{
			desc:         "with an old worktree",
			worktreeTime: oldTreeTime,
			shouldExist:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
			defer cleanupFn()

			req := &gitalypb.CleanupRequest{Repository: testRepo}

			testhelper.AddWorktree(t, testRepoPath, "test-worktree")
			basePath := filepath.Join(testRepoPath, "worktrees")
			worktreePath := filepath.Join(basePath, "test-worktree")

			require.NoError(t, os.Chtimes(worktreePath, tc.worktreeTime, tc.worktreeTime))

			ctx, cancel := testhelper.Context()
			defer cancel()

			c, err := client.Cleanup(ctx, req)

			// Sanity check
			assert.FileExists(t, filepath.Join(testRepoPath, "HEAD")) // For good measure

			if tc.shouldExist {
				assert.DirExists(t, basePath)
				assert.DirExists(t, worktreePath)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, c)

				testhelper.AssertFileNotExists(t, worktreePath)
			}
		})
	}
}

func TestCleanupDisconnectedWorktrees(t *testing.T) {
	const (
		worktreePath     = "test-worktree"
		worktreeAdminDir = "worktrees"
	)

	addWorkTree := func(t testing.TB, repoPath, worktree string) error {
		cmd := exec.Command(
			"git",
			testhelper.AddWorktreeArgs(repoPath, worktree)...,
		)

		out, err := cmd.CombinedOutput()

		t.Logf("%s: %s", cmd.Args, string(out))

		return err
	}

	server, serverSocketPath := runRepoServer(t)
	defer server.Stop()

	client, conn := newRepositoryClient(t, serverSocketPath)
	defer conn.Close()

	testCases := []struct {
		desc         string
		worktreeTime time.Time
	}{
		{
			desc:         "with a recent disconnected worktree",
			worktreeTime: freshTime,
		},
		{
			desc:         "with a slightly old disconnected worktree",
			worktreeTime: oldTime,
		},
		{
			desc:         "with an old disconnected worktree",
			worktreeTime: oldTreeTime,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
			defer cleanupFn()

			req := &gitalypb.CleanupRequest{Repository: testRepo}

			testhelper.AddWorktree(t, testRepoPath, worktreePath)
			worktreeAdminPath := filepath.Join(
				testRepoPath, worktreeAdminDir, filepath.Base(worktreePath),
			)

			require.NoError(t,
				os.Chtimes(worktreeAdminPath, tc.worktreeTime, tc.worktreeTime),
			)

			ctx, cancel := testhelper.Context()
			defer cancel()

			// removing the work tree path but leaving the administrative files in
			// $GIT_DIR/worktrees will result in the work tree being in a
			// "disconnected" state
			err := os.RemoveAll(worktreePath)
			require.NoError(t, err)

			// a disconnected work tree prevents us from checking out another work
			// tree at the same path
			err = addWorkTree(t, testRepoPath, worktreePath)
			require.Error(t, err)

			// cleanup should prune the disconnected worktree administrative files
			// in $GIT_DIR/worktrees
			_, err = client.Cleanup(ctx, req)
			require.NoError(t, err)
			testhelper.AssertFileNotExists(t, worktreeAdminPath)

			out, err := exec.Command("ls", "-al", worktreeAdminPath).CombinedOutput()
			t.Logf("ls -al %s: %s", worktreeAdminPath, out)

			// if the worktree administrative files are pruned, then we should be able
			// to checkout another worktree at the same path
			err = addWorkTree(t, testRepoPath, worktreePath)
			require.NoError(t, err)
		})
	}
}

func TestCleanupFileLocks(t *testing.T) {
	server, serverSocketPath := runRepoServer(t)
	defer server.Stop()

	client, conn := newRepositoryClient(t, serverSocketPath)
	defer conn.Close()

	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	ctx, cancel := testhelper.Context()
	defer cancel()

	req := &gitalypb.CleanupRequest{Repository: testRepo}

	for _, fileName := range lockFiles {
		lockPath := filepath.Join(testRepoPath, fileName)
		// No file on the lock path
		_, err := client.Cleanup(ctx, req)
		assert.NoError(t, err)

		// Fresh lock should remain
		createFileWithTimes(lockPath, freshTime)
		_, err = client.Cleanup(ctx, req)
		assert.NoError(t, err)
		assert.FileExists(t, lockPath)

		// Old lock should be removed
		createFileWithTimes(lockPath, oldTime)
		_, err = client.Cleanup(ctx, req)
		assert.NoError(t, err)
		testhelper.AssertFileNotExists(t, lockPath)
	}
}
