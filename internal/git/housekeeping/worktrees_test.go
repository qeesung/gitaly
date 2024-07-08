package housekeeping

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestCleanupDisconnectedWorktrees_doesNothingWithoutWorktrees(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
	worktreePath := filepath.Join(testhelper.TempDir(t), "worktree")

	countingGitCmdFactory := gittest.NewCountingCommandFactory(t, cfg)

	repo := localrepo.New(testhelper.NewLogger(t), config.NewLocator(cfg), countingGitCmdFactory, nil, repoProto)

	// If this command did spawn git-worktree(1) we'd see an error. It doesn't though because it
	// detects that there aren't any worktrees at all.
	require.NoError(t, cleanDisconnectedWorktrees(ctx, repo))
	countingGitCmdFactory.RequireCommandCount(t, "worktree", 0)

	// Create a worktree, but remove the actual worktree path so that it will be disconnected.
	gittest.AddWorktree(t, cfg, repoPath, worktreePath)
	require.NoError(t, os.RemoveAll(worktreePath))

	// We have now added a worktree now, so it should detect that there are worktrees and thus
	// spawn the Git command.
	require.NoError(t, cleanDisconnectedWorktrees(ctx, repo))
	countingGitCmdFactory.RequireCommandCount(t, "worktree", 1)

	// Trigger the cleanup again. As we have just deleted the worktree, we should not see
	// another execution of git-worktree(1).
	require.NoError(t, cleanDisconnectedWorktrees(ctx, repo))
	countingGitCmdFactory.RequireCommandCount(t, "worktree", 1)
}

func TestRemoveWorktree(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	existingWorktreePath := filepath.Join(repoPath, GitlabWorktreePrefix, "existing")
	gittest.AddWorktree(t, cfg, repoPath, existingWorktreePath)

	disconnectedWorktreePath := filepath.Join(repoPath, GitlabWorktreePrefix, "disconnected")
	gittest.AddWorktree(t, cfg, repoPath, disconnectedWorktreePath)
	require.NoError(t, os.RemoveAll(disconnectedWorktreePath))

	orphanedWorktreePath := filepath.Join(repoPath, GitlabWorktreePrefix, "orphaned")
	require.NoError(t, os.MkdirAll(orphanedWorktreePath, perm.PrivateDir))

	for _, tc := range []struct {
		worktree     string
		errorIs      error
		expectExists bool
	}{
		{
			worktree:     "existing",
			expectExists: false,
		},
		{
			worktree:     "disconnected",
			expectExists: false,
		},
		{
			worktree:     "unknown",
			errorIs:      errUnknownWorktree,
			expectExists: false,
		},
		{
			worktree:     "orphaned",
			errorIs:      errUnknownWorktree,
			expectExists: true,
		},
	} {
		t.Run(tc.worktree, func(t *testing.T) {
			ctx := testhelper.Context(t)

			worktreePath := filepath.Join(repoPath, GitlabWorktreePrefix, tc.worktree)

			err := removeWorktree(ctx, repo, tc.worktree)
			if tc.errorIs == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.errorIs)
			}

			if tc.expectExists {
				require.DirExists(t, worktreePath)
			} else {
				require.NoDirExists(t, worktreePath)
			}
		})
	}
}
