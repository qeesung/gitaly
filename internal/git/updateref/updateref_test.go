package updateref

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func setupUpdater(tb testing.TB, ctx context.Context) (config.Cfg, *localrepo.Repo, string, *Updater) {
	tb.Helper()

	cfg := testcfg.Build(tb)

	repoProto, repoPath := gittest.CreateRepository(tb, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(tb, cfg, repoProto, git.WithSkipHooks())

	updater, err := New(ctx, repo)
	require.NoError(tb, err)
	tb.Cleanup(func() {
		// This is just to clean up so we ignore the error here. Cancel may or may not
		// return an error depending on the test.
		_ = updater.Close()
	})

	return cfg, repo, repoPath, updater
}

func TestUpdater_create(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, _, repoPath, updater := setupUpdater(t, ctx)
	defer testhelper.MustClose(t, updater)

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	// Create the first branch.
	require.NoError(t, updater.Start())
	require.NoError(t, updater.Create("refs/heads/_create", commitID))
	require.NoError(t, updater.Commit())

	// Verify that the reference was created as expected and that it points to the correct
	// commit.
	require.Equal(t, gittest.ResolveRevision(t, cfg, repoPath, "refs/heads/_create"), commitID)
}

func TestUpdater_referenceAlreadyExists(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, _, repoPath, updater := setupUpdater(t, ctx)
	expectedErr := ReferenceAlreadyExistsError{ReferenceName: "refs/heads/main"}
	defer func() { require.Equal(t, expectedErr, updater.Close()) }()

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	// Create the first branch.
	require.NoError(t, updater.Start())
	require.NoError(t, updater.Create("refs/heads/main", commitID))
	require.NoError(t, updater.Commit())

	// Attempt to create the same branch.
	require.NoError(t, updater.Start())
	require.NoError(t, updater.Create("refs/heads/main", commitID))
	require.Equal(t, expectedErr, updater.Commit())
}

func TestUpdater_nonCommitObject(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc          string
		referenceName git.ReferenceName
		expectedError error
	}{
		{
			desc:          "non-branch",
			referenceName: "refs/tags/v1.0.0",
		},
		{
			desc:          "branch",
			referenceName: "refs/heads/main",
			expectedError: NonCommitObjectError{
				ReferenceName: "refs/heads/main",
				ObjectID:      gittest.DefaultObjectHash.EmptyTreeOID.String(),
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			for _, method := range []struct {
				desc   string
				finish func(*Updater) error
			}{
				{desc: "prepare", finish: func(updater *Updater) error { return updater.Prepare() }},
				{desc: "commit", finish: func(updater *Updater) error { return updater.Commit() }},
			} {
				t.Run(method.desc, func(t *testing.T) {
					_, _, _, updater := setupUpdater(t, ctx)

					require.NoError(t, updater.Start())
					require.NoError(t, updater.Create(tc.referenceName, gittest.DefaultObjectHash.EmptyTreeOID))

					require.Equal(t, tc.expectedError, method.finish(updater))
				})
			}
		})
	}
}

// TestUpdater_properErrorOnWriteFailure tests that the Updater returns a well-typed error from
// stderr even if the update-ref process closes by itself due to an error while processing the
// transaction.
func TestUpdater_properErrorOnWriteFailure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	const referenceName = "/.invalid-reference"
	for _, tc := range []struct {
		desc   string
		method func(*Updater, git.ObjectID) error
	}{
		{
			desc: "create",
			method: func(updater *Updater, commitOID git.ObjectID) error {
				return updater.Create(referenceName, commitOID)
			},
		},
		{
			desc: "update",
			method: func(updater *Updater, commitOID git.ObjectID) error {
				return updater.Update(referenceName, commitOID, gittest.DefaultObjectHash.ZeroOID)
			},
		},
		{
			desc: "delete",
			method: func(updater *Updater, commitOID git.ObjectID) error {
				return updater.Delete(referenceName)
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, _, repoPath, updater := setupUpdater(t, ctx)

			expectedErr := InvalidReferenceFormatError{ReferenceName: referenceName}
			defer func() { require.Equal(t, expectedErr, updater.Close()) }()

			commitID := gittest.WriteCommit(t, cfg, repoPath)

			require.NoError(t, updater.Start())

			for {
				err := tc.method(updater, commitID)
				if err == nil {
					// If the call didn't error, then git didn't manage to process the
					// invalid reference and shutdown. Keep writing until the process has
					// exited due to the invalid reference name.
					continue
				}

				require.Equal(t, expectedErr, err)
				break
			}
		})
	}
}

func TestUpdater_nonExistentObject(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc   string
		finish func(*Updater) error
	}{
		{desc: "prepare", finish: func(updater *Updater) error { return updater.Prepare() }},
		{desc: "commit", finish: func(updater *Updater) error { return updater.Commit() }},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, repo, _, updater := setupUpdater(t, ctx)

			objectHash, err := repo.ObjectHash(ctx)
			require.NoError(t, err)

			hasher := objectHash.Hash()
			_, err = hasher.Write([]byte("content does not matter"))
			require.NoError(t, err)
			nonExistentOID, err := objectHash.FromHex(hex.EncodeToString(hasher.Sum(nil)))
			require.NoError(t, err)

			require.NoError(t, updater.Start())
			require.NoError(t, updater.Create("refs/heads/main", nonExistentOID))
			require.Equal(t,
				NonExistentObjectError{
					ReferenceName: "refs/heads/main",
					ObjectID:      nonExistentOID.String(),
				},
				tc.finish(updater),
			)
		})
	}
}

func TestUpdater_fileDirectoryConflict(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc            string
		firstReference  git.ReferenceName
		secondReference git.ReferenceName
	}{
		{
			desc:            "directory-file",
			firstReference:  "refs/heads/directory",
			secondReference: "refs/heads/directory/file",
		},
		{
			desc:            "file-directory",
			firstReference:  "refs/heads/directory/file",
			secondReference: "refs/heads/directory",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			for _, method := range []struct {
				desc   string
				finish func(*Updater) error
			}{
				{desc: "prepare", finish: func(updater *Updater) error { return updater.Prepare() }},
				{desc: "commit", finish: func(updater *Updater) error { return updater.Commit() }},
			} {
				t.Run(method.desc, func(t *testing.T) {
					t.Run("different transaction", func(t *testing.T) {
						cfg, _, repoPath, updater := setupUpdater(t, ctx)

						expectedErr := FileDirectoryConflictError{
							ExistingReferenceName:    tc.firstReference.String(),
							ConflictingReferenceName: tc.secondReference.String(),
						}

						defer func() { require.Equal(t, expectedErr, updater.Close()) }()

						commitID := gittest.WriteCommit(t, cfg, repoPath)

						require.NoError(t, updater.Start())
						require.NoError(t, updater.Create(tc.firstReference, commitID))
						require.NoError(t, updater.Commit())

						require.NoError(t, updater.Start())
						require.NoError(t, updater.Create(tc.secondReference, commitID))

						require.Equal(t, expectedErr, method.finish(updater))
					})

					t.Run("same transaction", func(t *testing.T) {
						cfg, _, repoPath, updater := setupUpdater(t, ctx)

						expectedErr := InTransactionConflictError{
							FirstReferenceName:  tc.firstReference.String(),
							SecondReferenceName: tc.secondReference.String(),
						}
						defer func() { require.Equal(t, expectedErr, updater.Close()) }()

						commitID := gittest.WriteCommit(t, cfg, repoPath)

						require.NoError(t, updater.Start())
						require.NoError(t, updater.Create(tc.firstReference, commitID))
						require.NoError(t, updater.Create(tc.secondReference, commitID))

						require.Equal(t, expectedErr, method.finish(updater))
					})
				})
			}
		})
	}
}

func TestUpdater_invalidStateTransitions(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc        string
		expectedErr error
		perform     func(*testing.T, *Updater) error
	}{
		{
			desc: "update before starting",
			perform: func(t *testing.T, u *Updater) error {
				return u.Update("", "", "")
			},
			expectedErr: invalidStateTransitionError{expected: stateStarted, actual: stateIdle},
		},
		{
			desc: "create before starting",
			perform: func(t *testing.T, u *Updater) error {
				return u.Create("", "")
			},
			expectedErr: invalidStateTransitionError{expected: stateStarted, actual: stateIdle},
		},
		{
			desc: "delete before starting",
			perform: func(t *testing.T, u *Updater) error {
				return u.Delete("")
			},
			expectedErr: invalidStateTransitionError{expected: stateStarted, actual: stateIdle},
		},
		{
			desc: "prepare before starting",
			perform: func(t *testing.T, u *Updater) error {
				return u.Prepare()
			},
			expectedErr: invalidStateTransitionError{expected: stateStarted, actual: stateIdle},
		},
		{
			desc: "preparing before starting",
			perform: func(t *testing.T, u *Updater) error {
				return u.Prepare()
			},
			expectedErr: invalidStateTransitionError{expected: stateStarted, actual: stateIdle},
		},
		{
			desc: "preparing when prepared",
			perform: func(t *testing.T, u *Updater) error {
				require.NoError(t, u.Start())
				require.NoError(t, u.Prepare())
				return u.Prepare()
			},
			expectedErr: invalidStateTransitionError{expected: stateStarted, actual: statePrepared},
		},
		{
			desc: "starting when started",
			perform: func(t *testing.T, u *Updater) error {
				require.NoError(t, u.Start())
				return u.Start()
			},
			expectedErr: invalidStateTransitionError{expected: stateIdle, actual: stateStarted},
		},
		{
			desc: "starting when prepared",
			perform: func(t *testing.T, u *Updater) error {
				require.NoError(t, u.Start())
				require.NoError(t, u.Prepare())
				return u.Start()
			},
			expectedErr: invalidStateTransitionError{expected: stateIdle, actual: statePrepared},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, _, repoPath, updater := setupUpdater(t, ctx)
			defer func() { require.Equal(t, tc.expectedErr, updater.Close()) }()

			require.Equal(t, tc.expectedErr, tc.perform(t, updater))

			// Verify no references were created.
			require.Empty(t, gittest.GetReferences(t, cfg, repoPath))
		})
	}
}

func TestUpdater_allMethodsReturnSameError(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	_, _, _, updater := setupUpdater(t, ctx)

	expectedErr := invalidStateTransitionError{expected: stateStarted, actual: stateIdle}
	defer func() { require.Equal(t, expectedErr, updater.Close()) }()

	require.Equal(t, expectedErr, updater.Update("", "", ""))
	require.Equal(t, expectedErr, updater.Create("", ""))
	require.Equal(t, expectedErr, updater.Delete(""))
	require.Equal(t, expectedErr, updater.Prepare())
	require.Equal(t, expectedErr, updater.Commit())
	require.Equal(t, expectedErr, updater.Start())
	require.Equal(t, expectedErr, updater.Close())
}

func TestUpdater_update(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, _, repoPath, updater := setupUpdater(t, ctx)

	// The updater cancel should fail at the end of the test as the final operation is an error,
	// which results in closing the updater.

	var expectedErr error
	defer func() { require.Equal(t, expectedErr, updater.Close()) }()

	oldCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
	newCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(oldCommitID))
	otherCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("other"))

	// Check that we can force-update the reference when we don't give an old object ID.
	require.NoError(t, updater.Start())
	require.NoError(t, updater.Update("refs/heads/main", newCommitID, ""))
	require.NoError(t, updater.Commit())
	require.Equal(t, gittest.ResolveRevision(t, cfg, repoPath, "refs/heads/main"), newCommitID)

	// Check that we can update with safety guards when giving an old commit ID.
	require.NoError(t, updater.Start())
	require.NoError(t, updater.Update("refs/heads/main", oldCommitID, newCommitID))
	require.NoError(t, updater.Commit())
	require.Equal(t, gittest.ResolveRevision(t, cfg, repoPath, "refs/heads/main"), oldCommitID)

	// And finally assert that we fail to update the reference in case we're trying to update
	// when the old commit ID doesn't match.
	require.NoError(t, updater.Start())
	require.NoError(t, updater.Update("refs/heads/main", newCommitID, otherCommitID))

	expectedErr = MismatchingStateError{
		ReferenceName:    "refs/heads/main",
		ExpectedObjectID: otherCommitID.String(),
		ActualObjectID:   oldCommitID.String(),
	}
	require.Equal(t, expectedErr, updater.Commit())
	require.Equal(t, expectedErr, updater.Start())

	require.Equal(t, gittest.ResolveRevision(t, cfg, repoPath, "refs/heads/main"), oldCommitID)
}

func TestUpdater_delete(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath, updater := setupUpdater(t, ctx)
	defer testhelper.MustClose(t, updater)

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	require.NoError(t, updater.Start())
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

	expectedErr := invalidStateTransitionError{expected: stateStarted, actual: statePrepared}
	defer func() { require.Equal(t, expectedErr, updater.Close()) }()

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	require.NoError(t, updater.Start())
	require.NoError(t, updater.Update("refs/heads/feature", commitID, ""))
	require.NoError(t, updater.Prepare())

	require.Equal(t, invalidStateTransitionError{expected: stateStarted, actual: statePrepared}, updater.Update("refs/heads/feature", commitID, ""))
	require.Equal(t, invalidStateTransitionError{expected: stateStarted, actual: statePrepared}, updater.Commit())
}

func TestUpdater_invalidReferenceName(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto, git.WithSkipHooks())
	commitID := gittest.WriteCommit(t, cfg, repoPath)

	const referenceName = `refs/heads\master`
	updater, err := New(ctx, repo)
	require.NoError(t, err)
	defer func() { require.Equal(t, InvalidReferenceFormatError{ReferenceName: referenceName}, updater.Close()) }()

	require.NoError(t, updater.Start())
	require.NoError(t, updater.Update(referenceName, commitID, ""))
	require.Equal(t, InvalidReferenceFormatError{ReferenceName: referenceName}, updater.Prepare())
}

func TestUpdater_concurrentLocking(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto, git.WithSkipHooks())
	commitID := gittest.WriteCommit(t, cfg, repoPath)

	for _, tc := range []struct {
		desc string
		lock func(*Updater) error
	}{
		{
			desc: "prepare",
			lock: func(u *Updater) error {
				return u.Prepare()
			},
		},
		{
			desc: "commit",
			lock: func(u *Updater) error {
				return u.Commit()
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ref := git.ReferenceName("refs/heads/" + tc.desc)

			// Create the first updater that prepares the reference transaction so that the reference
			// we're about to update is locked.
			firstUpdater, err := New(ctx, repo)
			require.NoError(t, err)
			require.NoError(t, firstUpdater.Start())
			require.NoError(t, firstUpdater.Update(ref, commitID, ""))
			require.NoError(t, firstUpdater.Prepare())

			// Now we create a second updater that tries to update the same reference.
			secondUpdater, err := New(ctx, repo)
			require.NoError(t, err)
			require.NoError(t, secondUpdater.Start())
			require.NoError(t, secondUpdater.Update(ref, commitID, ""))

			if gittest.DefaultReferenceBackend == git.ReferenceBackendReftables {
				// Reftable locks are on an entire table instead of per reference, so
				// git doesn't output the name of the individual ref.
				require.Equal(t, AlreadyLockedError{}, tc.lock(secondUpdater))
			} else {
				// Try locking the reference via the second updater. This should fail now because the reference
				// is locked already.
				require.Equal(t, AlreadyLockedError{
					ReferenceName: string(ref),
				}, tc.lock(secondUpdater))
			}

			// Whereas committing the first transaction should succeed.
			require.NoError(t, firstUpdater.Commit())
		})
	}
}

func TestUpdater_bulkOperation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, _, repoPath, updater := setupUpdater(t, ctx)
	defer testhelper.MustClose(t, updater)

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	var expectedRefs []git.Reference
	for i := 0; i < 2; i++ {
		require.NoError(t, updater.Start())

		for j := 0; j < 2; j++ {
			reference := git.Reference{
				Name:   git.ReferenceName(fmt.Sprintf("refs/head/test_%d_%d", j, i)),
				Target: commitID.String(),
			}

			require.NoError(t, updater.Create(reference.Name, commitID))
			expectedRefs = append(expectedRefs, reference)
		}

		require.NoError(t, updater.Commit())
	}

	require.ElementsMatch(t, expectedRefs, gittest.GetReferences(t, cfg, repoPath))
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

	require.NoError(t, updater.Start())

	// Force the update-ref process to terminate early by cancelling the context.
	childCancel()

	for i := 0; true; i++ {
		// Context cancellation happens asynchronously. Keep running commands until
		// the context cancellation error is returned.
		err := updater.Create(git.ReferenceName(fmt.Sprintf("refs/heads/ref-%d", i)), commitID)
		if err != nil {
			require.ErrorIs(t, err, context.Canceled)
			return
		}
	}
}

func TestUpdater_cancel(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath, firstUpdater := setupUpdater(t, ctx)
	defer func() { require.Equal(t, errClosed, firstUpdater.Close()) }()

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	// Queue the branch for deletion and lock it.
	require.NoError(t, firstUpdater.Start())
	require.NoError(t, firstUpdater.Delete(git.ReferenceName("refs/heads/main")))
	require.NoError(t, firstUpdater.Prepare())

	// Try to delete the same reference via a concurrent updater. This should not be allowed
	// because the reference is locked already.
	failingUpdater, err := New(ctx, repo)
	require.NoError(t, err)

	expectedErr := AlreadyLockedError{
		ReferenceName: "refs/heads/main",
	}

	// Reftable locks are on an entire table instead of per reference, so
	// git doesn't output the name of the individual ref.
	if gittest.DefaultReferenceBackend == git.ReferenceBackendReftables {
		expectedErr.ReferenceName = ""
	}

	defer func() { require.Equal(t, expectedErr, failingUpdater.Close()) }()

	require.NoError(t, failingUpdater.Start())
	require.NoError(t, failingUpdater.Delete(git.ReferenceName("refs/heads/main")))
	require.Equal(t, expectedErr, failingUpdater.Commit())

	// We now cancel the initial updater. Afterwards, it should be possible again to update the
	// ref because locks should have been released.
	require.NoError(t, firstUpdater.Close())

	secondUpdater, err := New(ctx, repo)
	require.NoError(t, err)
	defer testhelper.MustClose(t, secondUpdater)

	require.NoError(t, secondUpdater.Start())
	require.NoError(t, secondUpdater.Delete(git.ReferenceName("refs/heads/main")))
	require.NoError(t, secondUpdater.Commit())
}

func TestUpdater_closingStdinAbortsChanges(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath, updater := setupUpdater(t, ctx)
	defer testhelper.MustClose(t, updater)

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	require.NoError(t, updater.Start())
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

	_, _, _, updater := setupUpdater(t, ctx)
	var expectedErr error
	defer func() { require.Equal(t, expectedErr, updater.Close()) }()

	require.NoError(t, updater.Start())
	// fail the process by writing bad input
	_, err := updater.cmd.Write([]byte("garbage input"))
	require.NoError(t, err)

	expectedErr = structerr.New("%w", updater.cmd.Wait()).WithMetadataItems(
		structerr.MetadataItem{Key: "stderr", Value: "fatal: unknown command: garbage input\n"},
		structerr.MetadataItem{Key: "close_error", Value: &fs.PathError{
			Op:   "write",
			Path: "|1",
			Err:  errors.New("file already closed"),
		}},
	)
	require.Equal(t, expectedErr, updater.Commit())
}

func TestUpdater_packRefsLocked(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc      string
		runMethod func(u *Updater) error
	}{
		{desc: "commit", runMethod: func(u *Updater) error { return u.Commit() }},
		{desc: "prepare", runMethod: func(u *Updater) error { return u.Prepare() }},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			files := []string{"packed-refs.lock", "packed-refs.new"}
			expectedErr := ErrPackedRefsLocked

			if gittest.DefaultReferenceBackend == git.ReferenceBackendReftables {
				files = []string{"reftable/tables.list.lock"}
				expectedErr = AlreadyLockedError{}
			}

			for _, lockFile := range files {
				lockFile := lockFile
				t.Run(lockFile, func(t *testing.T) {
					t.Parallel()

					ctx := testhelper.Context(t)

					cfg, _, repoPath, updater := setupUpdater(t, ctx)
					defer func() { require.Equal(t, expectedErr, updater.Close()) }()

					// Write a reference and pack it. Packing for the reftable backend
					// means table compaction (which would also run automatically based
					// on heuristics).
					gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
					gittest.Exec(t, cfg, "-C", repoPath, "pack-refs", "--all")

					// Write a lock file file so we can assert a lock related error is raised.
					require.NoError(t, os.WriteFile(filepath.Join(repoPath, lockFile), nil, fs.ModePerm))

					require.NoError(t, updater.Start())
					require.NoError(t, updater.Delete("refs/heads/main"))
					require.Equal(t, expectedErr, updater.Commit())
				})
			}
		})
	}
}

func BenchmarkUpdater(b *testing.B) {
	ctx := testhelper.Context(b)

	getReferenceName := func(id int) git.ReferenceName {
		// Pad the numbers so the references are sorted when they are written
		// out into a packed-refs file.
		return git.ReferenceName(fmt.Sprintf("refs/heads/branch-%09d", id))
	}

	createReferences := func(tb testing.TB, repository git.RepositoryExecutor, referenceCount int, commitOID git.ObjectID) {
		tb.Helper()

		updater, err := New(ctx, repository)
		require.NoError(tb, err)
		defer testhelper.MustClose(tb, updater)

		require.NoError(tb, updater.Start())
		for i := 0; i < referenceCount; i++ {
			require.NoError(b, updater.Create(getReferenceName(i), commitOID))
		}
		require.NoError(tb, updater.Commit())
	}

	b.Run("update", func(b *testing.B) {
		for _, tc := range []struct {
			// transactionSize determines how many references are updated in a single reference
			// transaction.
			transactionSize int
		}{
			{transactionSize: 1},
			{transactionSize: 10},
			{transactionSize: 100},
		} {
			b.Run(fmt.Sprintf("transaction size %d", tc.transactionSize), func(b *testing.B) {
				ctx := testhelper.Context(b)

				cfg, repo, repoPath, updater := setupUpdater(b, ctx)
				defer testhelper.MustClose(b, updater)

				commitOID1 := gittest.WriteCommit(b, cfg, repoPath)
				commitOID2 := gittest.WriteCommit(b, cfg, repoPath, gittest.WithParents(commitOID1))

				createReferences(b, repo, tc.transactionSize, commitOID1)

				old, new := commitOID1, commitOID2

				b.ReportAllocs()
				b.ResetTimer()
				began := time.Now()
				for n := 0; n < b.N; n++ {
					require.NoError(b, updater.Start())
					for i := 0; i < tc.transactionSize; i++ {
						require.NoError(b, updater.Update(getReferenceName(i), new, old))
					}
					require.NoError(b, updater.Commit())

					old, new = new, old
				}

				elapsed := time.Since(began)
				b.ReportMetric(float64(b.N*tc.transactionSize)/elapsed.Seconds(), "reference_updates/s")
			})
		}
	})

	b.Run("delete", func(b *testing.B) {
		for _, tc := range []struct {
			// referenceCount controls the number of references in the repository. This becomes important
			// when the references are packed as it impacts the size of the packed-refs file.
			referenceCount int
			// referencesPacked controls whether the references are packed or loose.
			referencesPacked bool
			// transactionSize determines how many references are deleted in one reference transaction.
			transactionSize int
		}{
			{referenceCount: 100, referencesPacked: false, transactionSize: 1},
			{referenceCount: 100, referencesPacked: true, transactionSize: 1},
			{referenceCount: 100, referencesPacked: false, transactionSize: 10},
			{referenceCount: 100, referencesPacked: true, transactionSize: 10},
			{referenceCount: 100, referencesPacked: false, transactionSize: 100},
			{referenceCount: 100, referencesPacked: true, transactionSize: 100},
			{referenceCount: 1_000_000, referencesPacked: true, transactionSize: 1},
			{referenceCount: 1_000_000, referencesPacked: true, transactionSize: 10},
			{referenceCount: 1_000_000, referencesPacked: true, transactionSize: 100},
			{referenceCount: 1_000_000, referencesPacked: true, transactionSize: 1000},
		} {
			desc := fmt.Sprintf("reference count %d/references packed %v/transaction size %d",
				tc.referenceCount, tc.referencesPacked, tc.transactionSize,
			)

			b.Run(desc, func(b *testing.B) {
				require.GreaterOrEqual(b, tc.referenceCount, tc.transactionSize,
					"Reference count must be greater than or equal to transaction size to have enough references to delete",
				)

				ctx := testhelper.Context(b)

				cfg, repo, repoPath, updater := setupUpdater(b, ctx)
				defer testhelper.MustClose(b, updater)

				commitOID := gittest.WriteCommit(b, cfg, repoPath)

				// createPackedReferences writes out a packed-refs file into the repository. We do this
				// manually as creating millions of references with update-ref and packing them is slow.
				// It overwrites a possible existing file.
				createPackedReferences := func(tb testing.TB) {
					tb.Helper()

					packedRefs, err := os.Create(filepath.Join(repoPath, "packed-refs"))
					require.NoError(tb, err)

					defer testhelper.MustClose(tb, packedRefs)

					_, err = fmt.Fprintf(packedRefs, "# pack-refs with: peeled fully-peeled sorted\n")
					require.NoError(tb, err)

					for i := 0; i < tc.referenceCount; i++ {
						_, err := fmt.Fprintf(packedRefs, "%s %s\n", commitOID, getReferenceName(i))
						require.NoError(tb, err)
					}
				}

				if tc.referencesPacked {
					// The pack file is always fully written out.
					createPackedReferences(b)
				} else {
					// Create all of the references initially.
					createReferences(b, repo, tc.referenceCount, commitOID)
				}

				var elapsed time.Duration
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					began := time.Now()

					require.NoError(b, updater.Start())
					for i := 0; i < tc.transactionSize; i++ {
						require.NoError(b, updater.Delete(getReferenceName(i)))
					}
					require.NoError(b, updater.Commit())

					b.StopTimer()

					elapsed += time.Since(began)
					// restore the deleted references
					if tc.referencesPacked {
						createPackedReferences(b)
					} else {
						// Only create the deleted references.
						createReferences(b, repo, tc.transactionSize, commitOID)
					}

					b.StartTimer()
				}

				b.ReportMetric(float64(b.N*tc.transactionSize)/elapsed.Seconds(), "reference_deletions/s")
			})
		}
	})
}

func TestUpdater_multipleUpdateError(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, _, repoPath, updater := setupUpdater(t, ctx)
	defer func() { require.Equal(t, MultipleUpdatesError{ReferenceName: "refs/heads/test_a"}, updater.Close()) }()

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("test_a"))

	require.NoError(t, updater.Start())
	require.NoError(t, updater.Delete("refs/heads/test_a"))
	require.NoError(t, updater.Create("refs/heads/test_a", commitID))
	require.Equal(t, MultipleUpdatesError{ReferenceName: "refs/heads/test_a"}, updater.Commit())
}
