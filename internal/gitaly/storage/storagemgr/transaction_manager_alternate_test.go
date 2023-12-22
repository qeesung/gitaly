package storagemgr

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func generateAlternateTests(t *testing.T, setup testTransactionSetup) []transactionTestCase {
	umask := testhelper.Umask()

	return []transactionTestCase{
		{
			desc: "repository is linked to alternate on creation",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
					Packs: [][]byte{setup.Commits.First.Pack},
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
					Alternate:     "../../pool/objects",
				},
				Commit{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
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
					"member": {
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
						Alternate: "../../pool/objects",
					},
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
				},
			},
		},
		{
			desc: "repository is linked to an alternate after creation",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
					Packs: [][]byte{setup.Commits.First.Pack},
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID:   3,
					UpdateAlternate: &alternateUpdate{relativePath: "pool"},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
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
					"member": {
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
						Alternate: "../../pool/objects",
					},
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
				},
			},
		},
		{
			desc: "repository is disconnected from alternate",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
					Packs: [][]byte{setup.Commits.First.Pack},
				},
				Commit{
					TransactionID: 1,
				},
				CloseManager{},
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						// Transactions write objects always as packs into the repository. To test
						// scenarios where repositories may have existing loose objects, manually
						// unpack the objects to the repository.
						gittest.ExecOpts(tb, cfg,
							gittest.ExecConfig{Stdin: bytes.NewReader(setup.Commits.Second.Pack)},
							"-C", filepath.Join(storagePath, "pool"), "unpack-objects",
						)
					},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
					Alternate:     "../../pool/objects",
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID:   3,
					UpdateAlternate: &alternateUpdate{},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					},
					"member": {
						// The objects should have been copied over to the repository when it was
						// disconnected from the alternate.
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					},
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
				},
			},
		},
		{
			desc: "repository's alternate must be pointed to a git repository",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "repository",
				},
				CreateRepository{
					TransactionID: 1,
					Alternate:     "../..",
				},
				Commit{
					TransactionID: 1,
					ExpectedError: storage.InvalidGitDirectoryError{MissingEntry: "objects"},
				},
			},
			expectedState: StateAssertion{
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "repository's alternate must not point to repository itself",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "repository",
				},
				CreateRepository{
					TransactionID: 1,
					Alternate:     "../objects",
				},
				Commit{
					TransactionID: 1,
					ExpectedError: errAlternatePointsToSelf,
				},
			},
			expectedState: StateAssertion{
				Repositories: RepositoryStates{},
			},
		},
		{
			desc: "repository's alternate can't have an alternate itself",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
					Alternate:     "../../pool/objects",
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "recursive-member",
					ExpectedSnapshotLSN: 2,
				},
				CreateRepository{
					TransactionID: 3,
					Alternate:     "../../member/objects",
				},
				Commit{
					TransactionID: 3,
					ExpectedError: errAlternateHasAlternate,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Objects: []git.ObjectID{},
					},
					"member": {
						Objects:   []git.ObjectID{},
						Alternate: "../../pool/objects",
					},
				},
			},
		},
		{
			desc: "repository can't be linked multiple times",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID:   3,
					UpdateAlternate: &alternateUpdate{relativePath: "pool"},
				},
				Begin{
					TransactionID:       4,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 3,
				},
				Commit{
					TransactionID:   4,
					UpdateAlternate: &alternateUpdate{relativePath: "pool"},
					ExpectedError:   errAlternateAlreadyLinked,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Objects: []git.ObjectID{},
					},
					"member": {
						Objects:   []git.ObjectID{},
						Alternate: "../../pool/objects",
					},
				},
			},
		},
		{
			desc: "repository can't be linked concurrently multiple times",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				Begin{
					TransactionID:       4,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID:   3,
					UpdateAlternate: &alternateUpdate{relativePath: "pool"},
				},
				Commit{
					TransactionID:   4,
					UpdateAlternate: &alternateUpdate{relativePath: "pool"},
					ExpectedError:   errAlternateAlreadyLinked,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Objects: []git.ObjectID{},
					},
					"member": {
						Objects:   []git.ObjectID{},
						Alternate: "../../pool/objects",
					},
				},
			},
		},
		{
			desc: "repository without an alternate can't be disconnected",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "repository",
				},
				CreateRepository{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "repository",
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID:   2,
					UpdateAlternate: &alternateUpdate{},
					ExpectedError:   errNoAlternate,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					"repository": {
						Objects: []git.ObjectID{},
					},
				},
			},
		},
		{
			desc: "repository can't be disconnected concurrently multiple times",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
					Alternate:     "../../pool/objects",
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				Begin{
					TransactionID:       4,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID:   3,
					UpdateAlternate: &alternateUpdate{},
				},
				Commit{
					TransactionID:   4,
					UpdateAlternate: &alternateUpdate{},
					ExpectedError:   errNoAlternate,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Objects: []git.ObjectID{},
					},
					"member": {
						Objects: []git.ObjectID{},
					},
				},
			},
		},
		{
			desc: "reapplying alternate linking works",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 2,
				},
				CloseManager{},
				StartManager{
					Hooks: testTransactionHooks{
						BeforeStoreAppliedLSN: func(hookContext) {
							panic(errSimulatedCrash)
						},
					},
					ExpectedError: errSimulatedCrash,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				RepositoryAssertion{
					TransactionID: 3,
					Repositories: RepositoryStates{
						"member": {
							DefaultBranch: "refs/heads/main",
						},
					},
				},
				Commit{
					TransactionID:   3,
					UpdateAlternate: &alternateUpdate{relativePath: "pool"},
					ExpectedError:   ErrTransactionProcessingStopped,
				},
				AssertManager{
					ExpectedError: errSimulatedCrash,
				},
				StartManager{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Objects: []git.ObjectID{},
					},
					"member": {
						Objects:   []git.ObjectID{},
						Alternate: "../../pool/objects",
					},
				},
			},
		},
		{
			desc: "reapplying alternate disconnection works",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
					Alternate:     "../../pool/objects",
				},
				Commit{
					TransactionID: 2,
				},
				CloseManager{},
				StartManager{
					Hooks: testTransactionHooks{
						BeforeStoreAppliedLSN: func(hookContext) {
							panic(errSimulatedCrash)
						},
					},
					ExpectedError: errSimulatedCrash,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				RepositoryAssertion{
					TransactionID: 3,
					Repositories: RepositoryStates{
						"pool": {
							DefaultBranch: "refs/heads/main",
						},
						"member": {
							DefaultBranch: "refs/heads/main",
							Alternate:     "../../pool/objects",
						},
					},
				},
				Commit{
					TransactionID:   3,
					UpdateAlternate: &alternateUpdate{},
					ExpectedError:   ErrTransactionProcessingStopped,
				},
				AssertManager{
					ExpectedError: errSimulatedCrash,
				},
				StartManager{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Objects: []git.ObjectID{},
					},
					"member": {
						Objects: []git.ObjectID{},
					},
				},
			},
		},
		{
			desc: "point reference to an object in an alternate",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
					Packs: [][]byte{setup.Commits.First.Pack},
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
					Alternate:     "../../pool/objects",
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
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
					"member": {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
						Alternate: "../../pool/objects",
					},
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
				},
			},
		},
		{
			desc: "point reference to new object with dependencies in an alternate",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
					Packs: [][]byte{setup.Commits.First.Pack},
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
					Alternate:     "../../pool/objects",
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
					},
					QuarantinedPacks: [][]byte{setup.Commits.Second.Pack},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
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
					"member": {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
						Alternate: "../../pool/objects",
					},
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/3":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/3/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/3/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							// The pack should only contain the new object 'second' as the
							// rest of the objects exist in the alternate. We're still including
							// all unreachable objects in the logged pack until we can compute
							// the pack files dependencies.
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					),
					"/wal/3/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
				},
			},
		},
		{
			desc: "repository's alternate is automatically snapshotted",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
					Packs: [][]byte{setup.Commits.First.Pack},
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
					Alternate:     "../../pool/objects",
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 2,
				},
				RepositoryAssertion{
					TransactionID: 3,
					Repositories: RepositoryStates{
						"pool": {
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
						"member": {
							DefaultBranch: "refs/heads/main",
							Objects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
							},
							Alternate: "../../pool/objects",
						},
					},
				},
				Rollback{
					TransactionID: 3,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
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
					"member": {
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
						Alternate: "../../pool/objects",
					},
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
				},
			},
		},
		{
			desc: "multiple repositories can be included in transaction's snapshot",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "repository-1",
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
					RelativePath:        "repository-2",
					ExpectedSnapshotLSN: 1,
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
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        "repository-3",
					ExpectedSnapshotLSN: 2,
				},
				CreateRepository{
					TransactionID: 3,
					// Set repository-2 as repository-3's alternate to assert the
					// snasphotted repositories' alternates are also included.
					Alternate: "../../repository-2/objects",
				},
				Commit{
					TransactionID: 3,
				},
				Begin{
					TransactionID: 4,
					// Create a repository that is not snapshotted to assert it's not included
					// in the snapshot.
					RelativePath:        "repository-4",
					ExpectedSnapshotLSN: 3,
				},
				CreateRepository{
					TransactionID: 4,
				},
				Commit{
					TransactionID: 4,
				},
				Begin{
					TransactionID:            5,
					RelativePath:             "repository-1",
					SnapshottedRelativePaths: []string{"repository-3"},
					ExpectedSnapshotLSN:      4,
				},
				RepositoryAssertion{
					TransactionID: 5,
					Repositories: RepositoryStates{
						"repository-1": {
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
								"/": {Mode: umask.Mask(fs.ModeDir | perm.PrivateDir)},
								"/pre-receive": {
									Mode:    umask.Mask(fs.ModePerm),
									Content: []byte("hook content"),
								},
								"/private-dir":              {Mode: umask.Mask(fs.ModeDir | perm.PrivateDir)},
								"/private-dir/private-file": {Mode: umask.Mask(perm.PrivateFile), Content: []byte("private content")},
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
						"repository-3": {
							DefaultBranch: "refs/heads/main",
							Objects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
								setup.Commits.Third.OID,
							},
							Alternate: "../../repository-2/objects",
						},
					},
				},
				Rollback{
					TransactionID: 5,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(4).toProto(),
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
							"/": {Mode: umask.Mask(fs.ModeDir | perm.PrivateDir)},
							"/pre-receive": {
								Mode:    umask.Mask(fs.ModePerm),
								Content: []byte("hook content"),
							},
							"/private-dir":              {Mode: umask.Mask(fs.ModeDir | perm.PrivateDir)},
							"/private-dir/private-file": {Mode: umask.Mask(perm.PrivateFile), Content: []byte("private content")},
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
					"repository-3": {
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
							setup.Commits.Third.OID,
						},
						Alternate: "../../repository-2/objects",
					},
					"repository-4": {
						Objects: []git.ObjectID{},
					},
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/2":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/2/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/2/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
							setup.Commits.Third.OID,
						},
					),
					"/wal/2/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
				},
			},
		},
		{
			desc: "additional repository is included in the snapshot explicitly and implicitly",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
					Packs: [][]byte{setup.Commits.First.Pack},
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/branch": setup.Commits.Second.OID,
					},
					DefaultBranch: "refs/heads/branch",
					Packs: [][]byte{
						setup.Commits.First.Pack,
						setup.Commits.Second.Pack,
					},
					Alternate: "../../pool/objects",
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID: 3,
					RelativePath:  "member",
					// The pool is included explicitly here, and also implicitly through
					// the alternate link of member.
					SnapshottedRelativePaths: []string{"pool"},
					ExpectedSnapshotLSN:      2,
				},
				RepositoryAssertion{
					TransactionID: 3,
					Repositories: RepositoryStates{
						"pool": {
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
						"member": {
							DefaultBranch: "refs/heads/branch",
							References: &ReferencesState{
								LooseReferences: map[git.ReferenceName]git.ObjectID{
									"refs/heads/branch": setup.Commits.Second.OID,
								},
							},
							Objects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
							},
							Alternate: "../../pool/objects",
						},
					},
				},
				Rollback{
					TransactionID: 3,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
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
					"member": {
						DefaultBranch: "refs/heads/branch",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch": setup.Commits.Second.OID,
							},
						},
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
						Alternate: "../../pool/objects",
					},
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/2":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/2/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/2/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					),
					"/wal/2/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
				},
			},
		},
		{
			desc: "target repository is included in the snapshot explicitly and implicitly",
			steps: steps{
				RemoveRepository{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  "pool",
				},
				CreateRepository{
					TransactionID: 1,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
					Packs: [][]byte{setup.Commits.First.Pack},
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 2,
					References: map[git.ReferenceName]git.ObjectID{
						"refs/heads/branch": setup.Commits.Second.OID,
					},
					DefaultBranch: "refs/heads/branch",
					Packs: [][]byte{
						setup.Commits.First.Pack,
						setup.Commits.Second.Pack,
					},
					Alternate: "../../pool/objects",
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID: 3,
					// The pool is targeted, and also implicitly included through
					// the alternate link of member.
					RelativePath:             "pool",
					SnapshottedRelativePaths: []string{"member"},
					ExpectedSnapshotLSN:      2,
				},
				RepositoryAssertion{
					TransactionID: 3,
					Repositories: RepositoryStates{
						"pool": {
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
						"member": {
							DefaultBranch: "refs/heads/branch",
							References: &ReferencesState{
								LooseReferences: map[git.ReferenceName]git.ObjectID{
									"refs/heads/branch": setup.Commits.Second.OID,
								},
							},
							Objects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
							},
							Alternate: "../../pool/objects",
						},
					},
				},
				Rollback{
					TransactionID: 3,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
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
					"member": {
						DefaultBranch: "refs/heads/branch",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch": setup.Commits.Second.OID,
							},
						},
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
						Alternate: "../../pool/objects",
					},
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/1/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					),
					"/wal/1/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
					"/wal/2":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/2/objects.idx": indexFileDirectoryEntry(setup.Config),
					"/wal/2/objects.pack": packFileDirectoryEntry(
						setup.Config,
						[]git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					),
					"/wal/2/objects.rev": reverseIndexFileDirectoryEntry(setup.Config),
				},
			},
		},
		{
			desc: "non-git directories are not snapshotted",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					// Try to snapshot the parent directory, which is no a valid Git directory.
					RelativePath:  filepath.Dir(setup.RelativePath),
					ExpectedError: storage.InvalidGitDirectoryError{MissingEntry: "objects"},
				},
			},
		},
	}
}
