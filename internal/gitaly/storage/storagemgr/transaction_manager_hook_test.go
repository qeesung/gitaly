package storagemgr

import (
	"io/fs"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/dgraph-io/badger/v4"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

// hookFunc is a function that is executed at a specific point. It gets a hookContext that allows it to
// influence the execution of the test.
type hookFunc func(hookContext)

// hookContext are the control toggels available in a hook.
type hookContext struct {
	// closeManager calls the calls Close on the TransactionManager.
	closeManager func()
	// database provides access to the database for the hook handler.
	database Database
	tb       testing.TB
}

// hooks are functions that get invoked at specific points of the TransactionManager Run method. They allow
// for hooking into the Run method at specific poins which would otherwise to do assertions that would otherwise
// not be possible.
type hooks struct {
	// beforeReadLogEntry is invoked before a log entry is read from the database.
	beforeReadLogEntry hookFunc
	// beforeStoreLogEntry is invoked before the log entry is stored to the database.
	beforeStoreLogEntry hookFunc
	// beforeDeferredClose is invoked before the deferred Close is invoked in Run.
	beforeDeferredClose hookFunc
	// beforeDeleteLogEntry is invoked before a log entry is deleted from the database.
	beforeDeleteLogEntry hookFunc
	// beforeReadAppliedLSN is invoked before the applied LSN is read from the database.
	beforeReadAppliedLSN hookFunc
	// beforeStoreAppliedLSN is invoked before a the applied LSN is stored.
	beforeStoreAppliedLSN hookFunc
}

// installHooks installs the configured hooks into the transactionManager.
func installHooks(tb testing.TB, transactionManager *TransactionManager, database Database, hooks hooks) {
	hookContext := hookContext{closeManager: transactionManager.close, database: database, tb: &testingHook{TB: tb}}

	transactionManager.close = func() {
		programCounter, _, _, ok := runtime.Caller(2)
		require.True(tb, ok)

		isDeferredCloseInRun := strings.HasSuffix(
			runtime.FuncForPC(programCounter).Name(),
			"gitaly.(*TransactionManager).Run",
		)

		if isDeferredCloseInRun && hooks.beforeDeferredClose != nil {
			hooks.beforeDeferredClose(hookContext)
		}

		hookContext.closeManager()
	}

	transactionManager.db = databaseHook{
		Database:    database,
		hooks:       hooks,
		hookContext: hookContext,
	}
}

type databaseHook struct {
	Database
	hookContext
	hooks
}

func (hook databaseHook) View(handler func(DatabaseTransaction) error) error {
	return hook.Database.View(func(transaction DatabaseTransaction) error {
		return handler(databaseTransactionHook{
			databaseTransaction: transaction,
			hookContext:         hook.hookContext,
			hooks:               hook.hooks,
		})
	})
}

func (hook databaseHook) Update(handler func(DatabaseTransaction) error) error {
	return hook.Database.Update(func(transaction DatabaseTransaction) error {
		return handler(databaseTransactionHook{
			databaseTransaction: transaction,
			hookContext:         hook.hookContext,
			hooks:               hook.hooks,
		})
	})
}

func (hook databaseHook) NewWriteBatch() WriteBatch {
	return writeBatchHook{
		writeBatch:  hook.database.NewWriteBatch(),
		hookContext: hook.hookContext,
		hooks:       hook.hooks,
	}
}

type databaseTransactionHook struct {
	databaseTransaction DatabaseTransaction
	hookContext
	hooks
}

var (
	regexLogEntry   = regexp.MustCompile("partition/.+/log/entry/")
	regexAppliedLSN = regexp.MustCompile("partition/.+/applied_lsn")
)

func (hook databaseTransactionHook) Get(key []byte) (*badger.Item, error) {
	if regexLogEntry.Match(key) {
		if hook.hooks.beforeReadLogEntry != nil {
			hook.hooks.beforeReadLogEntry(hook.hookContext)
		}
	} else if regexAppliedLSN.Match(key) {
		if hook.hooks.beforeReadAppliedLSN != nil {
			hook.hooks.beforeReadAppliedLSN(hook.hookContext)
		}
	}

	return hook.databaseTransaction.Get(key)
}

func (hook databaseTransactionHook) NewIterator(options badger.IteratorOptions) *badger.Iterator {
	return hook.databaseTransaction.NewIterator(options)
}

func (hook databaseTransactionHook) Delete(key []byte) error {
	if regexLogEntry.Match(key) && hook.beforeDeleteLogEntry != nil {
		hook.beforeDeleteLogEntry(hook.hookContext)
	}

	return hook.databaseTransaction.Delete(key)
}

type writeBatchHook struct {
	writeBatch WriteBatch
	hookContext
	hooks
}

func (hook writeBatchHook) Set(key []byte, value []byte) error {
	if regexAppliedLSN.Match(key) && hook.hooks.beforeStoreAppliedLSN != nil {
		hook.hooks.beforeStoreAppliedLSN(hook.hookContext)
	}

	if regexLogEntry.Match(key) && hook.hooks.beforeStoreLogEntry != nil {
		hook.hooks.beforeStoreLogEntry(hook.hookContext)
	}

	return hook.writeBatch.Set(key, value)
}

func (hook writeBatchHook) Flush() error { return hook.writeBatch.Flush() }

func (hook writeBatchHook) Cancel() { hook.writeBatch.Cancel() }

type testingHook struct {
	testing.TB
}

// We override the FailNow call to the regular testing.Fail, so that it can be
// used within goroutines without replacing calls made to the `require` library.
func (t testingHook) FailNow() {
	t.Fail()
}

func generateCustomHooksTests(t *testing.T, setup testTransactionSetup) []transactionTestCase {
	umask := testhelper.Umask()

	return []transactionTestCase{
		{
			desc: "set custom hooks successfully",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					CustomHooksUpdate: &CustomHooksUpdate{
						CustomHooksTAR: validCustomHooks(t),
					},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID:     2,
					CustomHooksUpdate: &CustomHooksUpdate{},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":    {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal": {Mode: fs.ModeDir | perm.PrivateDir},
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						CustomHooks: testhelper.DirectoryState{
							"/": {Mode: fs.ModeDir | perm.PrivateDir},
						},
					},
				},
			},
		},
		{
			desc: "rejects invalid custom hooks",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					CustomHooksUpdate: &CustomHooksUpdate{
						CustomHooksTAR: []byte("corrupted tar"),
					},
					ExpectedError: func(tb testing.TB, actualErr error) {
						require.ErrorContains(tb, actualErr, "stage hooks: extract hooks: waiting for tar command completion: exit status")
					},
				},
			},
		},
		{
			desc: "reapplying custom hooks works",
			steps: steps{
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
					CustomHooksUpdate: &CustomHooksUpdate{
						CustomHooksTAR: validCustomHooks(t),
					},
					ExpectedError: ErrTransactionProcessingStopped,
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
					"/":    {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal": {Mode: fs.ModeDir | perm.PrivateDir},
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
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
		},
		{
			desc: "hook index is correctly determined from log and disk",
			steps: steps{
				StartManager{
					Hooks: testTransactionHooks{
						BeforeApplyLogEntry: func(hookContext) {
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
					CustomHooksUpdate: &CustomHooksUpdate{
						CustomHooksTAR: validCustomHooks(t),
					},
					ExpectedError: ErrTransactionProcessingStopped,
				},
				AssertManager{
					ExpectedError: errSimulatedCrash,
				},
				StartManager{},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID:     2,
					CustomHooksUpdate: &CustomHooksUpdate{},
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				CloseManager{},
				StartManager{},
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				Rollback{
					TransactionID: 4,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":    {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal": {Mode: fs.ModeDir | perm.PrivateDir},
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						CustomHooks: testhelper.DirectoryState{
							"/": {Mode: fs.ModeDir | perm.PrivateDir},
						},
					},
				},
			},
		},
		{
			desc: "continues processing after reference verification failure",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
					ExpectedError: ReferenceVerificationError{
						ReferenceName: "refs/heads/main",
						ExpectedOID:   setup.Commits.First.OID,
						ActualOID:     setup.ObjectHash.ZeroOID,
					},
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
					},
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
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "continues processing after a restart",
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
				AssertManager{},
				CloseManager{},
				StartManager{},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
				},
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
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "continues processing after restarting after a reference verification failure",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
					ExpectedError: ReferenceVerificationError{
						ReferenceName: "refs/heads/main",
						ExpectedOID:   setup.Commits.First.OID,
						ActualOID:     setup.ObjectHash.ZeroOID,
					},
				},
				CloseManager{},
				StartManager{},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
					},
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
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "continues processing after failing to store log index",
			steps: steps{
				StartManager{
					Hooks: testTransactionHooks{
						BeforeStoreAppliedLSN: func(hookCtx hookContext) {
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
					ExpectedError: ErrTransactionProcessingStopped,
				},
				AssertManager{
					ExpectedError: errSimulatedCrash,
				},
				StartManager{},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
				},
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
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "recovers from the write-ahead log on start up",
			steps: steps{
				StartManager{
					Hooks: testTransactionHooks{
						BeforeApplyLogEntry: func(hookCtx hookContext) {
							hookCtx.closeManager()
						},
					},
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
					ExpectedError: ErrTransactionProcessingStopped,
				},
				AssertManager{},
				StartManager{},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
				},
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
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "reference verification fails after recovering logged writes",
			steps: steps{
				StartManager{
					Hooks: testTransactionHooks{
						BeforeApplyLogEntry: func(hookCtx hookContext) {
							hookCtx.closeManager()
						},
					},
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
					ExpectedError: ErrTransactionProcessingStopped,
				},
				AssertManager{},
				StartManager{},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.First.OID},
					},
					ExpectedError: ReferenceVerificationError{
						ReferenceName: "refs/heads/main",
						ExpectedOID:   setup.Commits.Second.OID,
						ActualOID:     setup.Commits.First.OID,
					},
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
		},
	}
}
