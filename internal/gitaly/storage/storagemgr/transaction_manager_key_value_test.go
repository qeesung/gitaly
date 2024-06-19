package storagemgr

import (
	"github.com/dgraph-io/badger/v4"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
)

func generateKeyValueTests(setup testTransactionSetup) []transactionTestCase {
	return []transactionTestCase{
		{
			desc: "set keys with values",
			steps: steps{
				StartManager{},
				Begin{},
				SetKey{Key: "key-1", Value: "value-1"},
				SetKey{Key: "key-2", Value: "value-2"},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
					"kv/key-1":            "value-1",
					"kv/key-2":            "value-2",
				},
			},
		},
		{
			desc: "override a key",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				SetKey{TransactionID: 1, Key: "key-2", Value: "value-2"},
				Commit{TransactionID: 1},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				SetKey{TransactionID: 2, Key: "key-2", Value: "value-3"},
				Commit{TransactionID: 2},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
					"kv/key-1":            "value-1",
					"kv/key-2":            "value-3",
				},
			},
		},
		{
			desc: "override key within a transaction",
			steps: steps{
				StartManager{},
				Begin{},
				SetKey{Key: "key-1", Value: "value-1"},
				SetKey{Key: "key-2", Value: "value-2"},
				SetKey{Key: "key-2", Value: "value-3"},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
					"kv/key-1":            "value-1",
					"kv/key-2":            "value-3",
				},
			},
		},
		{
			desc: "delete a key",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				SetKey{TransactionID: 1, Key: "key-2", Value: "value-2"},
				Commit{TransactionID: 1},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				DeleteKey{TransactionID: 2, Key: "key-2"},
				Commit{TransactionID: 2},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
					"kv/key-1":            "value-1",
				},
			},
		},
		{
			desc: "delete a key within a transaction",
			steps: steps{
				StartManager{},
				Begin{},
				SetKey{Key: "key-1", Value: "value-1"},
				SetKey{Key: "key-2", Value: "value-2"},
				DeleteKey{Key: "key-2"},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
					"kv/key-1":            "value-1",
				},
			},
		},
		{
			desc: "delete a non-existent key",
			steps: steps{
				StartManager{},
				Begin{},
				DeleteKey{Key: "key-1"},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
			},
		},
		{
			desc: "blind sets to a key do not conflict",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				SetKey{TransactionID: 2, Key: "key-1", Value: "value-2"},
				Commit{TransactionID: 1},
				Commit{TransactionID: 2},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
					"kv/key-1":            "value-2",
				},
			},
		},
		{
			desc: "blind deletes of a key do not conflict",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				Commit{TransactionID: 1},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					ExpectedSnapshotLSN: 1,
				},
				DeleteKey{TransactionID: 2, Key: "key-1"},
				DeleteKey{TransactionID: 3, Key: "key-1"},
				Commit{TransactionID: 2},
				Commit{TransactionID: 3},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
				},
			},
		},
		{
			desc: "blind set of a key does not conflict with deletion",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				Commit{TransactionID: 1},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					ExpectedSnapshotLSN: 1,
				},
				DeleteKey{TransactionID: 2, Key: "key-1"},
				SetKey{TransactionID: 3, Key: "key-1", Value: "value-2"},
				Commit{TransactionID: 2},
				Commit{TransactionID: 3},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
					"kv/key-1":            "value-2",
				},
			},
		},
		{
			desc: "blind delete of a key does not conflict with setting",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				Commit{TransactionID: 1},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					ExpectedSnapshotLSN: 1,
				},
				DeleteKey{TransactionID: 2, Key: "key-1"},
				SetKey{TransactionID: 3, Key: "key-1", Value: "value-2"},
				Commit{TransactionID: 3},
				Commit{TransactionID: 2},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
				},
			},
		},
		{
			desc: "conflict reading a key earlier transaction set",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				ReadKey{TransactionID: 2, Key: "key-1", ExpectedError: badger.ErrKeyNotFound},
				Commit{TransactionID: 1},
				Commit{
					TransactionID: 2,
					ExpectedError: newConflictingKeyValueOperationError("key-1"),
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
					"kv/key-1":            "value-1",
				},
			},
		},
		{
			desc: "conflict reading a key concurrent transaction set",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				ReadKey{TransactionID: 2, Key: "key-1", ExpectedError: badger.ErrKeyNotFound},
				Commit{TransactionID: 1},
				Commit{
					TransactionID: 2,
					ExpectedError: newConflictingKeyValueOperationError("key-1"),
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
					"kv/key-1":            "value-1",
				},
			},
		},
		{
			desc: "conflict reading a key concurrent transaction deleted",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				Commit{TransactionID: 1},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					ExpectedSnapshotLSN: 1,
				},
				DeleteKey{TransactionID: 2, Key: "key-1"},
				ReadKey{TransactionID: 3, Key: "key-1", ExpectedValue: "value-1"},
				Commit{TransactionID: 2},
				Commit{
					TransactionID: 3,
					ExpectedError: newConflictingKeyValueOperationError("key-1"),
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
			},
		},
		{
			desc: "no conflict reading key concurrently",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				Commit{TransactionID: 1},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					ExpectedSnapshotLSN: 1,
				},
				ReadKey{TransactionID: 2, Key: "key-1", ExpectedValue: "value-1"},
				ReadKey{TransactionID: 3, Key: "key-1", ExpectedValue: "value-1"},
				Commit{TransactionID: 2},
				Commit{TransactionID: 3},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
					"kv/key-1":            "value-1",
				},
			},
		},
		{
			desc: "no conflict reading a key earlier transaction set",
			steps: steps{
				StartManager{},
				// Hold a transaction open to force the manager to keep
				// the lock information around.
				Begin{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
				},
				SetKey{TransactionID: 2, Key: "key-1", Value: "value-1"},
				Commit{TransactionID: 2},
				Begin{
					TransactionID:       3,
					ExpectedSnapshotLSN: 1,
				},
				ReadKey{TransactionID: 3, Key: "key-1", ExpectedValue: "value-1"},
				Commit{TransactionID: 3},
				Commit{TransactionID: 1},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
					"kv/key-1":            "value-1",
				},
			},
		},
		{
			desc: "conflict iterating over concurrently set key",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				SetKey{TransactionID: 1, Key: "key-2", Value: "value-2"},
				SetKey{TransactionID: 1, Key: "key-3", Value: "value-3"},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					ExpectedSnapshotLSN: 1,
				},
				SetKey{TransactionID: 2, Key: "key-2", Value: "value-2-modified"},
				ReadKeyPrefix{TransactionID: 3, ExpectedValues: map[string]string{
					"key-1": "value-1",
					"key-2": "value-2",
					"key-3": "value-3",
				}},
				Commit{TransactionID: 2},
				Commit{
					TransactionID: 3,
					ExpectedError: newConflictingKeyValueOperationError("key-2"),
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
					"kv/key-1":            "value-1",
					"kv/key-2":            "value-2-modified",
					"kv/key-3":            "value-3",
				},
			},
		},
		{
			desc: "conflict iterating over concurrently deleted key",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				SetKey{TransactionID: 1, Key: "key-2", Value: "value-2"},
				SetKey{TransactionID: 1, Key: "key-3", Value: "value-3"},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					ExpectedSnapshotLSN: 1,
				},
				DeleteKey{TransactionID: 2, Key: "key-2"},
				ReadKeyPrefix{TransactionID: 3, ExpectedValues: map[string]string{
					"key-1": "value-1",
					"key-2": "value-2",
					"key-3": "value-3",
				}},
				Commit{TransactionID: 2},
				Commit{
					TransactionID: 3,
					ExpectedError: newConflictingKeyValueOperationError("key-2"),
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
					"kv/key-1":            "value-1",
					"kv/key-3":            "value-3",
				},
			},
		},
		{
			desc: "key concurrently inserted into an iterated range",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				SetKey{TransactionID: 1, Key: "key-3", Value: "value-3"},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					ExpectedSnapshotLSN: 1,
				},
				SetKey{TransactionID: 2, Key: "key-2", Value: "value-2"},
				ReadKeyPrefix{TransactionID: 3, ExpectedValues: map[string]string{
					"key-1": "value-1",
					"key-3": "value-3",
				}},
				Commit{TransactionID: 2},
				Commit{
					TransactionID: 3,
					ExpectedError: newConflictingKeyValueOperationError("key-2"),
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
					"kv/key-1":            "value-1",
					"kv/key-2":            "value-2",
					"kv/key-3":            "value-3",
				},
			},
		},
		{
			desc: "inserting key that wasn't iterated doesn't conflict",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "prefix-1/key-1", Value: "value-1"},
				SetKey{TransactionID: 1, Key: "prefix-1/key-3", Value: "value-3"},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					ExpectedSnapshotLSN: 1,
				},
				SetKey{TransactionID: 2, Key: "prefix-2/key-2", Value: "value-2"},
				ReadKeyPrefix{TransactionID: 3, Prefix: "prefix-1/", ExpectedValues: map[string]string{
					"prefix-1/key-1": "value-1",
					"prefix-1/key-3": "value-3",
				}},
				Commit{TransactionID: 2},
				Commit{TransactionID: 3},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
					"kv/prefix-1/key-1":   "value-1",
					"kv/prefix-1/key-3":   "value-3",
					"kv/prefix-2/key-2":   "value-2",
				},
			},
		},
		{
			desc: "user keys are namespaced from internal keys",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				SetKey{TransactionID: 1, Key: "key-2", Value: "value-2"},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					ExpectedSnapshotLSN: 1,
				},
				ReadKey{TransactionID: 2, Key: string(keyAppliedLSN), ExpectedError: badger.ErrKeyNotFound},
				ReadKeyPrefix{TransactionID: 2, ExpectedValues: map[string]string{
					// We don't expect to see keyAppliedLSN here
					"key-1": "value-1",
					"key-2": "value-2",
				}},
				Commit{TransactionID: 2},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
					"kv/key-1":            "value-1",
					"kv/key-2":            "value-2",
				},
			},
		},
		{
			desc: "key-value operations are recovered from write-ahead log",
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
				},
				SetKey{TransactionID: 1, Key: "key-1", Value: "value-1"},
				SetKey{TransactionID: 1, Key: "key-2", Value: "value-2"},
				Commit{
					TransactionID: 1,
					ExpectedError: ErrTransactionProcessingStopped,
				},
				AssertManager{
					ExpectedError: errSimulatedCrash,
				},
				StartManager{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
					"kv/key-1":            "value-1",
					"kv/key-2":            "value-2",
				},
			},
		},
		{
			desc: "read-only transaction does not conflict with key-value operations",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					ReadOnly:      true,
				},
				Begin{
					TransactionID: 2,
				},
				SetKey{TransactionID: 2, Key: "key-1", Value: "value-1"},
				DeleteKey{TransactionID: 2, Key: "key-2"},
				Commit{TransactionID: 2},
				ReadKey{TransactionID: 1, Key: "key-1", ExpectedError: badger.ErrKeyNotFound},
				ReadKeyPrefix{TransactionID: 1},
				Commit{TransactionID: 1},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
					"kv/key-1":            "value-1",
				},
			},
		},
		{
			desc: "key writes in read-only transaction",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					ReadOnly:      true,
				},
				SetKey{TransactionID: 1, Key: "key-1", ExpectedError: badger.ErrReadOnlyTxn},
				DeleteKey{TransactionID: 1, Key: "key-2", ExpectedError: badger.ErrReadOnlyTxn},
				Commit{TransactionID: 1, ExpectedError: errReadOnlyKeyValue},
			},
		},
		{
			desc: "rollbacked key-value operations are discarded",
			steps: steps{
				StartManager{},
				Begin{},
				SetKey{Key: "key-1", Value: "value-1"},
				SetKey{Key: "key-2", Value: "value-2"},
				Rollback{},
			},
		},
	}
}
