package storagemgr

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func generateCreateRepositoryTests(t *testing.T, setup testTransactionSetup) []transactionTestCase {
	return []transactionTestCase{
		{
			desc: "create repository when it doesn't exist",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				CreateRepository{},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                               storage.LSN(1).ToProto(),
					"kv/" + string(relativePathKey(setup.RelativePath)): string(""),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						Objects: []git.ObjectID{},
					},
				},
			},
		},
		{
			desc: "create repository when it already exists",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				CreateRepository{
					TransactionID: 1,
				},
				CreateRepository{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 2,
					ExpectedError: ErrRepositoryAlreadyExists,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                               storage.LSN(1).ToProto(),
					"kv/" + string(relativePathKey(setup.RelativePath)): string(""),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						Objects: []git.ObjectID{},
					},
				},
			},
		},
		{
			desc: "create repository with full state",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				CreateRepository{
					DefaultBranch: "refs/heads/branch",
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main":   setup.Commits.First.OID,
						"refs/heads/branch": setup.Commits.Second.OID,
					},
					Packs:       [][]byte{setup.Commits.First.Pack, setup.Commits.Second.Pack},
					CustomHooks: validCustomHooks(t),
				},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                               storage.LSN(1).ToProto(),
					"kv/" + string(relativePathKey(setup.RelativePath)): string(""),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/branch",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main":   setup.Commits.First.OID,
								"refs/heads/branch": setup.Commits.Second.OID,
							},
						},
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
						CustomHooks: testhelper.DirectoryState{
							"/": {Mode: storage.ModeDirectory},
							"/pre-receive": {
								Mode:    storage.ModeExecutable,
								Content: []byte("hook content"),
							},
							"/private-dir":              {Mode: storage.ModeDirectory},
							"/private-dir/private-file": {Mode: storage.ModeFile, Content: []byte("private content")},
						},
					},
				},
			},
		},
		{
			desc: "transactions are snapshot isolated from concurrent creations",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				CreateRepository{
					TransactionID: 1,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
					Packs:       [][]byte{setup.Commits.First.Pack},
					CustomHooks: validCustomHooks(t),
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ReadOnly:            true,
					ExpectedSnapshotLSN: 1,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID:    3,
					DeleteRepository: true,
				},
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				CreateRepository{
					TransactionID: 4,
					DefaultBranch: "refs/heads/other",
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/other": setup.Commits.Second.OID,
					},
					Packs: [][]byte{setup.Commits.First.Pack, setup.Commits.Second.Pack},
				},
				Commit{
					TransactionID: 4,
				},
				// Transaction 2 has been open through out the repository deletion and creation. It should
				// still see the original state of the repository before the deletion.
				RepositoryAssertion{
					TransactionID: 2,
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
							CustomHooks: testhelper.DirectoryState{
								"/": {Mode: storage.ModeDirectory},
								"/pre-receive": {
									Mode:    storage.ModeExecutable,
									Content: []byte("hook content"),
								},
								"/private-dir":              {Mode: storage.ModeDirectory},
								"/private-dir/private-file": {Mode: storage.ModeFile, Content: []byte("private content")},
							},
						},
					},
				},
				Begin{
					TransactionID:       5,
					RelativePath:        setup.RelativePath,
					ReadOnly:            true,
					ExpectedSnapshotLSN: 3,
				},
				RepositoryAssertion{
					TransactionID: 5,
					Repositories: RepositoryStates{
						setup.RelativePath: {
							DefaultBranch: "refs/heads/other",
							References: &ReferencesState{
								LooseReferences: map[git.ReferenceName]git.ObjectID{
									"refs/heads/other": setup.Commits.Second.OID,
								},
							},
							Objects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
							},
						},
					},
				},
				Rollback{
					TransactionID: 2,
				},
				Rollback{
					TransactionID: 5,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                               storage.LSN(3).ToProto(),
					"kv/" + string(relativePathKey(setup.RelativePath)): string(""),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/other",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/other": setup.Commits.Second.OID,
							},
						},
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
			desc: "logged repository creation is respected",
			steps: steps{
				RemoveRepository{},
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
				CreateRepository{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 1,
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
					TransactionID:    2,
					DeleteRepository: true,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "reapplying repository creation works",
			steps: steps{
				RemoveRepository{},
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
				CreateRepository{
					TransactionID: 1,
					DefaultBranch: "refs/heads/branch",
					Packs:         [][]byte{setup.Commits.First.Pack},
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
					CustomHooks: validCustomHooks(t),
				},
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
					string(keyAppliedLSN):                               storage.LSN(1).ToProto(),
					"kv/" + string(relativePathKey(setup.RelativePath)): string(""),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/branch",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						CustomHooks: testhelper.DirectoryState{
							"/": {Mode: storage.ModeDirectory},
							"/pre-receive": {
								Mode:    storage.ModeExecutable,
								Content: []byte("hook content"),
							},
							"/private-dir":              {Mode: storage.ModeDirectory},
							"/private-dir/private-file": {Mode: storage.ModeFile, Content: []byte("private content")},
						},
						Objects: []git.ObjectID{
							setup.Commits.First.OID,
							setup.ObjectHash.EmptyTreeOID,
						},
					},
				},
			},
		},
		{
			desc: "commit without creating a repository",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{},
			},
			expectedState: StateAssertion{
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "two repositories created in different transactions",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "repository-1",
				},
				Begin{
					TransactionID: 2,
					RelativePath:  "repository-2",
				},
				CreateRepository{
					TransactionID: 1,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
					Packs:       [][]byte{setup.Commits.First.Pack},
					CustomHooks: validCustomHooks(t),
				},
				CreateRepository{
					TransactionID: 2,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/branch": setup.Commits.Third.OID,
					},
					DefaultBranch: "refs/heads/branch",
					Packs: [][]byte{
						setup.Commits.First.Pack,
						setup.Commits.Second.Pack,
						setup.Commits.Third.Pack,
					},
				},
				Commit{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                           storage.LSN(2).ToProto(),
					"kv/" + string(relativePathKey("repository-1")): string(""),
					"kv/" + string(relativePathKey("repository-2")): string(""),
				},
				Repositories: RepositoryStates{
					"repository-1": {
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
							"/": {Mode: storage.ModeDirectory},
							"/pre-receive": {
								Mode:    storage.ModeExecutable,
								Content: []byte("hook content"),
							},
							"/private-dir":              {Mode: storage.ModeDirectory},
							"/private-dir/private-file": {Mode: storage.ModeFile, Content: []byte("private content")},
						},
					},
					"repository-2": {
						DefaultBranch: "refs/heads/branch",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch": setup.Commits.Third.OID,
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
	}
}

func generateDeleteRepositoryTests(t *testing.T, setup testTransactionSetup) []transactionTestCase {
	return []transactionTestCase{
		{
			desc: "repository is successfully deleted",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID:    1,
					DeleteRepository: true,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				RepositoryAssertion{
					TransactionID: 2,
					Repositories:  RepositoryStates{},
				},
				Rollback{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "repository deletion including a reference modification succeeds",
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
					DeleteRepository: true,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				RepositoryAssertion{
					TransactionID: 2,
					Repositories:  RepositoryStates{},
				},
				Rollback{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "repository deletion fails if repository is deleted",
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
				Commit{
					TransactionID:    1,
					DeleteRepository: true,
				},
				Commit{
					TransactionID:    2,
					DeleteRepository: true,
					ExpectedError:    ErrRepositoryNotFound,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "custom hooks update fails if repository is deleted",
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
				Commit{
					TransactionID:    1,
					DeleteRepository: true,
				},
				Commit{
					TransactionID:     2,
					CustomHooksUpdate: &CustomHooksUpdate{CustomHooksTAR: validCustomHooks(t)},
					ExpectedError:     ErrRepositoryNotFound,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "reference updates fail if repository is deleted",
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
				Commit{
					TransactionID:    1,
					DeleteRepository: true,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					ExpectedError: ErrRepositoryNotFound,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "default branch update fails if repository is deleted",
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
				Commit{
					TransactionID:    1,
					DeleteRepository: true,
				},
				Commit{
					TransactionID: 2,
					DefaultBranchUpdate: &DefaultBranchUpdate{
						Reference: "refs/heads/new-default",
					},
					ExpectedError: ErrRepositoryNotFound,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "logged repository deletions are considered after restart",
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
					TransactionID:    1,
					DeleteRepository: true,
					ExpectedError:    ErrTransactionProcessingStopped,
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
				RepositoryAssertion{
					TransactionID: 2,
					Repositories:  RepositoryStates{},
				},
				Rollback{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "reapplying repository deletion works",
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
					TransactionID:    1,
					DeleteRepository: true,
					ExpectedError:    ErrTransactionProcessingStopped,
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
				RepositoryAssertion{
					TransactionID: 2,
					Repositories:  RepositoryStates{},
				},
				Rollback{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			// This is a serialization violation as the outcome would be different
			// if the transactions were applied in different order.
			desc: "deletion succeeds with concurrent writes to repository",
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
				Commit{
					TransactionID: 1,
					DefaultBranchUpdate: &DefaultBranchUpdate{
						Reference: "refs/heads/branch",
					},
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					CustomHooksUpdate: &CustomHooksUpdate{
						CustomHooksTAR: validCustomHooks(t),
					},
				},
				Commit{
					TransactionID:    2,
					DeleteRepository: true,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "deletion waits until other transactions are done",
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
				Commit{
					TransactionID:    1,
					DeleteRepository: true,
				},
				// The concurrent transaction should be able to read the
				// repository despite the committed deletion.
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
				Rollback{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "read-only transaction fails with repository deletion staged",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
					ReadOnly:     true,
				},
				Commit{
					DeleteRepository: true,
					ExpectedError:    errReadOnlyRepositoryDeletion,
				},
			},
		},
		{
			desc: "transactions are snapshot isolated from concurrent deletions",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
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
					TransactionID:    2,
					DeleteRepository: true,
				},
				// This transaction was started before the deletion, so it should see the old state regardless
				// of the repository being deleted.
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
								"/": {Mode: storage.ModeDirectory},
								"/pre-receive": {
									Mode:    storage.ModeExecutable,
									Content: []byte("hook content"),
								},
								"/private-dir":              {Mode: storage.ModeDirectory},
								"/private-dir/private-file": {Mode: storage.ModeFile, Content: []byte("private content")},
							},
						},
					},
				},
				Rollback{
					TransactionID: 3,
				},
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				RepositoryAssertion{
					TransactionID: 4,
					Repositories:  RepositoryStates{},
				},
				Rollback{
					TransactionID: 4,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "create repository again after deletion",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				CreateRepository{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 2,
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
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID:    3,
					DeleteRepository: true,
				},
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 3,
				},
				CreateRepository{
					TransactionID: 4,
				},
				Commit{
					TransactionID: 4,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                               storage.LSN(4).ToProto(),
					"kv/" + string(relativePathKey(setup.RelativePath)): string(""),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						Objects: []git.ObjectID{},
					},
				},
			},
		},
	}
}
