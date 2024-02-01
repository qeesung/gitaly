package storagemgr

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func generateHousekeepingPackRefsTests(t *testing.T, ctx context.Context, testPartitionID partitionID, relativePath string) []transactionTestCase {
	customSetup := func(t *testing.T, ctx context.Context, testPartitionID partitionID, relativePath string) testTransactionSetup {
		setup := setupTest(t, ctx, testPartitionID, relativePath)
		gittest.WriteRef(t, setup.Config, setup.RepositoryPath, "refs/heads/main", setup.Commits.First.OID)
		gittest.WriteRef(t, setup.Config, setup.RepositoryPath, "refs/heads/branch-1", setup.Commits.Second.OID)
		gittest.WriteRef(t, setup.Config, setup.RepositoryPath, "refs/heads/branch-2", setup.Commits.Third.OID)

		gittest.WriteTag(t, setup.Config, setup.RepositoryPath, "v1.0.0", setup.Commits.Diverging.OID.Revision())
		annotatedTag := gittest.WriteTag(t, setup.Config, setup.RepositoryPath, "v2.0.0", setup.Commits.Diverging.OID.Revision(), gittest.WriteTagConfig{
			Message: "annotated tag",
		})
		setup.AnnotatedTags = append(setup.AnnotatedTags, testTransactionTag{
			Name: "v2.0.0",
			OID:  annotatedTag,
		})

		return setup
	}
	setup := customSetup(t, ctx, testPartitionID, relativePath)
	lightweightTag := setup.Commits.Diverging.OID
	annotatedTag := setup.AnnotatedTags[0]

	directoryStateWithReferences := func(lsn LSN) testhelper.DirectoryState {
		return testhelper.DirectoryState{
			"/":    {Mode: fs.ModeDir | perm.PrivateDir},
			"/wal": {Mode: fs.ModeDir | perm.PrivateDir},
			// LSN is when a log entry is appended, it's different from transaction ID.
			fmt.Sprintf("/wal/%d", lsn):             {Mode: fs.ModeDir | perm.PrivateDir},
			fmt.Sprintf("/wal/%s/packed-refs", lsn): anyDirectoryEntryWithPerm(setup.Config, perm.SharedFile),
		}
	}

	defaultReferences := map[git.ReferenceName]git.ObjectID{
		"refs/heads/branch-1": setup.Commits.Second.OID,
		"refs/heads/branch-2": setup.Commits.Third.OID,
		"refs/heads/main":     setup.Commits.First.OID,
		"refs/tags/v1.0.0":    lightweightTag,
		"refs/tags/v2.0.0":    annotatedTag.OID,
	}

	return []transactionTestCase{
		{
			desc:        "run pack-refs on a repository without packed-refs",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunPackRefs{
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
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: directoryStateWithReferences(1),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-1": setup.Commits.Second.OID,
								"refs/heads/branch-2": setup.Commits.Third.OID,
								// But `main` in packed-refs file points to the first
								// commit.
								"refs/heads/main":  setup.Commits.First.OID,
								"refs/tags/v1.0.0": lightweightTag,
								"refs/tags/v2.0.0": annotatedTag.OID,
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								// It's shadowed by the loose reference.
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
					},
				},
			},
		},
		{
			desc:        "run pack-refs on a repository with an existing packed-refs",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						// Execute pack-refs command without going through transaction manager
						gittest.Exec(tb, cfg, "-C", repoPath, "pack-refs", "--all")

						// Add artifactual packed-refs.lock. The pack-refs task should ignore
						// the lock and move on.
						require.NoError(t, os.WriteFile(
							filepath.Join(repoPath, "packed-refs.lock"),
							[]byte{},
							perm.PrivateFile,
						))
						require.NoError(t, os.WriteFile(
							filepath.Join(repoPath, "packed-refs.new"),
							[]byte{},
							perm.PrivateFile,
						))
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":     {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
						"refs/heads/branch-3": {OldOID: gittest.DefaultObjectHash.ZeroOID, NewOID: setup.Commits.Diverging.OID},
					},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				RunPackRefs{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: directoryStateWithReferences(2),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-1": setup.Commits.Second.OID,
								"refs/heads/branch-2": setup.Commits.Third.OID,
								"refs/heads/branch-3": setup.Commits.Diverging.OID,
								"refs/heads/main":     setup.Commits.Second.OID,
								"refs/tags/v1.0.0":    lightweightTag,
								"refs/tags/v2.0.0":    annotatedTag.OID,
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
						},
					},
				},
			},
		},
		{
			desc:        "run pack-refs, all refs outside refs/heads and refs/tags are packed",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/keep-around/1":        {OldOID: gittest.DefaultObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
						"refs/merge-requests/1":     {OldOID: gittest.DefaultObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
						"refs/very/deep/nested/ref": {OldOID: gittest.DefaultObjectHash.ZeroOID, NewOID: setup.Commits.Third.OID},
					},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				RunPackRefs{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: directoryStateWithReferences(2),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-1":       setup.Commits.Second.OID,
								"refs/heads/branch-2":       setup.Commits.Third.OID,
								"refs/heads/main":           setup.Commits.First.OID,
								"refs/keep-around/1":        setup.Commits.First.OID,
								"refs/merge-requests/1":     setup.Commits.Second.OID,
								"refs/tags/v1.0.0":          lightweightTag,
								"refs/tags/v2.0.0":          annotatedTag.OID,
								"refs/very/deep/nested/ref": setup.Commits.Third.OID,
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
						},
					},
				},
			},
		},
		{
			desc:        "concurrent ref creation before pack-refs task is committed",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunPackRefs{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-3": {OldOID: gittest.DefaultObjectHash.ZeroOID, NewOID: setup.Commits.Diverging.OID},
						"refs/keep-around/1":  {OldOID: gittest.DefaultObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: directoryStateWithReferences(2),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-1": setup.Commits.Second.OID,
								"refs/heads/branch-2": setup.Commits.Third.OID,
								"refs/heads/main":     setup.Commits.First.OID,
								"refs/tags/v1.0.0":    lightweightTag,
								"refs/tags/v2.0.0":    annotatedTag.OID,
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								// Although ref creation commits beforehand, pack-refs
								// task is unaware of these new refs. It keeps them as
								// loose refs.
								"refs/heads/branch-3": setup.Commits.Diverging.OID,
								"refs/keep-around/1":  setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc:        "concurrent ref creation after pack-refs task is committed",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunPackRefs{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-3": {OldOID: gittest.DefaultObjectHash.ZeroOID, NewOID: setup.Commits.Diverging.OID},
						"refs/keep-around/1":  {OldOID: gittest.DefaultObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: directoryStateWithReferences(1),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-1": setup.Commits.Second.OID,
								"refs/heads/branch-2": setup.Commits.Third.OID,
								"refs/heads/main":     setup.Commits.First.OID,
								"refs/tags/v1.0.0":    lightweightTag,
								"refs/tags/v2.0.0":    annotatedTag.OID,
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								// pack-refs task is unaware of these new refs. It keeps
								// them as loose refs.
								"refs/heads/branch-3": setup.Commits.Diverging.OID,
								"refs/keep-around/1":  setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc:        "concurrent ref updates before pack-refs task is committed",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunPackRefs{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":     {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
						"refs/heads/branch-1": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.Third.OID},
						"refs/heads/branch-2": {OldOID: setup.Commits.Third.OID, NewOID: setup.Commits.Diverging.OID},
						"refs/tags/v1.0.0":    {OldOID: setup.Commits.Diverging.OID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: directoryStateWithReferences(2),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-1": setup.Commits.Second.OID, // Outdated
								"refs/heads/branch-2": setup.Commits.Third.OID,  // Outdated
								"refs/heads/main":     setup.Commits.First.OID,  // Outdated
								"refs/tags/v1.0.0":    lightweightTag,           // Outdated
								"refs/tags/v2.0.0":    annotatedTag.OID,         // Still up-to-date
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								// Updated refs shadow the ones in the packed-refs file.
								"refs/heads/main":     setup.Commits.Second.OID,
								"refs/heads/branch-1": setup.Commits.Third.OID,
								"refs/heads/branch-2": setup.Commits.Diverging.OID,
								"refs/tags/v1.0.0":    setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc:        "concurrent ref updates after pack-refs task is committed",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunPackRefs{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":     {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
						"refs/heads/branch-1": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.Third.OID},
						"refs/heads/branch-2": {OldOID: setup.Commits.Third.OID, NewOID: setup.Commits.Diverging.OID},
						"refs/tags/v1.0.0":    {OldOID: setup.Commits.Diverging.OID, NewOID: setup.Commits.First.OID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: directoryStateWithReferences(1),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-1": setup.Commits.Second.OID, // Outdated
								"refs/heads/branch-2": setup.Commits.Third.OID,  // Outdated
								"refs/heads/main":     setup.Commits.First.OID,  // Outdated
								"refs/tags/v1.0.0":    lightweightTag,           // Outdated
								"refs/tags/v2.0.0":    annotatedTag.OID,
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main":     setup.Commits.Second.OID,
								"refs/heads/branch-1": setup.Commits.Third.OID,
								"refs/heads/branch-2": setup.Commits.Diverging.OID,
								"refs/tags/v1.0.0":    setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc:        "concurrent ref deletion before pack-refs is committed",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunPackRefs{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-1": {OldOID: setup.Commits.Second.OID, NewOID: gittest.DefaultObjectHash.ZeroOID},
						"refs/tags/v1.0.0":    {OldOID: lightweightTag, NewOID: gittest.DefaultObjectHash.ZeroOID},
					},
				},
				Commit{
					TransactionID: 1,
					ExpectedError: errPackRefsConflictRefDeletion,
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
							// Empty packed-refs. It means the pack-refs task is not
							// executed.
							PackedReferences: nil,
							// Deleted refs went away.
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-2": setup.Commits.Third.OID,
								"refs/heads/main":     setup.Commits.First.OID,
								"refs/tags/v2.0.0":    annotatedTag.OID,
							},
						},
					},
				},
			},
		},
		{
			desc:        "concurrent ref deletion before pack-refs is committed",
			customSetup: customSetup,
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
				RunPackRefs{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 3,
				},
				Commit{
					TransactionID: 1,
					ExpectedError: errPackRefsConflictRefDeletion,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Repositories: RepositoryStates{
					relativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-1": setup.Commits.Second.OID,
								"refs/heads/branch-2": setup.Commits.Third.OID,
								"refs/tags/v1.0.0":    lightweightTag,
								"refs/tags/v2.0.0":    annotatedTag.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "concurrent ref deletion in other repository of a pool",
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
						"refs/heads/branch-1": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Begin{
					TransactionID:       4,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 3,
				},
				Begin{
					TransactionID:       5,
					RelativePath:        "pool",
					ExpectedSnapshotLSN: 3,
				},
				RunPackRefs{
					TransactionID: 5,
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-1": {OldOID: setup.Commits.First.OID, NewOID: gittest.DefaultObjectHash.ZeroOID},
					},
				},
				Commit{
					TransactionID: 5,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(5).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
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
					"/wal/5":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/5/packed-refs": anyDirectoryEntryWithPerm(setup.Config, perm.SharedFile),
				},
			},
		},
		{
			desc:        "concurrent ref deletion after pack-refs is committed",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunPackRefs{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-1": {OldOID: setup.Commits.Second.OID, NewOID: gittest.DefaultObjectHash.ZeroOID},
						"refs/tags/v1.0.0":    {OldOID: lightweightTag, NewOID: gittest.DefaultObjectHash.ZeroOID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: directoryStateWithReferences(1),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-2": setup.Commits.Third.OID,
								"refs/heads/main":     setup.Commits.First.OID,
								"refs/tags/v2.0.0":    annotatedTag.OID,
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
						},
					},
				},
			},
		},
		{
			desc: "empty directories are pruned after interrupted log application",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/empty-dir/parent/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
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
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				RunPackRefs{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 2,
					ExpectedError: ErrTransactionProcessingStopped,
				},
				AssertManager{
					ExpectedError: errSimulatedCrash,
				},
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						// Create the directory that was removed already by the pack-refs task.
						// This way we can assert reapplying the log entry will successfully remove
						// the all directories even if the reference deletion was already applied.
						require.NoError(tb, os.MkdirAll(
							filepath.Join(storagePath, setup.RelativePath, "refs", "heads", "empty-dir"),
							perm.PrivateDir,
						))
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
					"/wal/2":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/2/packed-refs": anyDirectoryEntryWithPerm(setup.Config, perm.SharedFile),
				},
				Repositories: RepositoryStates{
					relativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/empty-dir/parent/main": setup.Commits.First.OID,
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
						},
					},
				},
			},
		},
		{
			desc:        "housekeeping fails in read-only transaction",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
					ReadOnly:     true,
				},
				RunPackRefs{},
				Commit{
					ExpectedError: errReadOnlyHousekeeping,
				},
			},
			expectedState: StateAssertion{
				Repositories: RepositoryStates{
					relativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: defaultReferences,
						},
					},
				},
			},
		},
		{
			desc:        "housekeeping fails when there are other updates in transaction",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				RunPackRefs{},
				Commit{
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
					ExpectedError: errHousekeepingConflictOtherUpdates,
				},
			},
			expectedState: StateAssertion{
				Repositories: RepositoryStates{
					relativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: defaultReferences,
						},
					},
				},
			},
		},
		{
			desc:        "housekeeping transaction runs concurrently with another housekeeping transaction",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunPackRefs{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				RunPackRefs{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 1,
				},
				Commit{
					TransactionID: 2,
					ExpectedError: errHousekeepingConflictConcurrent,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Directory: directoryStateWithReferences(1),
				Repositories: RepositoryStates{
					relativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: defaultReferences,
							LooseReferences:  map[git.ReferenceName]git.ObjectID{},
						},
					},
				},
			},
		},
		{
			desc: "housekeeping transaction runs after another housekeeping transaction in other repository of a pool",
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
				Begin{
					TransactionID:       4,
					RelativePath:        "pool",
					ExpectedSnapshotLSN: 2,
				},
				RunPackRefs{
					TransactionID: 3,
				},
				RunPackRefs{
					TransactionID: 4,
				},
				Commit{
					TransactionID: 3,
				},
				Commit{
					TransactionID: 4,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(4).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Objects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
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
					"/wal/3":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/3/packed-refs": anyDirectoryEntryWithPerm(setup.Config, perm.SharedFile),
					"/wal/4":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/4/packed-refs": anyDirectoryEntryWithPerm(setup.Config, perm.SharedFile),
				},
			},
		},
		{
			desc:        "housekeeping transaction runs after another housekeeping transaction",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunPackRefs{
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
				RunPackRefs{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 2,
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
					"/wal/1/packed-refs": anyDirectoryEntryWithPerm(setup.Config, perm.SharedFile),
					"/wal/2":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/2/packed-refs": anyDirectoryEntryWithPerm(setup.Config, perm.SharedFile),
				},
				Repositories: RepositoryStates{
					relativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: defaultReferences,
							LooseReferences:  map[git.ReferenceName]git.ObjectID{},
						},
					},
				},
			},
		},
		{
			desc:        "housekeeping transaction runs concurrently with a repository deletion",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunPackRefs{
					TransactionID: 1,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID:    2,
					DeleteRepository: true,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				CreateRepository{
					TransactionID: 3,
				},
				Commit{
					TransactionID: 3,
				},
				Commit{
					TransactionID: 1,
					ExpectedError: errConflictRepositoryDeletion,
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
					relativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
						},
						Objects: []git.ObjectID{},
					},
				},
			},
		},
	}
}

// A shortcut to return a digest hash correspondingly to the current testing object hash format. Names of
// packfiles have digest hashes. The following tests examine on-disk packfiles of WAL log entry's luggage and
// the destination repository. We could use an incremental names for attached packfiles of the log entry,
// eventually, they will be applied into the repository. The actual on-disk packfiles should match the their
// IDs, which are specified in the *.idx files. So, it's essential to include the digest hash in the tests
// although it's annoying to switch between different hash formats.
func hash(tb testing.TB, sha1 string, sha256 string) string {
	return gittest.ObjectHashDependent(tb, map[string]string{
		git.ObjectHashSHA1.Format:   sha1,
		git.ObjectHashSHA256.Format: sha256,
	})
}

type walDirectoryState struct {
	lsn                 LSN
	includePackfiles    []string
	includeMultiIndexes []string
	includeCommitGraphs []string
	includeObjects      []git.ObjectID
}

func generateDirectoryState(cfg config.Cfg, stats []*walDirectoryState) testhelper.DirectoryState {
	state := testhelper.DirectoryState{}
	if len(stats) == 0 {
		return state
	}
	state["/"] = testhelper.DirectoryEntry{Mode: fs.ModeDir | perm.PrivateDir}
	state["/wal"] = testhelper.DirectoryEntry{Mode: fs.ModeDir | perm.PrivateDir}
	for _, stat := range stats {
		walDir := fmt.Sprintf("/wal/%d", stat.lsn)
		state[walDir] = testhelper.DirectoryEntry{Mode: fs.ModeDir | perm.PrivateDir}
		for _, packfile := range stat.includePackfiles {
			state[filepath.Join(walDir, packfile+".pack")] = anyDirectoryEntry(cfg)
			state[filepath.Join(walDir, packfile+".idx")] = anyDirectoryEntry(cfg)
			state[filepath.Join(walDir, packfile+".rev")] = anyDirectoryEntry(cfg)
		}
		for _, index := range stat.includeMultiIndexes {
			state[filepath.Join(walDir, "multi-pack-index")] = anyDirectoryEntryWithPerm(cfg, perm.SharedFile)
			state[filepath.Join(walDir, index+".bitmap")] = anyDirectoryEntry(cfg)
		}
		for _, graph := range stat.includeCommitGraphs {
			state[filepath.Join(walDir, "commit-graphs")] = testhelper.DirectoryEntry{Mode: fs.ModeDir | perm.PrivateDir}
			state[filepath.Join(walDir, "commit-graphs", "commit-graph-chain")] = anyDirectoryEntry(cfg)
			state[filepath.Join(walDir, "commit-graphs", graph+".graph")] = anyDirectoryEntry(cfg)
		}
		if len(stat.includeObjects) != 0 {
			state[filepath.Join(walDir, "objects.idx")] = indexFileDirectoryEntry(cfg)
			state[filepath.Join(walDir, "objects.rev")] = reverseIndexFileDirectoryEntry(cfg)
			state[filepath.Join(walDir, "objects.pack")] = packFileDirectoryEntry(cfg, stat.includeObjects)
		}
	}
	return state
}

// generateHousekeepingRepackingStrategyTests returns a set of tests which run repacking with different strategies and
// settings.
func generateHousekeepingRepackingStrategyTests(t *testing.T, ctx context.Context, testPartitionID partitionID, relativePath string) []transactionTestCase {
	customSetup := func(t *testing.T, ctx context.Context, testPartitionID partitionID, relativePath string) testTransactionSetup {
		setup := setupTest(t, ctx, testPartitionID, relativePath)
		gittest.WriteRef(t, setup.Config, setup.RepositoryPath, "refs/heads/main", setup.Commits.Third.OID)
		gittest.WriteRef(t, setup.Config, setup.RepositoryPath, "refs/heads/branch", setup.Commits.Diverging.OID)
		setup.Commits.Unreachable = testTransactionCommit{
			OID: gittest.WriteCommit(t, setup.Config, setup.RepositoryPath, gittest.WithParents(setup.Commits.Second.OID), gittest.WithMessage("unreachable commit")),
		}
		setup.Commits.Orphan = testTransactionCommit{
			OID: gittest.WriteCommit(t, setup.Config, setup.RepositoryPath, gittest.WithParents(), gittest.WithMessage("orphan commit")),
		}
		return setup
	}
	setup := customSetup(t, ctx, testPartitionID, relativePath)

	defaultReferences := map[git.ReferenceName]git.ObjectID{
		"refs/heads/main":   setup.Commits.Third.OID,
		"refs/heads/branch": setup.Commits.Diverging.OID,
	}
	defaultReachableObjects := []git.ObjectID{
		gittest.DefaultObjectHash.EmptyTreeOID,
		setup.Commits.First.OID,
		setup.Commits.Second.OID,
		setup.Commits.Third.OID,
		setup.Commits.Diverging.OID,
	}
	return []transactionTestCase{
		{
			desc:        "run repacking (IncrementalWithUnreachable)",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-C", repoPath, "repack", "-ad")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyIncrementalWithUnreachable,
					},
				},
				Commit{
					TransactionID: 1,
					ExpectedError: errRepackNotSupportedStrategy,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							// Loose objects stay intact.
							LooseObjects: []git.ObjectID{
								setup.Commits.Orphan.OID,
								setup.Commits.Unreachable.OID,
							},
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-5f624d294fda1b8df86f1c286c6a66757b44126e",
									"pack-c57ed22f16c0a35f04febe26eac0fe8974b2b4ab3469d1ece0bc2983588ad44e",
								): {
									Objects:         defaultReachableObjects,
									HasBitmap:       true,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   false,
						},
					},
				},
				Directory: testhelper.DirectoryState{
					"/":    {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal": {Mode: fs.ModeDir | perm.PrivateDir},
				},
			},
		},
		{
			desc:        "run repacking (FullWithUnreachable) on a repository with an existing packfile",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-C", repoPath, "repack", "-ad")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyFullWithUnreachable,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							// Unreachable objects are packed.
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-689b1fa746246c50a8b0f3469a06c7ae68af9926",
									"pack-3506da99c69e8bbb4e3122636a486ffcc3506f08d24426823a2a394a7fb16b94",
								): {
									Objects: append(defaultReachableObjects,
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									),
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
							HasCommitGraphs:   false,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-689b1fa746246c50a8b0f3469a06c7ae68af9926",
							"pack-3506da99c69e8bbb4e3122636a486ffcc3506f08d24426823a2a394a7fb16b94",
						)},
						includeMultiIndexes: []string{hash(t,
							"multi-pack-index-dece3dfef114aa668c61339e0d4eb081af62ce68",
							"multi-pack-index-bf9ee4098624aeb3fae4990d943443f5759d6d63c8cca686b19fb48e3c6a6f25",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking (FullWithUnreachable) on a repository without any packfile",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyFullWithUnreachable,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							// Interestingly, loose unreachable objects stay untouched!
							LooseObjects: []git.ObjectID{
								setup.Commits.Orphan.OID,
								setup.Commits.Unreachable.OID,
							},
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-a81cd79eb9f32ce0afbdc15dec51c7141029e54c",
									"pack-ce649b013f4191c500c7c4de5fe407120314c83354944e5639bf1a33a2c94110",
								): {
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
							HasCommitGraphs:   false,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-a81cd79eb9f32ce0afbdc15dec51c7141029e54c",
							"pack-ce649b013f4191c500c7c4de5fe407120314c83354944e5639bf1a33a2c94110",
						)},
						includeMultiIndexes: []string{hash(t,
							"multi-pack-index-f881063fbb14e481b5be5619df02c9874dbe5d3b",
							"multi-pack-index-67d1f13534c85393277dc006444eee9b6670b6f1554faa43e051fa9402efa3a8",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking (Geometric) on a repository without any packfile",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyGeometric,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-e1b234fb89567714fc382281c7f89a363f4ac115",
									"pack-d7214dae50142c99e75bf21d679b8cc14bc5d82cdb84dc23f39120101a6ed5e9",
								): {
									Objects: append(defaultReachableObjects,
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									),
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
							HasCommitGraphs:   true,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-e1b234fb89567714fc382281c7f89a363f4ac115",
							"pack-d7214dae50142c99e75bf21d679b8cc14bc5d82cdb84dc23f39120101a6ed5e9",
						)},
						includeMultiIndexes: []string{hash(t,
							"multi-pack-index-70fc88df37859b5f9c0d68a6b4ed42e9a6d3819e",
							"multi-pack-index-d55aca104a7164aa65e984e53ebe2633c515eba475a68dccc982156fefaf9c51",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-5eddc89b8217451ecd51182f91ddf6f58b20f0f7",
							"graph-d7a6f93863d026b02376bf869ab4fa23a7cd6bdbc013543741352b574cc19606",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking (Geometric) on a repository having an existing packfile",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-C", repoPath, "repack", "-ad")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyGeometric,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								// Initial packfile.
								hash(t,
									"pack-5f624d294fda1b8df86f1c286c6a66757b44126e",
									"pack-c57ed22f16c0a35f04febe26eac0fe8974b2b4ab3469d1ece0bc2983588ad44e",
								): {
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
								// New packfile that contains unreachable objects. This
								// is a co-incident, it follows the geometric
								// progression.
								hash(t,
									"pack-f20a6e68adae9088db85f994838091d53fbaf608",
									"pack-aa6d40f5f019492a7cc11291ab68666ae7ac2a23e66762905581c44523bb12bd",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									},
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
							HasCommitGraphs:   true,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-f20a6e68adae9088db85f994838091d53fbaf608",
							"pack-aa6d40f5f019492a7cc11291ab68666ae7ac2a23e66762905581c44523bb12bd",
						)},
						includeMultiIndexes: []string{hash(t,
							"multi-pack-index-8b9315908033879678e7a6d7ff16d8cf3f419181",
							"multi-pack-index-c2be71e50a69e0706c7818e608144ad07eca4425a4d6446d4b4f1a667f756500",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-5eddc89b8217451ecd51182f91ddf6f58b20f0f7",
							"graph-d7a6f93863d026b02376bf869ab4fa23a7cd6bdbc013543741352b574cc19606",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking (FullWithCruft) on a repository having all loose objects",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyFullWithCruft,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							// Interestingly, loose unreachable objects stay untouched!
							LooseObjects: []git.ObjectID{
								setup.Commits.Orphan.OID,
								setup.Commits.Unreachable.OID,
							},
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-a81cd79eb9f32ce0afbdc15dec51c7141029e54c",
									"pack-ce649b013f4191c500c7c4de5fe407120314c83354944e5639bf1a33a2c94110",
								): {
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
							HasCommitGraphs:   true,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-a81cd79eb9f32ce0afbdc15dec51c7141029e54c",
							"pack-ce649b013f4191c500c7c4de5fe407120314c83354944e5639bf1a33a2c94110",
						)},
						includeMultiIndexes: []string{hash(t,
							"multi-pack-index-f881063fbb14e481b5be5619df02c9874dbe5d3b",
							"multi-pack-index-67d1f13534c85393277dc006444eee9b6670b6f1554faa43e051fa9402efa3a8",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-5eddc89b8217451ecd51182f91ddf6f58b20f0f7",
							"graph-d7a6f93863d026b02376bf869ab4fa23a7cd6bdbc013543741352b574cc19606",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking (FullWithCruft) on a repository whose objects are packed",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-C", repoPath, "repack", "-adl")
						gittest.Exec(tb, cfg, "-C", repoPath, "repack", "-adl", "--keep-unreachable")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyFullWithCruft,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							// Unreachable objects are pruned.
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-a81cd79eb9f32ce0afbdc15dec51c7141029e54c",
									"pack-ce649b013f4191c500c7c4de5fe407120314c83354944e5639bf1a33a2c94110",
								): {
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
							HasCommitGraphs:   true,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-a81cd79eb9f32ce0afbdc15dec51c7141029e54c",
							"pack-ce649b013f4191c500c7c4de5fe407120314c83354944e5639bf1a33a2c94110",
						)},
						includeMultiIndexes: []string{hash(t,
							"multi-pack-index-f881063fbb14e481b5be5619df02c9874dbe5d3b",
							"multi-pack-index-67d1f13534c85393277dc006444eee9b6670b6f1554faa43e051fa9402efa3a8",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-5eddc89b8217451ecd51182f91ddf6f58b20f0f7",
							"graph-d7a6f93863d026b02376bf869ab4fa23a7cd6bdbc013543741352b574cc19606",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking (FullWithCruft) on a repository having both packfile and loose unreachable objects",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-C", repoPath, "repack", "-adl")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyFullWithCruft,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							// Interestingly, loose unreachable objects stay untouched!
							LooseObjects: []git.ObjectID{
								setup.Commits.Orphan.OID,
								setup.Commits.Unreachable.OID,
							},
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-a81cd79eb9f32ce0afbdc15dec51c7141029e54c",
									"pack-ce649b013f4191c500c7c4de5fe407120314c83354944e5639bf1a33a2c94110",
								): {
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
							HasCommitGraphs:   true,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-a81cd79eb9f32ce0afbdc15dec51c7141029e54c",
							"pack-ce649b013f4191c500c7c4de5fe407120314c83354944e5639bf1a33a2c94110",
						)},
						includeMultiIndexes: []string{hash(t,
							"multi-pack-index-f881063fbb14e481b5be5619df02c9874dbe5d3b",
							"multi-pack-index-67d1f13534c85393277dc006444eee9b6670b6f1554faa43e051fa9402efa3a8",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-5eddc89b8217451ecd51182f91ddf6f58b20f0f7",
							"graph-d7a6f93863d026b02376bf869ab4fa23a7cd6bdbc013543741352b574cc19606",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking without bitmap and multi-pack-index",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-c", "repack.writeBitmaps=false", "-C", repoPath, "repack", "-ad")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyGeometric,
						WriteBitmap:         false,
						WriteMultiPackIndex: false,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-5f624d294fda1b8df86f1c286c6a66757b44126e",
									"pack-c57ed22f16c0a35f04febe26eac0fe8974b2b4ab3469d1ece0bc2983588ad44e",
								): {
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
								hash(t,
									"pack-f20a6e68adae9088db85f994838091d53fbaf608",
									"pack-aa6d40f5f019492a7cc11291ab68666ae7ac2a23e66762905581c44523bb12bd",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									},
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   true,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-f20a6e68adae9088db85f994838091d53fbaf608",
							"pack-aa6d40f5f019492a7cc11291ab68666ae7ac2a23e66762905581c44523bb12bd",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-5eddc89b8217451ecd51182f91ddf6f58b20f0f7",
							"graph-d7a6f93863d026b02376bf869ab4fa23a7cd6bdbc013543741352b574cc19606",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking (Geometric) on a repository having existing commit-graphs",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-c", "repack.writeBitmaps=false", "-C", repoPath, "repack", "-ad")
						gittest.Exec(tb, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--changed-paths", "--size-multiple=4", "--split=replace")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyGeometric,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-5f624d294fda1b8df86f1c286c6a66757b44126e",
									"pack-c57ed22f16c0a35f04febe26eac0fe8974b2b4ab3469d1ece0bc2983588ad44e",
								): {
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
								hash(t,
									"pack-f20a6e68adae9088db85f994838091d53fbaf608",
									"pack-aa6d40f5f019492a7cc11291ab68666ae7ac2a23e66762905581c44523bb12bd",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									},
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   true,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-f20a6e68adae9088db85f994838091d53fbaf608",
							"pack-aa6d40f5f019492a7cc11291ab68666ae7ac2a23e66762905581c44523bb12bd",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-5eddc89b8217451ecd51182f91ddf6f58b20f0f7",
							"graph-d7a6f93863d026b02376bf869ab4fa23a7cd6bdbc013543741352b574cc19606",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking (FullWithUnreachable) on a repository having existing commit-graphs",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-c", "repack.writeBitmaps=false", "-C", repoPath, "repack", "-ad")
						gittest.Exec(tb, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--changed-paths", "--size-multiple=4", "--split=replace")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithUnreachable,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-689b1fa746246c50a8b0f3469a06c7ae68af9926",
									"pack-3506da99c69e8bbb4e3122636a486ffcc3506f08d24426823a2a394a7fb16b94",
								): {
									Objects: append(defaultReachableObjects,
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									),
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   true,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-689b1fa746246c50a8b0f3469a06c7ae68af9926",
							"pack-3506da99c69e8bbb4e3122636a486ffcc3506f08d24426823a2a394a7fb16b94",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking (FullWithCruft) on a repository having existing commit-graphs",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-C", repoPath, "repack", "-adl")
						gittest.Exec(tb, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--changed-paths", "--size-multiple=4", "--split=replace")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyFullWithCruft,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							// Interestingly, loose unreachable objects stay untouched!
							LooseObjects: []git.ObjectID{
								setup.Commits.Orphan.OID,
								setup.Commits.Unreachable.OID,
							},
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-a81cd79eb9f32ce0afbdc15dec51c7141029e54c",
									"pack-ce649b013f4191c500c7c4de5fe407120314c83354944e5639bf1a33a2c94110",
								): {
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
							HasCommitGraphs:   true,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-a81cd79eb9f32ce0afbdc15dec51c7141029e54c",
							"pack-ce649b013f4191c500c7c4de5fe407120314c83354944e5639bf1a33a2c94110",
						)},
						includeMultiIndexes: []string{hash(t,
							"multi-pack-index-f881063fbb14e481b5be5619df02c9874dbe5d3b",
							"multi-pack-index-67d1f13534c85393277dc006444eee9b6670b6f1554faa43e051fa9402efa3a8",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-5eddc89b8217451ecd51182f91ddf6f58b20f0f7",
							"graph-d7a6f93863d026b02376bf869ab4fa23a7cd6bdbc013543741352b574cc19606",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking on a repository having monolithic commit-graph file",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-c", "repack.writeBitmaps=false", "-C", repoPath, "repack", "-ad")
						gittest.Exec(tb, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--changed-paths")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyGeometric,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-5f624d294fda1b8df86f1c286c6a66757b44126e",
									"pack-c57ed22f16c0a35f04febe26eac0fe8974b2b4ab3469d1ece0bc2983588ad44e",
								): {
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
								hash(t,
									"pack-f20a6e68adae9088db85f994838091d53fbaf608",
									"pack-aa6d40f5f019492a7cc11291ab68666ae7ac2a23e66762905581c44523bb12bd",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									},
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   true,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-f20a6e68adae9088db85f994838091d53fbaf608",
							"pack-aa6d40f5f019492a7cc11291ab68666ae7ac2a23e66762905581c44523bb12bd",
						)},
					},
				}),
			},
		},
		{
			desc:        "run repacking twice with the same setting",
			customSetup: customSetup,
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-C", repoPath, "repack", "-adl")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyFullWithUnreachable,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				RunRepack{
					TransactionID: 2,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyFullWithUnreachable,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
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
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-689b1fa746246c50a8b0f3469a06c7ae68af9926",
									"pack-3506da99c69e8bbb4e3122636a486ffcc3506f08d24426823a2a394a7fb16b94",
								): {
									Objects: append(defaultReachableObjects,
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									),
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
							HasCommitGraphs:   false,
						},
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includePackfiles: []string{hash(t,
							"pack-689b1fa746246c50a8b0f3469a06c7ae68af9926",
							"pack-3506da99c69e8bbb4e3122636a486ffcc3506f08d24426823a2a394a7fb16b94",
						)},
						includeMultiIndexes: []string{hash(t,
							"multi-pack-index-dece3dfef114aa668c61339e0d4eb081af62ce68",
							"multi-pack-index-bf9ee4098624aeb3fae4990d943443f5759d6d63c8cca686b19fb48e3c6a6f25",
						)},
					},
					{
						lsn: 2,
					},
				}),
			},
		},
		{
			desc:        "run repacking in the same transaction including other changes",
			customSetup: customSetup,
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy:            housekeeping.RepackObjectsStrategyFullWithUnreachable,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
					ExpectedError: errHousekeepingConflictOtherUpdates,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects:      append(defaultReachableObjects, setup.Commits.Unreachable.OID, setup.Commits.Orphan.OID),
							Packfiles:         map[string]*PackfileState{},
							HasMultiPackIndex: false,
							HasCommitGraphs:   false,
						},
					},
				},
				Directory: testhelper.DirectoryState{
					"/":    {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal": {Mode: fs.ModeDir | perm.PrivateDir},
				},
			},
		},
	}
}

// generateHousekeepingRepackingConcurrentTests returns a set of tests which run repacking before, after, or alongside
// with other transactions.
func generateHousekeepingRepackingConcurrentTests(t *testing.T, ctx context.Context, setup testTransactionSetup) []transactionTestCase {
	return []transactionTestCase{
		{
			desc: "run repacking on an empty repository",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{lsn: 1},
				}),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
						},
						Packfiles: &PackfilesState{
							Packfiles:       map[string]*PackfileState{},
							HasCommitGraphs: false,
						},
					},
				},
			},
		},
		{
			desc: "run repacking after some changes including both reachable and unreachable objects",
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
						setup.Commits.Diverging.Pack, // This commit is not reachable
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Diverging.OID},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				RunRepack{
					TransactionID: 2,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
							setup.Commits.Diverging.OID,
						},
					},
					{
						lsn: 2,
						includePackfiles: []string{hash(t,
							"pack-98be7bb46e97ddbe7e3093e0cc5bca60f37f9b09",
							"pack-53630df54431a48f6d87f1bbe0d054327f8eb1964f813de1821d15bc5dcb1621",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-8cd59940f998ecb90f7935b6b7adc8df46d9174e",
							"graph-8cbbd20f75bc45c5337718fe4ab8498e1ce7524e87d6bd7fc9581bc08c119562",
						)},
					},
				}),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-98be7bb46e97ddbe7e3093e0cc5bca60f37f9b09",
									"pack-53630df54431a48f6d87f1bbe0d054327f8eb1964f813de1821d15bc5dcb1621",
								): {
									// Diverging commit is gone.
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasCommitGraphs: true,
						},
					},
				},
			},
		},
		{
			desc: "run repacking before another transaction that produce new packfiles",
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
						setup.Commits.Diverging.Pack, // This commit is not reachable
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Diverging.OID},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				RunRepack{
					TransactionID: 2,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.Third.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Third.Pack,
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
							setup.Commits.Diverging.OID,
						},
					},
					{
						lsn: 2,
						includePackfiles: []string{hash(t,
							"pack-98be7bb46e97ddbe7e3093e0cc5bca60f37f9b09",
							"pack-53630df54431a48f6d87f1bbe0d054327f8eb1964f813de1821d15bc5dcb1621",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-8cd59940f998ecb90f7935b6b7adc8df46d9174e",
							"graph-8cbbd20f75bc45c5337718fe4ab8498e1ce7524e87d6bd7fc9581bc08c119562",
						)},
					},
					{
						lsn: 3,
						includeObjects: []git.ObjectID{
							setup.Commits.Third.OID,
						},
					},
				}),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Third.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-98be7bb46e97ddbe7e3093e0cc5bca60f37f9b09",
									"pack-53630df54431a48f6d87f1bbe0d054327f8eb1964f813de1821d15bc5dcb1621",
								): {
									// Diverging commit is gone.
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
								hash(t,
									"pack-529ec37accbc126425efe69abdf91153411532a6",
									"pack-895b4eade6c459f47a382a0d637ef1ce34a661c76f003c7d7a38a7420e3afc69",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Third.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasCommitGraphs: true,
						},
					},
				},
			},
		},
		{
			desc: "run repacking concurrently with another transaction that produce new packfiles",
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
						setup.Commits.Diverging.Pack,
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Diverging.OID},
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
				RunRepack{
					TransactionID: 2,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.Third.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Third.Pack,
					},
				},
				Commit{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
							setup.Commits.Diverging.OID,
						},
					},
					{
						lsn: 2,
						includeObjects: []git.ObjectID{
							setup.Commits.Third.OID,
						},
					},
					{
						lsn: 3,
						includePackfiles: []string{hash(t,
							"pack-98be7bb46e97ddbe7e3093e0cc5bca60f37f9b09",
							"pack-53630df54431a48f6d87f1bbe0d054327f8eb1964f813de1821d15bc5dcb1621",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-8cd59940f998ecb90f7935b6b7adc8df46d9174e",
							"graph-8cbbd20f75bc45c5337718fe4ab8498e1ce7524e87d6bd7fc9581bc08c119562",
						)},
					},
				}),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Third.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-98be7bb46e97ddbe7e3093e0cc5bca60f37f9b09",
									"pack-53630df54431a48f6d87f1bbe0d054327f8eb1964f813de1821d15bc5dcb1621",
								): {
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
								hash(t,
									"pack-529ec37accbc126425efe69abdf91153411532a6",
									"pack-895b4eade6c459f47a382a0d637ef1ce34a661c76f003c7d7a38a7420e3afc69",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Third.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasCommitGraphs: true,
						},
					},
				},
			},
		},
		{
			desc: "run repacking concurrently with another transaction that points to a survived object",
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
						setup.Commits.Diverging.Pack,
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Diverging.OID},
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
				RunRepack{
					TransactionID: 2,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
							setup.Commits.Diverging.OID,
						},
					},
					{
						lsn: 3,
						includePackfiles: []string{hash(t,
							"pack-98be7bb46e97ddbe7e3093e0cc5bca60f37f9b09",
							"pack-53630df54431a48f6d87f1bbe0d054327f8eb1964f813de1821d15bc5dcb1621",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-8cd59940f998ecb90f7935b6b7adc8df46d9174e",
							"graph-8cbbd20f75bc45c5337718fe4ab8498e1ce7524e87d6bd7fc9581bc08c119562",
						)},
					},
				}),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-98be7bb46e97ddbe7e3093e0cc5bca60f37f9b09",
									"pack-53630df54431a48f6d87f1bbe0d054327f8eb1964f813de1821d15bc5dcb1621",
								): {
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasCommitGraphs: true,
						},
					},
				},
			},
		},
		{
			desc: "run repacking that spans through multiple transactions",
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
					QuarantinedPacks: [][]byte{
						setup.Commits.First.Pack,
					},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				RunRepack{
					TransactionID: 2,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithCruft,
					},
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Second.Pack,
					},
				},
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.Third.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Third.Pack,
					},
				},
				Begin{
					TransactionID:       5,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 3,
				},
				Commit{
					TransactionID: 5,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Third.OID, NewOID: setup.Commits.Diverging.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Diverging.Pack,
					},
				},
				Commit{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(5).toProto(),
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					},
					{
						lsn: 2,
						includeObjects: []git.ObjectID{
							setup.Commits.Second.OID,
						},
					},
					{
						lsn: 3,
						includeObjects: []git.ObjectID{
							setup.Commits.Third.OID,
						},
					},
					{
						lsn: 4,
						includeObjects: []git.ObjectID{
							setup.Commits.Diverging.OID,
						},
					},
					{
						lsn: 5,
					},
				}),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Diverging.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-452292f7e0c6bcca1b42c53aaac4537416b5dbb9",
									"pack-735ad245db57a16c41525c9101c42594d090c7021b51aa12d9104a4eea4223c5",
								): {
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
									},
									HasReverseIndex: true,
								},
								hash(t,
									"pack-407172c9edc9b3cef89f8fb341262155b6b401ae",
									"pack-8c9a31ee3c6493a1883f96fe629925b6f94c00d810eb6c80d5e2502fba646d3a",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Diverging.OID,
									},
									HasReverseIndex: true,
								},
								hash(t,
									"pack-529ec37accbc126425efe69abdf91153411532a6",
									"pack-895b4eade6c459f47a382a0d637ef1ce34a661c76f003c7d7a38a7420e3afc69",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Third.OID,
									},
									HasReverseIndex: true,
								},
								hash(t,
									"pack-5a422f6c469963ffa026bf15cfd151751fba6e5f",
									"pack-3ed0b733b17d82f87a350b856f7fd6d6a781d85c5d8d36fad64b459124444f11",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
						},
					},
				},
			},
		},
		{
			desc: "run repacking (FullWithUnreachable) concurrently with another transaction pointing new reference to packed objects",
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
						"refs/heads/main":   {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
						"refs/heads/branch": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
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
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch": {OldOID: setup.Commits.Second.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				RunRepack{
					TransactionID: 3,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithUnreachable,
					},
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
				},
				Commit{
					TransactionID: 3,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(4).toProto(),
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					},
					{
						lsn: 4,
						includePackfiles: []string{hash(t,
							"pack-df5e3e230b167b4ce31a30f389e0f1908ae40f2b",
							"pack-6a4d9d6b54438754effb555adec435cd9031a01cba7515bdf8b73a0e2714c6ff",
						)},
					},
				}),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-df5e3e230b167b4ce31a30f389e0f1908ae40f2b",
									"pack-6a4d9d6b54438754effb555adec435cd9031a01cba7515bdf8b73a0e2714c6ff",
								): {
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasCommitGraphs: false,
						},
					},
				},
			},
		},
		{
			desc: "run repacking (Geometric) concurrently with another transaction pointing new reference to packed objects",
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
						"refs/heads/main":   {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
						"refs/heads/branch": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
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
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch": {OldOID: setup.Commits.Second.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				RunRepack{
					TransactionID: 3,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyGeometric,
					},
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
				},
				Commit{
					TransactionID: 3,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(4).toProto(),
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					},
					{
						// No new packfiles
						lsn: 4,
					},
				}),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-4274682fcb6a4dbb1a59ba7dd8577402e61ccbd2",
									"pack-8ebabff3c37210ed37c4343255992f62a2ce113f7fb11f757de3bca157379d40",
								): {
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasCommitGraphs: false,
						},
					},
				},
			},
		},
		{
			desc: "run repacking (FullWithCruft) concurrently with another transaction pointing new reference to pruned objects",
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
						"refs/heads/main":   {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
						"refs/heads/branch": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
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
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch": {OldOID: setup.Commits.Second.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				RunRepack{
					TransactionID: 3,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
				},
				Commit{
					TransactionID: 3,
					ExpectedError: errRepackConflictPrunedObject,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					},
				}),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-4274682fcb6a4dbb1a59ba7dd8577402e61ccbd2",
									"pack-8ebabff3c37210ed37c4343255992f62a2ce113f7fb11f757de3bca157379d40",
								): {
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasCommitGraphs: false,
						},
					},
				},
			},
		},
		{
			desc: "run repacking (FullWithUnreachable) on an alternate member",
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
						"refs/heads/branch": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Third.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Second.Pack,
						setup.Commits.Third.Pack,
					},
				},
				Begin{
					TransactionID:       4,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 3,
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch": {OldOID: setup.Commits.Third.OID, NewOID: setup.Commits.Second.OID},
					},
				},
				Begin{
					TransactionID:       5,
					RelativePath:        "pool",
					ExpectedSnapshotLSN: 4,
				},
				Commit{
					TransactionID: 5,
					QuarantinedPacks: [][]byte{
						setup.Commits.Second.Pack,
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Second.OID},
				},
				Begin{
					TransactionID:       6,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 5,
				},
				RunRepack{
					TransactionID: 6,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithUnreachable,
					},
				},
				Commit{
					TransactionID: 6,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(6).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							// First commit and its tree object.
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-452292f7e0c6bcca1b42c53aaac4537416b5dbb9",
									"pack-735ad245db57a16c41525c9101c42594d090c7021b51aa12d9104a4eea4223c5",
								): {
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
									},
									HasReverseIndex: true,
								},
								hash(t,
									"pack-5a422f6c469963ffa026bf15cfd151751fba6e5f",
									"pack-3ed0b733b17d82f87a350b856f7fd6d6a781d85c5d8d36fad64b459124444f11",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   false,
						},
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
					"member": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								// Packfile containing second commit (reachable) and
								// third commit (unreachable). Redundant objects in
								// quarantined packs are removed.
								hash(t,
									"pack-529ec37accbc126425efe69abdf91153411532a6",
									"pack-895b4eade6c459f47a382a0d637ef1ce34a661c76f003c7d7a38a7420e3afc69",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Third.OID,
									},
									HasReverseIndex: true,
								},
							},
							PooledObjects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								// Both member and pool have second commit. It's
								// deduplicated and the member inherits it from the
								// pool.
								setup.Commits.Second.OID,
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   false,
						},
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch": setup.Commits.Second.OID,
							},
						},
						Alternate: "../../pool/objects",
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					},
					{
						lsn: 3,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
							setup.Commits.Third.OID,
						},
					},
					{
						lsn: 5,
						includeObjects: []git.ObjectID{
							setup.Commits.Second.OID,
						},
					},
					{
						lsn: 6,
						includePackfiles: []string{hash(t,
							"pack-529ec37accbc126425efe69abdf91153411532a6",
							"pack-895b4eade6c459f47a382a0d637ef1ce34a661c76f003c7d7a38a7420e3afc69",
						)},
					},
				}),
			},
		},
		{
			desc: "run repacking (FullWithUnreachable) on an alternate pool",
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
					RelativePath:        "pool",
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Third.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Second.Pack,
						setup.Commits.Third.Pack,
					},
				},
				Begin{
					TransactionID:       4,
					RelativePath:        "pool",
					ExpectedSnapshotLSN: 3,
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch": {OldOID: setup.Commits.Third.OID, NewOID: setup.Commits.Second.OID},
					},
				},
				Begin{
					TransactionID:       5,
					RelativePath:        "pool",
					ExpectedSnapshotLSN: 4,
				},
				RunRepack{
					TransactionID: 5,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithUnreachable,
					},
				},
				Commit{
					TransactionID: 5,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(5).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-144f9890c312a4cf5e66895bf721606d0f691083",
									"pack-1d8b96ae9cc5301db6024e5d87974c960da6c017a9cf1bbed52bf8fe51e085de",
								): {
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
										setup.Commits.Third.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   false,
						},
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main":   setup.Commits.First.OID,
								"refs/heads/branch": setup.Commits.Second.OID,
							},
						},
					},
					"member": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles:    make(map[string]*PackfileState),
							// All objects are accessible in member.
							PooledObjects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
								setup.Commits.Third.OID,
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   false,
						},
						References: nil,
						Alternate:  "../../pool/objects",
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					},
					{
						lsn: 3,
						includeObjects: []git.ObjectID{
							setup.Commits.Second.OID,
							setup.Commits.Third.OID,
						},
					},
					{
						lsn: 5,
						includePackfiles: []string{hash(t,
							"pack-144f9890c312a4cf5e66895bf721606d0f691083",
							"pack-1d8b96ae9cc5301db6024e5d87974c960da6c017a9cf1bbed52bf8fe51e085de",
						)},
					},
				}),
			},
		},
		{
			desc: "run repacking (Geometric) on an alternate member",
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
						"refs/heads/branch": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Second.Pack,
					},
				},
				Begin{
					TransactionID:       4,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 3,
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.Third.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Third.Pack,
					},
				},
				Begin{
					TransactionID:       5,
					RelativePath:        "pool",
					ExpectedSnapshotLSN: 4,
				},
				Commit{
					TransactionID: 5,
					QuarantinedPacks: [][]byte{
						setup.Commits.Second.Pack,
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Second.OID},
				},
				Begin{
					TransactionID:       6,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 5,
				},
				RunRepack{
					TransactionID: 6,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyGeometric,
					},
				},
				Commit{
					TransactionID: 6,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(6).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-452292f7e0c6bcca1b42c53aaac4537416b5dbb9",
									"pack-735ad245db57a16c41525c9101c42594d090c7021b51aa12d9104a4eea4223c5",
								): {
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
									},
									HasReverseIndex: true,
								},
								hash(t,
									"pack-5a422f6c469963ffa026bf15cfd151751fba6e5f",
									"pack-3ed0b733b17d82f87a350b856f7fd6d6a781d85c5d8d36fad64b459124444f11",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   false,
						},
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
					"member": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: map[string]*PackfileState{
								// This packfile matches the quarantined pack of
								// transaction 3. Geometric repacking does not
								// deduplicate second commit.
								hash(t,
									"pack-4274682fcb6a4dbb1a59ba7dd8577402e61ccbd2",
									"pack-8ebabff3c37210ed37c4343255992f62a2ce113f7fb11f757de3bca157379d40",
								): {
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
								// This packfile matches the quarantined pack of
								// transaction 4.
								hash(t,
									"pack-529ec37accbc126425efe69abdf91153411532a6",
									"pack-895b4eade6c459f47a382a0d637ef1ce34a661c76f003c7d7a38a7420e3afc69",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Third.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   false,
						},
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch": setup.Commits.Third.OID,
							},
						},
						Alternate: "../../pool/objects",
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					},
					{
						lsn: 3,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
						},
					},
					{
						lsn: 4,
						includeObjects: []git.ObjectID{
							setup.Commits.Third.OID,
						},
					},
					{
						lsn: 5,
						includeObjects: []git.ObjectID{
							setup.Commits.Second.OID,
						},
					},
					{
						// As they form geometric progression, the repacking task keeps them intact.
						lsn: 6,
					},
				}),
			},
		},
		{
			desc: "run repacking (FullWithCruft) on an alternate member",
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
						"refs/heads/branch-1": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
						"refs/heads/branch-2": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Third.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Second.Pack,
						setup.Commits.Third.Pack,
						setup.Commits.Diverging.Pack,
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Diverging.OID},
				},
				Begin{
					TransactionID:       4,
					RelativePath:        "pool",
					ExpectedSnapshotLSN: 3,
				},
				Commit{
					TransactionID: 4,
					QuarantinedPacks: [][]byte{
						setup.Commits.Second.Pack,
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Second.OID},
				},
				Begin{
					TransactionID:       5,
					RelativePath:        "member",
					ExpectedSnapshotLSN: 4,
				},
				RunRepack{
					TransactionID: 5,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 5,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(5).toProto(),
				},
				Repositories: RepositoryStates{
					"pool": {
						Packfiles: &PackfilesState{
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-452292f7e0c6bcca1b42c53aaac4537416b5dbb9",
									"pack-735ad245db57a16c41525c9101c42594d090c7021b51aa12d9104a4eea4223c5",
								): {
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
									},
									HasReverseIndex: true,
								},
								hash(t,
									"pack-5a422f6c469963ffa026bf15cfd151751fba6e5f",
									"pack-3ed0b733b17d82f87a350b856f7fd6d6a781d85c5d8d36fad64b459124444f11",
								): {
									Objects: []git.ObjectID{
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   false,
						},
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
					"member": {
						Packfiles: &PackfilesState{
							Packfiles: map[string]*PackfileState{
								hash(t,
									"pack-529ec37accbc126425efe69abdf91153411532a6",
									"pack-895b4eade6c459f47a382a0d637ef1ce34a661c76f003c7d7a38a7420e3afc69",
								): {
									Objects: []git.ObjectID{
										// Diverging commit is pruned.
										setup.Commits.Third.OID,
									},
									HasReverseIndex: true,
								},
							},
							PooledObjects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								// Second commit is deduplicated.
								setup.Commits.Second.OID,
							},
							HasMultiPackIndex: false,
							HasCommitGraphs:   true,
						},
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-1": setup.Commits.Second.OID,
								"refs/heads/branch-2": setup.Commits.Third.OID,
							},
						},
						Alternate: "../../pool/objects",
					},
				},
				Directory: generateDirectoryState(setup.Config, []*walDirectoryState{
					{
						lsn: 1,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
						},
					},
					{
						lsn: 3,
						includeObjects: []git.ObjectID{
							setup.ObjectHash.EmptyTreeOID,
							setup.Commits.First.OID,
							setup.Commits.Second.OID,
							setup.Commits.Third.OID,
							setup.Commits.Diverging.OID,
						},
					},
					{
						lsn: 4,
						includeObjects: []git.ObjectID{
							setup.Commits.Second.OID,
						},
					},
					{
						lsn: 5,
						includePackfiles: []string{hash(t,
							"pack-529ec37accbc126425efe69abdf91153411532a6",
							"pack-895b4eade6c459f47a382a0d637ef1ce34a661c76f003c7d7a38a7420e3afc69",
						)},
						includeCommitGraphs: []string{hash(t,
							"graph-5cd3399b1657ded0a67d1dc3f9fef739fd648116",
							"graph-cc6c67a3c5e19b7b15b1f1551363a9d163fd14b13f11b6aeedcc5a9f40ffb590",
						)},
					},
				}),
			},
		},
		{
			desc: "run repacking concurrently with other repacking task",
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-C", repoPath, "repack", "-ad")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithUnreachable,
					},
				},
				RunRepack{
					TransactionID: 2,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithUnreachable,
					},
				},
				Commit{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 1,
					ExpectedError: errHousekeepingConflictConcurrent,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						Packfiles: &PackfilesState{
							// Unreachable objects are packed.
							LooseObjects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
								setup.Commits.Third.OID,
								setup.Commits.Diverging.OID,
							},
							Packfiles: map[string]*PackfileState{},
						},
					},
				},
				Directory: testhelper.DirectoryState{
					"/":      {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":   {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1": {Mode: fs.ModeDir | perm.PrivateDir},
				},
			},
		},
		{
			desc: "run repacking concurrently with other housekeeping task",
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-C", repoPath, "repack", "-ad")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				RunRepack{
					TransactionID: 1,
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithUnreachable,
					},
				},
				RunPackRefs{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 1,
					ExpectedError: errHousekeepingConflictConcurrent,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						Packfiles: &PackfilesState{
							// Unreachable objects are packed.
							LooseObjects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
								setup.Commits.Third.OID,
								setup.Commits.Diverging.OID,
							},
							Packfiles: map[string]*PackfileState{},
						},
					},
				},
				Directory: testhelper.DirectoryState{
					"/":                  {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":               {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/1/packed-refs": anyDirectoryEntryWithPerm(setup.Config, perm.SharedFile),
				},
			},
		},
	}
}
