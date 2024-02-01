package storagemgr

import (
	"archive/tar"
	"bytes"
	"container/list"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// errSimulatedCrash is used in the tests to simulate a crash at a certain point during
// TransactionManager.Run execution.
var errSimulatedCrash = errors.New("simulated crash")

func validCustomHooks(tb testing.TB) []byte {
	tb.Helper()

	var hooks bytes.Buffer
	writer := tar.NewWriter(&hooks)
	require.NoError(tb, writer.WriteHeader(&tar.Header{
		Name: "custom_hooks/pre-receive",
		Size: int64(len("hook content")),
		Mode: int64(fs.ModePerm),
	}))
	_, err := writer.Write([]byte("hook content"))
	require.NoError(tb, err)

	require.NoError(tb, writer.WriteHeader(&tar.Header{
		Name: "custom_hooks/private-dir/",
		Mode: int64(perm.PrivateDir),
	}))

	require.NoError(tb, writer.WriteHeader(&tar.Header{
		Name: "custom_hooks/private-dir/private-file",
		Size: int64(len("private content")),
		Mode: int64(perm.PrivateFile),
	}))
	_, err = writer.Write([]byte("private content"))
	require.NoError(tb, err)

	require.NoError(tb, writer.WriteHeader(&tar.Header{Name: "ignored_file"}))
	require.NoError(tb, writer.WriteHeader(&tar.Header{Name: "ignored_dir/ignored_file"}))

	require.NoError(tb, writer.Close())
	return hooks.Bytes()
}

// writePack writes a pack file and its index into the destination.
func writePack(tb testing.TB, cfg config.Cfg, packFile []byte, destinationPack string) {
	tb.Helper()

	require.NoError(tb, os.WriteFile(destinationPack, packFile, fs.ModePerm))
	gittest.ExecOpts(tb, cfg,
		gittest.ExecConfig{Stdin: bytes.NewReader(packFile)},
		"index-pack", "--object-format="+gittest.DefaultObjectHash.Format, destinationPack,
	)
}

// anyDirectoryEntry returns a DirectoryEntry that checks for the existence of a file. The content will be asserted in
// the later state.
func anyDirectoryEntry(cfg config.Cfg) testhelper.DirectoryEntry {
	return testhelper.DirectoryEntry{
		Mode:         perm.SharedReadOnlyFile,
		Content:      "",
		ParseContent: func(testing.TB, string, []byte) any { return "" },
	}
}

// anyDirectoryEntryWithPerm returns a DirectoryEntry that checks for the existence of a file having a particular perm.
// The content will be asserted in the later state.
func anyDirectoryEntryWithPerm(cfg config.Cfg, perm os.FileMode) testhelper.DirectoryEntry {
	return testhelper.DirectoryEntry{
		Mode:         perm,
		Content:      "",
		ParseContent: func(testing.TB, string, []byte) any { return "" },
	}
}

// packFileDirectoryEntry returns a DirectoryEntry that parses content as a pack file and asserts that the
// set of objects in the pack file matches the expected objects.
func packFileDirectoryEntry(cfg config.Cfg, expectedObjects []git.ObjectID) testhelper.DirectoryEntry {
	sortObjects(expectedObjects)

	return testhelper.DirectoryEntry{
		Mode:    perm.SharedReadOnlyFile,
		Content: expectedObjects,
		ParseContent: func(tb testing.TB, path string, content []byte) any {
			tb.Helper()

			tempDir := tb.TempDir()
			// Initialize a temporary repository where to write the pack. The cat file invocation for listing
			// the objects needs to run within a repository, and it would otherwise use the developer's repository.
			// If the object format doesn't match with the pack files in the test, things fail.
			gittest.Exec(tb, cfg, "init", "--object-format="+gittest.DefaultObjectHash.Format, "--bare", tempDir)
			writePack(tb, cfg, content, filepath.Join(tempDir, "objects", "pack", "content.pack"))

			actualObjects := gittest.ListObjects(tb, cfg, tempDir)
			sortObjects(actualObjects)

			return actualObjects
		},
	}
}

// indexFileDirectoryEntry returns a DirectoryEntry that asserts the given pack file index is valid.
func indexFileDirectoryEntry(cfg config.Cfg) testhelper.DirectoryEntry {
	return testhelper.DirectoryEntry{
		Mode: perm.SharedReadOnlyFile,
		ParseContent: func(tb testing.TB, path string, content []byte) any {
			tb.Helper()

			// Verify the index is valid.
			gittest.Exec(tb, cfg, "verify-pack", "--object-format="+gittest.DefaultObjectHash.Format, "-v", path)
			// As we already verified the index is valid, we don't care about the actual contents.
			return nil
		},
	}
}

// reverseIndexFileDirectoryEntry returns a DirectoryEntry that asserts the given pack file reverse index is valid.
func reverseIndexFileDirectoryEntry(cfg config.Cfg) testhelper.DirectoryEntry {
	return testhelper.DirectoryEntry{
		Mode: perm.SharedReadOnlyFile,
		ParseContent: func(tb testing.TB, path string, content []byte) any {
			tb.Helper()

			// We cannot run git-verify-pack(1) directly against a reverse index file, so we validate the
			// header to fail fast when possible. If this passes but the '.rev' file is invalid, the
			// '.idx' check will fail.
			buf := make([]byte, 4)
			f, err := os.Open(path)
			require.NoError(tb, err)
			defer testhelper.MustClose(tb, f)

			_, err = f.Read(buf)
			require.NoError(tb, err)
			require.Equal(tb, []byte{'R', 'I', 'D', 'X'}, buf)

			return nil
		},
	}
}

func setupTest(t *testing.T, ctx context.Context, testPartitionID partitionID, relativePath string) testTransactionSetup {
	t.Helper()

	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           relativePath,
	})

	firstCommitOID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents())
	secondCommitOID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(firstCommitOID))
	thirdCommitOID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(secondCommitOID))
	divergingCommitOID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(firstCommitOID), gittest.WithMessage("diverging commit"))

	cmdFactory := gittest.NewCommandFactory(t, cfg)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	logger := testhelper.NewLogger(t)
	locator := config.NewLocator(cfg)
	localRepo := localrepo.New(
		logger,
		locator,
		cmdFactory,
		catfileCache,
		repo,
	)

	objectHash, err := localRepo.ObjectHash(ctx)
	require.NoError(t, err)

	hasher := objectHash.Hash()
	_, err = hasher.Write([]byte("content does not matter"))
	require.NoError(t, err)
	nonExistentOID, err := objectHash.FromHex(hex.EncodeToString(hasher.Sum(nil)))
	require.NoError(t, err)

	packCommit := func(oid git.ObjectID) []byte {
		t.Helper()

		var pack bytes.Buffer
		require.NoError(t,
			localRepo.PackObjects(ctx, strings.NewReader(oid.String()), &pack),
		)

		return pack.Bytes()
	}

	return testTransactionSetup{
		PartitionID:       testPartitionID,
		RelativePath:      relativePath,
		RepositoryPath:    repoPath,
		Repo:              localRepo,
		Config:            cfg,
		ObjectHash:        objectHash,
		CommandFactory:    cmdFactory,
		RepositoryFactory: localrepo.NewFactory(logger, locator, cmdFactory, catfileCache),
		NonExistentOID:    nonExistentOID,
		Commits: testTransactionCommits{
			First: testTransactionCommit{
				OID:  firstCommitOID,
				Pack: packCommit(firstCommitOID),
			},
			Second: testTransactionCommit{
				OID:  secondCommitOID,
				Pack: packCommit(secondCommitOID),
			},
			Third: testTransactionCommit{
				OID:  thirdCommitOID,
				Pack: packCommit(thirdCommitOID),
			},
			Diverging: testTransactionCommit{
				OID:  divergingCommitOID,
				Pack: packCommit(divergingCommitOID),
			},
		},
	}
}

func TestTransactionManager(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	// testPartitionID is the partition ID used in the tests for the TransactionManager.
	const testPartitionID partitionID = 1

	// A clean repository is setup for each test. We build a setup ahead of the tests here once to
	// get deterministic commit IDs, relative path and object hash we can use to build the declarative
	// test cases.
	relativePath := gittest.NewRepositoryName(t)
	setup := setupTest(t, ctx, testPartitionID, relativePath)

	var testCases []transactionTestCase
	subTests := [][]transactionTestCase{
		generateCommonTests(t, ctx, setup),
		generateCommittedEntriesTests(t, setup),
		generateInvalidReferencesTests(t, setup),
		generateModifyReferencesTests(t, setup),
		generateCreateRepositoryTests(t, setup),
		generateDeleteRepositoryTests(t, setup),
		generateDefaultBranchTests(t, setup),
		generateAlternateTests(t, setup),
		generateCustomHooksTests(t, setup),
		generateHousekeepingPackRefsTests(t, ctx, testPartitionID, relativePath),
		generateHousekeepingRepackingStrategyTests(t, ctx, testPartitionID, relativePath),
		generateHousekeepingRepackingConcurrentTests(t, ctx, setup),
	}
	for _, subCases := range subTests {
		testCases = append(testCases, subCases...)
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			// Setup the repository with the exact same state as what was used to build the test cases.
			var setup testTransactionSetup
			if tc.customSetup != nil {
				setup = tc.customSetup(t, ctx, testPartitionID, relativePath)
			} else {
				setup = setupTest(t, ctx, testPartitionID, relativePath)
			}
			runTransactionTest(t, ctx, tc, setup)
		})
	}
}

func generateCommonTests(t *testing.T, ctx context.Context, setup testTransactionSetup) []transactionTestCase {
	umask := testhelper.Umask()

	return []transactionTestCase{
		{
			desc: "begin returns if context is canceled before initialization",
			steps: steps{
				Begin{
					RelativePath: setup.RelativePath,
					Context: func() context.Context {
						ctx, cancel := context.WithCancel(ctx)
						cancel()
						return ctx
					}(),
					ExpectedError: context.Canceled,
				},
			},
			expectedState: StateAssertion{
				// Manager is not started up so no state is initialized.
				Directory: testhelper.DirectoryState{},
			},
		},
		{
			desc: "commit returns if transaction processing stops before admission",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				CloseManager{},
				Commit{
					ExpectedError: ErrTransactionProcessingStopped,
				},
			},
		},
		func() transactionTestCase {
			ctx, cancel := context.WithCancel(ctx)
			return transactionTestCase{
				desc: "commit returns if context is canceled after admission",
				steps: steps{
					StartManager{
						Hooks: testTransactionHooks{
							BeforeAppendLogEntry: func(hookCtx hookContext) {
								// Cancel the context used in Commit
								cancel()
							},
						},
					},
					Begin{
						RelativePath: setup.RelativePath,
					},
					Commit{
						Context: ctx,
						ReferenceUpdates: ReferenceUpdates{
							"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
						},
						ExpectedError: context.Canceled,
					},
				},
				expectedState: StateAssertion{
					Database: DatabaseState{
						string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
					},
					Repositories: RepositoryStates{
						setup.RelativePath: {
							DefaultBranch: "refs/heads/main",
							References: &ReferencesState{
								LooseReferences: map[git.ReferenceName]git.ObjectID{
									"refs/heads/main": setup.Commits.First.OID,
								},
							},
						},
					},
				},
			}
		}(),
		{
			desc: "commit returns if transaction processing stops before transaction acceptance",
			steps: steps{
				StartManager{
					Hooks: testTransactionHooks{
						BeforeAppendLogEntry: func(hookContext hookContext) { hookContext.closeManager() },
						// This ensures we are testing the context cancellation errors being unwrapped properly
						// to an ErrTransactionProcessingStopped instead of hitting the general case when
						// runDone is closed.
						WaitForTransactionsWhenClosing: true,
					},
				},
				Begin{
					RelativePath: setup.RelativePath,
				},
				CloseManager{},
				Commit{
					ExpectedError: ErrTransactionProcessingStopped,
				},
			},
		},
		{
			desc: "commit returns if transaction processing stops after transaction acceptance",
			steps: steps{
				StartManager{
					Hooks: testTransactionHooks{
						BeforeApplyLogEntry: func(hookCtx hookContext) {
							hookCtx.closeManager()
						},
					},
				},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					ExpectedError: ErrTransactionProcessingStopped,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyLogEntry(setup.PartitionID, 1)): &gitalypb.LogEntry{
						RelativePath: setup.RelativePath,
						ReferenceTransactions: []*gitalypb.LogEntry_ReferenceTransaction{
							{
								Changes: []*gitalypb.LogEntry_ReferenceTransaction_Change{
									{
										ReferenceName: []byte("refs/heads/main"),
										NewOid:        []byte(setup.Commits.First.OID),
									},
								},
							},
						},
					},
				},
			},
		},
		{
			desc: "read snapshots include committed data",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				RepositoryAssertion{
					TransactionID: 1,
					Repositories: RepositoryStates{
						setup.RelativePath: {
							DefaultBranch: "refs/heads/main",
							Objects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
								setup.Commits.Third.OID,
								setup.Commits.Diverging.OID,
							},
						},
					},
				},
				RepositoryAssertion{
					TransactionID: 2,
					Repositories: RepositoryStates{
						setup.RelativePath: {
							DefaultBranch: "refs/heads/main",
							Objects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
								setup.Commits.Third.OID,
								setup.Commits.Diverging.OID,
							},
						},
					},
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					CustomHooksUpdate: &CustomHooksUpdate{
						CustomHooksTAR: validCustomHooks(t),
					},
				},
				// Transaction 2 is isolated from the changes made by transaction 1. It does not see the
				// committed changes.
				RepositoryAssertion{
					TransactionID: 2,
					Repositories: RepositoryStates{
						setup.RelativePath: {
							DefaultBranch: "refs/heads/main",
							Objects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
								setup.Commits.Third.OID,
								setup.Commits.Diverging.OID,
							},
						},
					},
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				// Transaction 3 is should see the new changes as it began after transaction 1 was committed.
				RepositoryAssertion{
					TransactionID: 3,
					Repositories: RepositoryStates{
						setup.RelativePath: {
							DefaultBranch: "refs/heads/main",
							References: &ReferencesState{
								LooseReferences: map[git.ReferenceName]git.ObjectID{
									"refs/heads/main": setup.Commits.First.OID,
								},
							},
							Objects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
								setup.Commits.Third.OID,
								setup.Commits.Diverging.OID,
							},
							CustomHooks: testhelper.DirectoryState{
								"/": {Mode: fs.ModeDir | perm.PrivateDir},
								"/pre-receive": {
									Mode:    umask.Mask(fs.ModePerm),
									Content: []byte("hook content"),
								},
								"/private-dir":              {Mode: fs.ModeDir | perm.PrivateDir},
								"/private-dir/private-file": {Mode: umask.Mask(perm.PrivateFile), Content: []byte("private content")},
							},
						},
					},
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
				},
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				Rollback{
					TransactionID: 3,
				},
				Begin{
					TransactionID:       5,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.Third.OID},
					},
					CustomHooksUpdate: &CustomHooksUpdate{},
				},
				Begin{
					TransactionID:       6,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 3,
				},
				Rollback{
					TransactionID: 5,
				},
				Rollback{
					TransactionID: 6,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":    {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal": {Mode: fs.ModeDir | perm.PrivateDir},
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Third.OID,
							},
						},
						CustomHooks: testhelper.DirectoryState{
							"/": {Mode: fs.ModeDir | perm.PrivateDir},
						},
					},
				},
			},
		},
		{
			desc: "pack file includes referenced commit",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					ExpectedError: updateref.NonExistentObjectError{
						ReferenceName: "refs/heads/main",
						ObjectID:      setup.Commits.First.OID.String(),
					},
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.First.Pack},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					},
				},
			},
		},
		{
			desc: "pack file includes unreachable objects depended upon",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.First.Pack,
						setup.Commits.Second.Pack,
					},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				// Point main to the first commit so the second one is unreachable.
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.First.OID},
					},
				},
				AssertManager{},
				CloseManager{},
				StartManager{
					// Crash the manager before the third transaction is applied. This allows us to
					// prune before it is applied to ensure the pack file contains all necessary commits.
					Hooks: testTransactionHooks{
						BeforeApplyLogEntry: func(hookContext) {
							panic(errSimulatedCrash)
						},
					},
					ExpectedError: errSimulatedCrash,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Third.OID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.Third.Pack},
					ExpectedError:    ErrTransactionProcessingStopped,
				},
				AssertManager{
					ExpectedError: errSimulatedCrash,
				},
				// Prune so the unreachable commits have been removed prior to the third log entry being
				// applied.
				Prune{
					ExpectedObjects: []git.ObjectID{
						setup.ObjectHash.EmptyTreeOID,
						setup.Commits.First.OID,
					},
				},
				StartManager{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					),
					"/wal/3":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/3/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/3/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/3/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.Commits.Second.OID,
							setup.Commits.Third.OID,
						},
					),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Third.OID,
							},
						},
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
							setup.Commits.Third.OID,
						},
					},
				},
			},
		},
		{
			desc: "pack file reapplying works",
			steps: steps{
				Prune{},
				StartManager{
					Hooks: testTransactionHooks{
						BeforeStoreAppliedLSN: func(hookContext) {
							panic(errSimulatedCrash)
						},
					},
					ExpectedError: errSimulatedCrash,
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.First.Pack},
					ExpectedError:    ErrTransactionProcessingStopped,
				},
				AssertManager{
					ExpectedError: errSimulatedCrash,
				},
				StartManager{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					},
				},
			},
		},
		{
			desc: "pack file missing referenced commit",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.First.Pack},
					ExpectedError:    localrepo.BadObjectError{ObjectID: setup.Commits.Second.OID},
				},
			},
			expectedState: StateAssertion{
				Repositories: RepositoryStates{
					setup.RelativePath: {
						Objects: []git.ObjectID{},
					},
				},
			},
		},
		{
			desc: "pack file missing intermediate commit",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.First.Pack},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Third.OID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.Third.Pack},
					ExpectedError:    localrepo.ObjectReadError{ObjectID: setup.Commits.Second.OID},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
				},

				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					},
				},
			},
		},
		{
			desc: "pack file only",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID:    1,
					QuarantinedPacks: [][]byte{setup.Commits.First.Pack},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":    {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal": {Mode: fs.ModeDir | perm.PrivateDir},
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						Objects: []git.ObjectID{},
					},
				},
			},
		},
		{
			desc: "transaction includes an unreachable object with dependencies",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					QuarantinedPacks: [][]byte{
						setup.Commits.First.Pack,
						setup.Commits.Second.Pack,
						setup.Commits.Third.Pack,
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Second.OID},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					},
				},
			},
		},
		{
			desc: "transaction includes multiple unreachable objects",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					QuarantinedPacks: [][]byte{
						setup.Commits.First.Pack,
						setup.Commits.Second.Pack,
						setup.Commits.Diverging.Pack,
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Second.OID, setup.Commits.Diverging.OID},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
							setup.Commits.Diverging.OID,
						},
					),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
							setup.Commits.Diverging.OID,
						},
					},
				},
			},
		},
		{
			desc: "transaction includes a missing object",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					QuarantinedPacks: [][]byte{
						setup.Commits.First.Pack,
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Second.OID},
					ExpectedError: localrepo.BadObjectError{
						ObjectID: setup.Commits.Second.OID,
					},
				},
			},
			expectedState: StateAssertion{
				Repositories: RepositoryStates{
					setup.RelativePath: {
						Objects: []git.ObjectID{},
					},
				},
			},
		},
		{
			desc: "pack file with deletions",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.First.Pack},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.Second.Pack},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					},
				},
			},
		},
		{
			desc: "pack file applies with dependency concurrently deleted",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.First.Pack},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				AssertManager{},
				Prune{},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/dependant": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.Second.Pack},
					// The transaction fails to apply as we are not yet maintaining internal references
					// to the old tips of concurrently deleted references. This causes the prune step to
					// remove the object this the pack file depends on.
					//
					// For now, keep the test case to assert the behavior. We'll fix this in a later MR.
					ExpectedError: localrepo.ObjectReadError{
						ObjectID: setup.Commits.First.OID,
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						Objects: []git.ObjectID{},
					},
				},
			},
		},
		{
			desc: "pack files without log entries are cleaned up after a crash",
			steps: steps{
				StartManager{
					// The manager cleans up pack files if a committing fails. Since we can't
					// hard kill the manager and it will still run the deferred clean up functions,
					// we have to test the behavior by manually creating a stale pack here.
					//
					// The Manager starts up and we expect the pack file to be gone at the end of the test.
					ModifyStorage: func(_ testing.TB, _ config.Cfg, storagePath string) {
						packFilePath := packFilePath(walFilesPathForLSN(filepath.Join(storagePath, setup.RelativePath), 1))
						require.NoError(t, os.MkdirAll(filepath.Dir(packFilePath), perm.PrivateDir))
						require.NoError(t, os.WriteFile(
							packFilePath,
							[]byte("invalid pack"),
							perm.PrivateDir,
						))
					},
				},
			},
		},
		{
			desc: "non-existent repository is correctly handled",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RepositoryAssertion{
					TransactionID: 1,
					Repositories:  RepositoryStates{},
				},
				Rollback{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "failing initialization prevents transaction beginning",
			steps: steps{
				StartManager{
					Hooks: testTransactionHooks{
						BeforeReadAppliedLSN: func(hookContext) {
							// Raise a panic when the manager is about to read the applied log
							// index when initializing. In reality this would crash the server but
							// in tests it serves as a way to abort the initialization in correct
							// location.
							panic(errSimulatedCrash)
						},
					},
					ExpectedError: errSimulatedCrash,
				},
				Begin{
					RelativePath:  setup.RelativePath,
					ExpectedError: errInitializationFailed,
				},
				AssertManager{
					ExpectedError: errSimulatedCrash,
				},
			},
			expectedState: StateAssertion{
				// The test case fails before the partition's state directory is created.
				Directory: testhelper.DirectoryState{},
			},
		},
		{
			desc: "transaction rollbacked after already being rollbacked",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Rollback{},
				Rollback{
					ExpectedError: ErrTransactionAlreadyRollbacked,
				},
			},
		},
		{
			desc: "transaction rollbacked after already being committed",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{},
				Rollback{
					ExpectedError: ErrTransactionAlreadyCommitted,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
			},
		},
		{
			desc: "transaction committed after already being committed",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{},
				Commit{
					ExpectedError: ErrTransactionAlreadyCommitted,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
			},
		},
		{
			desc: "transaction committed after already being rollbacked",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Rollback{},
				Commit{
					ExpectedError: ErrTransactionAlreadyRollbacked,
				},
			},
		},
		{
			desc: "read-only transaction doesn't commit a log entry",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
					ReadOnly:     true,
				},
				Commit{},
			},
		},
		{
			desc: "read-only transaction fails with reference updates staged",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
					ReadOnly:     true,
				},
				Commit{
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					ExpectedError: errReadOnlyReferenceUpdates,
				},
			},
		},
		{
			desc: "read-only transaction fails with default branch update staged",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
					ReadOnly:     true,
				},
				Commit{
					DefaultBranchUpdate: &DefaultBranchUpdate{
						Reference: "refs/heads/main",
					},
					ExpectedError: errReadOnlyDefaultBranchUpdate,
				},
			},
		},
		{
			desc: "read-only transaction fails with custom hooks update staged",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
					ReadOnly:     true,
				},
				Commit{
					CustomHooksUpdate: &CustomHooksUpdate{
						CustomHooksTAR: validCustomHooks(t),
					},
					ExpectedError: errReadOnlyCustomHooksUpdate,
				},
			},
		},
		{
			desc: "read-only transaction fails with objects staged",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
					ReadOnly:     true,
				},
				Commit{
					IncludeObjects: []git.ObjectID{setup.Commits.First.OID},
					ExpectedError:  errReadOnlyObjectsIncluded,
				},
			},
		},
		{
			desc: "transactions are snapshot isolated from concurrent updates",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 2,
					DefaultBranchUpdate: &DefaultBranchUpdate{
						Reference: "refs/heads/new-head",
					},
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.First.Pack},
					CustomHooksUpdate: &CustomHooksUpdate{
						CustomHooksTAR: validCustomHooks(t),
					},
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				// This transaction was started before the commit, so it should see the original state.
				RepositoryAssertion{
					TransactionID: 1,
					Repositories: RepositoryStates{
						setup.RelativePath: {
							DefaultBranch: "refs/heads/main",
						},
					},
				},
				// This transaction was started after the commit, so it should see the new state.
				RepositoryAssertion{
					TransactionID: 3,
					Repositories: RepositoryStates{
						setup.RelativePath: {
							DefaultBranch: "refs/heads/new-head",
							References: &ReferencesState{
								LooseReferences: map[git.ReferenceName]git.ObjectID{
									"refs/heads/main": setup.Commits.First.OID,
								},
							},
							Objects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
							},
							CustomHooks: testhelper.DirectoryState{
								"/": {Mode: umask.Mask(fs.ModeDir | perm.PrivateDir)},
								"/pre-receive": {
									Mode:    umask.Mask(fs.ModePerm),
									Content: []byte("hook content"),
								},
								"/private-dir":              {Mode: umask.Mask(fs.ModeDir | perm.PrivateDir)},
								"/private-dir/private-file": {Mode: umask.Mask(perm.PrivateFile), Content: []byte("private content")},
							},
						},
					},
				},
				Rollback{
					TransactionID: 1,
				},
				Rollback{
					TransactionID: 3,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/new-head",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
						CustomHooks: testhelper.DirectoryState{
							"/": {Mode: umask.Mask(fs.ModeDir | perm.PrivateDir)},
							"/pre-receive": {
								Mode:    umask.Mask(fs.ModePerm),
								Content: []byte("hook content"),
							},
							"/private-dir":              {Mode: umask.Mask(fs.ModeDir | perm.PrivateDir)},
							"/private-dir/private-file": {Mode: umask.Mask(perm.PrivateFile), Content: []byte("private content")},
						},
					},
				},
			},
		},
		{
			desc: "start transaction with empty relative path",
			steps: steps{
				StartManager{},
				Begin{
					ExpectedError: errRelativePathNotSet,
				},
			},
		},
	}
}

func generateCommittedEntriesTests(t *testing.T, setup testTransactionSetup) []transactionTestCase {
	assertCommittedEntries := func(t *testing.T, expected []*committedEntry, actualList *list.List) {
		require.Equal(t, len(expected), actualList.Len())

		i := 0
		for elm := actualList.Front(); elm != nil; elm = elm.Next() {
			actual := elm.Value.(*committedEntry)
			require.Equal(t, expected[i].lsn, actual.lsn)
			require.Equal(t, expected[i].snapshotReaders, actual.snapshotReaders)
			testhelper.ProtoEqual(t, expected[i].entry, actual.entry)
			i++
		}
	}

	refChangeLogEntry := func(ref string, oid git.ObjectID) *gitalypb.LogEntry {
		return &gitalypb.LogEntry{
			RelativePath: setup.RelativePath,
			ReferenceTransactions: []*gitalypb.LogEntry_ReferenceTransaction{
				{
					Changes: []*gitalypb.LogEntry_ReferenceTransaction_Change{
						{
							ReferenceName: []byte(ref),
							NewOid:        []byte(oid),
						},
					},
				},
			},
		}
	}

	return []transactionTestCase{
		{
			desc: "manager has just initialized",
			steps: steps{
				StartManager{},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{}, tm.committedEntries)
				}),
			},
		},
		{
			desc: "a transaction has one reader",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{
						{
							lsn:             0,
							snapshotReaders: 1,
						},
					}, tm.committedEntries)
				}),
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-1": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{}, tm.committedEntries)
				}),
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{
						{
							lsn:             1,
							snapshotReaders: 1,
						},
					}, tm.committedEntries)
				}),
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{}, tm.committedEntries)
				}),
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main":     setup.Commits.First.OID,
								"refs/heads/branch-1": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "a transaction has multiple readers",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{
						{
							lsn:             1,
							snapshotReaders: 2,
						},
					}, tm.committedEntries)
				}),
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-1": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{
						{
							lsn:             1,
							snapshotReaders: 1,
						},
						{
							lsn:   2,
							entry: refChangeLogEntry("refs/heads/branch-1", setup.Commits.First.OID),
						},
					}, tm.committedEntries)
				}),
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{
						{
							lsn:             1,
							snapshotReaders: 1,
						},
						{
							lsn:             2,
							snapshotReaders: 1,
							entry:           refChangeLogEntry("refs/heads/branch-1", setup.Commits.First.OID),
						},
					}, tm.committedEntries)
				}),
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-2": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{
						{
							lsn:             2,
							entry:           refChangeLogEntry("refs/heads/branch-1", setup.Commits.First.OID),
							snapshotReaders: 1,
						},
						{
							lsn:   3,
							entry: refChangeLogEntry("refs/heads/branch-2", setup.Commits.First.OID),
						},
					}, tm.committedEntries)
				}),
				Rollback{
					TransactionID: 4,
				},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{}, tm.committedEntries)
				}),
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main":     setup.Commits.First.OID,
								"refs/heads/branch-1": setup.Commits.First.OID,
								"refs/heads/branch-2": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "committed read-only transaction are not kept",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
					ReadOnly:      true,
				},
				Commit{
					TransactionID: 1,
				},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{}, tm.committedEntries)
				}),
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
					ReadOnly:      true,
				},
				Commit{
					TransactionID: 2,
				},
				AdhocAssertion(func(t *testing.T, ctx context.Context, tm *TransactionManager) {
					assertCommittedEntries(t, []*committedEntry{}, tm.committedEntries)
				}),
			},
			expectedState: StateAssertion{
				Database: DatabaseState{},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
					},
				},
			},
		},
	}
}

// BenchmarkTransactionManager benchmarks the transaction throughput of the TransactionManager at various levels
// of concurrency and transaction sizes.
func BenchmarkTransactionManager(b *testing.B) {
	for _, tc := range []struct {
		// numberOfRepositories sets the number of repositories that are updating the references. Each repository has
		// its own TransactionManager. Setting this to 1 allows for testing throughput of a single repository while
		// setting this higher allows for testing parallel throughput of multiple repositories. This mostly serves
		// to determine the impact of the shared resources such as the database.
		numberOfRepositories int
		// concurrentUpdaters sets the number of goroutines that are calling committing transactions for a repository.
		// Each of the updaters work on their own references so they don't block each other. Setting this to 1 allows
		// for testing sequential update throughput of a repository. Setting this higher allows for testing reference
		// update throughput when multiple references are being updated concurrently.
		concurrentUpdaters int
		// transactionSize sets the number of references that are updated in each transaction.
		transactionSize int
	}{
		{
			numberOfRepositories: 1,
			concurrentUpdaters:   1,
			transactionSize:      1,
		},
		{
			numberOfRepositories: 10,
			concurrentUpdaters:   1,
			transactionSize:      1,
		},
		{
			numberOfRepositories: 1,
			concurrentUpdaters:   10,
			transactionSize:      1,
		},
		{
			numberOfRepositories: 1,
			concurrentUpdaters:   1,
			transactionSize:      10,
		},
		{
			numberOfRepositories: 10,
			concurrentUpdaters:   1,
			transactionSize:      10,
		},
	} {
		desc := fmt.Sprintf("%d repositories/%d updaters/%d transaction size",
			tc.numberOfRepositories,
			tc.concurrentUpdaters,
			tc.transactionSize,
		)
		b.Run(desc, func(b *testing.B) {
			ctx := testhelper.Context(b)

			cfg := testcfg.Build(b)
			logger := testhelper.NewLogger(b)

			cmdFactory := gittest.NewCommandFactory(b, cfg)
			cache := catfile.NewCache(cfg)
			defer cache.Stop()

			database, err := OpenDatabase(testhelper.SharedLogger(b), b.TempDir())
			require.NoError(b, err)
			defer testhelper.MustClose(b, database)

			txManager := transaction.NewManager(cfg, logger, backchannel.NewRegistry())
			housekeepingManager := housekeeping.NewManager(cfg.Prometheus, logger, txManager)

			var (
				// managerWG records the running TransactionManager.Run goroutines.
				managerWG sync.WaitGroup
				managers  []*TransactionManager

				// The references are updated back and forth between commit1 and commit2.
				commit1 git.ObjectID
				commit2 git.ObjectID
			)

			// getReferenceUpdates builds a ReferenceUpdates with unique branches for the updater.
			getReferenceUpdates := func(updaterID int, old, new git.ObjectID) ReferenceUpdates {
				referenceUpdates := make(ReferenceUpdates, tc.transactionSize)
				for i := 0; i < tc.transactionSize; i++ {
					referenceUpdates[git.ReferenceName(fmt.Sprintf("refs/heads/updater-%d-branch-%d", updaterID, i))] = ReferenceUpdate{
						OldOID: old,
						NewOID: new,
					}
				}

				return referenceUpdates
			}

			repositoryFactory, err := localrepo.NewFactory(
				logger, config.NewLocator(cfg), cmdFactory, cache,
			).ScopeByStorage(cfg.Storages[0].Name)
			require.NoError(b, err)

			// Set up the repositories and start their TransactionManagers.
			var relativePaths []string
			for i := 0; i < tc.numberOfRepositories; i++ {
				repo, repoPath := gittest.CreateRepository(b, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				relativePaths = append(relativePaths, repo.RelativePath)

				// Set up two commits that the updaters update their references back and forth.
				// The commit IDs are the same across all repositories as the parameters used to
				// create them are the same. We thus simply override the commit IDs here across
				// repositories.
				commit1 = gittest.WriteCommit(b, cfg, repoPath, gittest.WithParents())
				commit2 = gittest.WriteCommit(b, cfg, repoPath, gittest.WithParents(commit1))

				storagePath := cfg.Storages[0].Path

				stateDir := filepath.Join(storagePath, "state", strconv.Itoa(i))
				require.NoError(b, os.MkdirAll(stateDir, perm.PrivateDir))

				stagingDir := filepath.Join(storagePath, "staging", strconv.Itoa(i))
				require.NoError(b, os.MkdirAll(stagingDir, perm.PrivateDir))

				// Valid partition IDs are >=1.
				testPartitionID := partitionID(i + 1)
				manager := NewTransactionManager(testPartitionID, logger, database, storagePath, stateDir, stagingDir, cmdFactory, housekeepingManager, repositoryFactory)

				managers = append(managers, manager)

				managerWG.Add(1)
				go func() {
					defer managerWG.Done()
					assert.NoError(b, manager.Run())
				}()

				objectHash, err := repositoryFactory.Build(repo.RelativePath).ObjectHash(ctx)
				require.NoError(b, err)

				for j := 0; j < tc.concurrentUpdaters; j++ {
					transaction, err := manager.Begin(ctx, repo.RelativePath, nil, false)
					require.NoError(b, err)
					transaction.UpdateReferences(getReferenceUpdates(j, objectHash.ZeroOID, commit1))
					require.NoError(b, transaction.Commit(ctx))
				}
			}

			// transactionWG tracks the number of on going transaction.
			var transactionWG sync.WaitGroup
			transactionChan := make(chan struct{})

			for i, manager := range managers {
				manager := manager
				relativePath := relativePaths[i]
				for i := 0; i < tc.concurrentUpdaters; i++ {

					// Build the reference updates that this updater will go back and forth with.
					currentReferences := getReferenceUpdates(i, commit1, commit2)
					nextReferences := getReferenceUpdates(i, commit2, commit1)

					transaction, err := manager.Begin(ctx, relativePath, nil, false)
					require.NoError(b, err)
					transaction.UpdateReferences(currentReferences)

					// Setup the starting state so the references start at the expected old tip.
					require.NoError(b, transaction.Commit(ctx))

					transactionWG.Add(1)
					go func() {
						defer transactionWG.Done()

						for range transactionChan {
							transaction, err := manager.Begin(ctx, relativePath, nil, false)
							require.NoError(b, err)
							transaction.UpdateReferences(nextReferences)
							assert.NoError(b, transaction.Commit(ctx))
							currentReferences, nextReferences = nextReferences, currentReferences
						}
					}()
				}
			}

			b.ReportAllocs()
			b.ResetTimer()

			began := time.Now()
			for n := 0; n < b.N; n++ {
				transactionChan <- struct{}{}
			}
			close(transactionChan)

			transactionWG.Wait()
			b.StopTimer()

			b.ReportMetric(float64(b.N*tc.transactionSize)/time.Since(began).Seconds(), "reference_updates/s")

			for _, manager := range managers {
				manager.Close()
			}

			managerWG.Wait()
		})
	}
}
