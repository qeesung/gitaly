package updateref

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func setupUpdater(t *testing.T, ctx context.Context) (config.Cfg, *localrepo.Repo, string, *Updater) {
	t.Helper()

	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto, git.WithSkipHooks())

	updater, err := New(ctx, repo)
	require.NoError(t, err)

	return cfg, repo, repoPath, updater
}

func TestUpdater_create(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, _, repoPath, updater := setupUpdater(t, ctx)

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	require.NoError(t, updater.Create("refs/heads/_create", commitID))
	require.NoError(t, updater.Commit())

	// Verify that the reference was created as expected and that it points to the correct
	// commit.
	require.Equal(t, gittest.ResolveRevision(t, cfg, repoPath, "refs/heads/_create"), commitID)
}

func TestUpdater_update(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath, _ := setupUpdater(t, ctx)

	oldCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
	newCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(oldCommitID))
	otherCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("other"))

	// Check that we can force-update the reference when we don't give an old object ID.
	updater, err := New(ctx, repo)
	require.NoError(t, err)
	require.NoError(t, updater.Update("refs/heads/main", newCommitID, ""))
	require.NoError(t, updater.Commit())
	require.Equal(t, gittest.ResolveRevision(t, cfg, repoPath, "refs/heads/main"), newCommitID)

	// Check that we can update with safety guards when giving an old commit ID.
	updater, err = New(ctx, repo)
	require.NoError(t, err)
	require.NoError(t, updater.Update("refs/heads/main", oldCommitID, newCommitID))
	require.NoError(t, updater.Commit())
	require.Equal(t, gittest.ResolveRevision(t, cfg, repoPath, "refs/heads/main"), oldCommitID)

	// And finally assert that we fail to update the reference in case we're trying to update
	// when the old commit ID doesn't match.
	updater, err = New(ctx, repo)
	require.NoError(t, err)
	require.NoError(t, updater.Update("refs/heads/main", newCommitID, otherCommitID))
	err = updater.Commit()
	require.Error(t, err)
	require.Contains(t, err.Error(), fmt.Sprintf("fatal: commit: cannot lock ref 'refs/heads/main': is at %s but expected %s", oldCommitID, otherCommitID))

	require.Equal(t, gittest.ResolveRevision(t, cfg, repoPath, "refs/heads/main"), oldCommitID)
}

func TestUpdater_delete(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath, updater := setupUpdater(t, ctx)

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	require.NoError(t, updater.Delete("refs/heads/main"))
	require.NoError(t, updater.Commit())

	// Check that the reference was removed.
	_, err := repo.ReadCommit(ctx, "refs/heads/main")
	require.Equal(t, localrepo.ErrObjectNotFound, err)
}

func TestUpdater_prepareLocksTransaction(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, _, repoPath, updater := setupUpdater(t, ctx)

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	require.NoError(t, updater.Update("refs/heads/feature", commitID, ""))
	require.NoError(t, updater.Prepare())
	require.NoError(t, updater.Update("refs/heads/feature", commitID, ""))

	err := updater.Commit()
	require.Error(t, err, "cannot update after prepare")
	require.Contains(t, err.Error(), "fatal: prepared transactions can only be closed")
}

func TestUpdater_concurrentLocking(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)

	if !gittest.GitSupportsStatusFlushing(t, ctx, cfg) {
		t.Skip("git does not support flushing yet, which is known to be flaky")
	}

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto, git.WithSkipHooks())

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	// Create the first updater that prepares the reference transaction so that the reference
	// we're about to update is locked.
	firstUpdater, err := New(ctx, repo)
	require.NoError(t, err)
	require.NoError(t, firstUpdater.Update("refs/heads/master", commitID, ""))
	require.NoError(t, firstUpdater.Prepare())

	// Now we create a second updater that tries to update the same reference.
	secondUpdater, err := New(ctx, repo)
	require.NoError(t, err)
	require.NoError(t, secondUpdater.Update("refs/heads/master", commitID, ""))

	// Preparing this second updater should fail though because we notice that the reference is
	// locked.
	err = secondUpdater.Prepare()
	var errAlreadyLocked *ErrAlreadyLocked
	require.ErrorAs(t, err, &errAlreadyLocked)
	require.Equal(t, err, &ErrAlreadyLocked{
		Ref: "refs/heads/master",
	})

	// Whereas committing the first transaction should succeed.
	require.NoError(t, firstUpdater.Commit())
}

func TestUpdater_bulkOperation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath, updater := setupUpdater(t, ctx)

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	var expectedRefs []git.Reference
	for i := 0; i < 1000; i++ {
		reference := git.Reference{
			Name:   git.ReferenceName(fmt.Sprintf("refs/head/test_%d", i)),
			Target: commitID.String(),
		}

		require.NoError(t, updater.Create(reference.Name, commitID))
		expectedRefs = append(expectedRefs, reference)
	}

	require.NoError(t, updater.Commit())

	refs, err := repo.GetReferences(ctx, "refs/")
	require.NoError(t, err)
	require.ElementsMatch(t, expectedRefs, refs)
}

func TestUpdater_contextCancellation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	childCtx, childCancel := context.WithCancel(ctx)

	cfg, repoProto, repoPath, _ := setupUpdater(t, ctx)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	updater, err := New(childCtx, repo)
	require.NoError(t, err)

	require.NoError(t, updater.Create("refs/heads/main", commitID))

	// Force the update-ref process to terminate early by cancelling the context.
	childCancel()

	// We should see that committing the update fails now.
	require.Error(t, updater.Commit())

	// And the reference should not have been created.
	_, err = repo.ReadCommit(ctx, "refs/heads/main")
	require.Equal(t, localrepo.ErrObjectNotFound, err)
}

func TestUpdater_cancel(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath, updater := setupUpdater(t, ctx)

	if !gittest.GitSupportsStatusFlushing(t, ctx, cfg) {
		t.Skip("git does not support flushing yet, which is known to be flaky")
	}

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	// Queue the branch for deletion and lock it.
	require.NoError(t, updater.Delete(git.ReferenceName("refs/heads/main")))
	require.NoError(t, updater.Prepare())

	// Try to delete the same reference via a concurrent updater. This should not be allowed
	// because the reference is locked already.
	concurrentUpdater, err := New(ctx, repo)
	require.NoError(t, err)
	require.NoError(t, concurrentUpdater.Delete(git.ReferenceName("refs/heads/main")))
	err = concurrentUpdater.Commit()
	require.Error(t, err)
	require.Contains(t, err.Error(), "fatal: commit: cannot lock ref 'refs/heads/main'")

	// We now cancel the initial updater. Afterwards, it should be possible again to update the
	// ref because locks should have been released.
	require.NoError(t, updater.Cancel())

	concurrentUpdater, err = New(ctx, repo)
	require.NoError(t, err)
	require.NoError(t, concurrentUpdater.Delete(git.ReferenceName("refs/heads/main")))
	require.NoError(t, concurrentUpdater.Commit())
}

func TestUpdater_closingStdinAbortsChanges(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath, updater := setupUpdater(t, ctx)

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	require.NoError(t, updater.Create("refs/heads/main", commitID))

	// Note that we call `Wait()` on the command, not on the updater. This
	// circumvents our usual semantics of sending "commit" and thus
	// emulates that the command somehow terminates correctly without us
	// terminating it intentionally. Previous to our use of the "start"
	// verb, this would've caused the reference to be created...
	require.NoError(t, updater.cmd.Wait())

	// ... but as we now use explicit transactional behaviour, this is no
	// longer the case.
	_, err := repo.ReadCommit(ctx, "refs/heads/main")
	require.Equal(t, localrepo.ErrObjectNotFound, err, "expected 'not found' error got %v", err)
}

func TestUpdater_capturesStderr(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, _, _, updater := setupUpdater(t, ctx)

	newValue := git.ObjectID(strings.Repeat("1", gittest.DefaultObjectHash.EncodedLen()))
	oldValue := gittest.DefaultObjectHash.ZeroOID

	require.NoError(t, updater.Update("refs/heads/main", newValue, oldValue))

	var expectedErr string
	if gittest.GitSupportsStatusFlushing(t, ctx, cfg) {
		expectedErr = fmt.Sprintf("state update to \"commit\" failed: EOF, stderr: \"fatal: commit: cannot update ref '%[1]s': "+
			"trying to write ref '%[1]s' with nonexistent object %[2]s\\n\"", "refs/heads/main", newValue)
	} else {
		expectedErr = fmt.Sprintf("git update-ref: exit status 128, stderr: "+
			"\"fatal: commit: cannot update ref '%[1]s': "+
			"trying to write ref '%[1]s' with nonexistent object %[2]s\\n\"", "refs/heads/main", newValue)
	}

	err := updater.Commit()
	require.NotNil(t, err)
	require.Equal(t, err.Error(), expectedErr)
}
