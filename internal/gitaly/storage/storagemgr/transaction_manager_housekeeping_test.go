package storagemgr

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	housekeepingcfg "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
)

func generateHousekeepingPackRefsTests(t *testing.T, ctx context.Context, testPartitionID storage.PartitionID, relativePath string) []transactionTestCase {
	customSetup := func(t *testing.T, ctx context.Context, testPartitionID storage.PartitionID, relativePath string) testTransactionSetup {
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

	defaultReferences := map[git.ReferenceName]git.ObjectID{
		"refs/heads/branch-1": setup.Commits.Second.OID,
		"refs/heads/branch-2": setup.Commits.Third.OID,
		"refs/heads/main":     setup.Commits.First.OID,
		"refs/tags/v1.0.0":    lightweightTag,
		"refs/tags/v2.0.0":    annotatedTag.OID,
	}

	assertPackRefsMetrics := AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
		"housekeeping_task=total,stage=prepare":     1,
		"housekeeping_task=total,stage=verify":      1,
		"housekeeping_task=total,stage=apply":       1,
		"housekeeping_task=pack-refs,stage=prepare": 1,
		"housekeeping_task=pack-refs,stage=verify":  1,
		"housekeeping_task=pack-refs,stage=apply":   1,
	}}

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
				assertPackRefsMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
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
							perm.PrivateWriteOnceFile,
						))
						require.NoError(t, os.WriteFile(
							filepath.Join(repoPath, "packed-refs.new"),
							[]byte{},
							perm.PrivateWriteOnceFile,
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
				assertPackRefsMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
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
				assertPackRefsMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
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
				assertPackRefsMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
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
				assertPackRefsMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
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
				assertPackRefsMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
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
				assertPackRefsMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
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
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":     1,
					"housekeeping_task=total,stage=verify":      1,
					"housekeeping_task=pack-refs,stage=prepare": 1,
					"housekeeping_task=pack-refs,stage=verify":  1,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
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
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":     1,
					"housekeeping_task=total,stage=verify":      1,
					"housekeeping_task=pack-refs,stage=prepare": 1,
					"housekeeping_task=pack-refs,stage=verify":  1,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
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
					TransactionID:            2,
					RelativePath:             "member",
					SnapshottedRelativePaths: []string{"pool"},
					ExpectedSnapshotLSN:      1,
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
				assertPackRefsMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                     storage.LSN(5).ToProto(),
					"kv/" + string(relativePathKey("pool")):   string(""),
					"kv/" + string(relativePathKey("member")): string(""),
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
				assertPackRefsMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
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
				AssertMetrics{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
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
				AssertMetrics{},
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
				AssertMetrics{},
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
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":     2,
					"housekeeping_task=total,stage=verify":      2,
					"housekeeping_task=total,stage=apply":       1,
					"housekeeping_task=pack-refs,stage=prepare": 2,
					"housekeeping_task=pack-refs,stage=verify":  1,
					"housekeeping_task=pack-refs,stage=apply":   1,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
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
					TransactionID:            2,
					RelativePath:             "member",
					SnapshottedRelativePaths: []string{"pool"},
					ExpectedSnapshotLSN:      1,
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
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":     2,
					"housekeeping_task=total,stage=verify":      2,
					"housekeeping_task=total,stage=apply":       2,
					"housekeeping_task=pack-refs,stage=prepare": 2,
					"housekeeping_task=pack-refs,stage=verify":  2,
					"housekeeping_task=pack-refs,stage=apply":   2,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                     storage.LSN(4).ToProto(),
					"kv/" + string(relativePathKey("pool")):   string(""),
					"kv/" + string(relativePathKey("member")): string(""),
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
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":     2,
					"housekeeping_task=total,stage=verify":      2,
					"housekeeping_task=total,stage=apply":       2,
					"housekeeping_task=pack-refs,stage=prepare": 2,
					"housekeeping_task=pack-refs,stage=verify":  2,
					"housekeeping_task=pack-refs,stage=apply":   2,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
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
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":     1,
					"housekeeping_task=total,stage=verify":      1,
					"housekeeping_task=pack-refs,stage=prepare": 1,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                               storage.LSN(2).ToProto(),
					"kv/" + string(relativePathKey(setup.RelativePath)): string(""),
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

// generateHousekeepingRepackingStrategyTests returns a set of tests which run repacking with different strategies and
// settings.
func generateHousekeepingRepackingStrategyTests(t *testing.T, ctx context.Context, testPartitionID storage.PartitionID, relativePath string) []transactionTestCase {
	customSetup := func(t *testing.T, ctx context.Context, testPartitionID storage.PartitionID, relativePath string) testTransactionSetup {
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

	assertRepackingMetrics := AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
		"housekeeping_task=total,stage=prepare":  1,
		"housekeeping_task=total,stage=verify":   1,
		"housekeeping_task=total,stage=apply":    1,
		"housekeeping_task=repack,stage=prepare": 1,
		"housekeeping_task=repack,stage=verify":  1,
		"housekeeping_task=repack,stage=apply":   1,
	}}

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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyIncrementalWithUnreachable,
					},
				},
				Commit{
					TransactionID: 1,
					ExpectedError: errRepackNotSupportedStrategy,
				},
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":  1,
					"housekeeping_task=repack,stage=prepare": 1,
				}},
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
							Packfiles: []*PackfileState{
								{
									Objects:         defaultReachableObjects,
									HasBitmap:       true,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
						},
					},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy:            housekeepingcfg.RepackObjectsStrategyFullWithUnreachable,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							// Unreachable objects are packed.
							LooseObjects: nil,
							Packfiles: []*PackfileState{
								{
									Objects: append(defaultReachableObjects,
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									),
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
				},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy:            housekeepingcfg.RepackObjectsStrategyFullWithUnreachable,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: append(
										defaultReachableObjects,
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									),
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
				},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy:            housekeepingcfg.RepackObjectsStrategyGeometric,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: []*PackfileState{
								{
									Objects: append(defaultReachableObjects,
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									),
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
						},
					},
				},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy:            housekeepingcfg.RepackObjectsStrategyGeometric,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: []*PackfileState{
								// Initial packfile.
								{
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
								// New packfile that contains unreachable objects. This
								// is a co-incident, it follows the geometric
								// progression.
								{
									Objects: []git.ObjectID{
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									},
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
						},
					},
				},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy:            housekeepingcfg.RepackObjectsStrategyFullWithCruft,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
				},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy:            housekeepingcfg.RepackObjectsStrategyFullWithCruft,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							// Unreachable objects are pruned.
							LooseObjects: nil,
							Packfiles: []*PackfileState{
								{
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
				},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy:            housekeepingcfg.RepackObjectsStrategyFullWithCruft,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
				},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy:            housekeepingcfg.RepackObjectsStrategyGeometric,
						WriteBitmap:         false,
						WriteMultiPackIndex: false,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: []*PackfileState{
								{
									Objects:         defaultReachableObjects,
									HasBitmap:       false,
									HasReverseIndex: true,
								},
								{
									Objects: []git.ObjectID{
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									},
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
						},
					},
				},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy:            housekeepingcfg.RepackObjectsStrategyFullWithUnreachable,
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy:            housekeepingcfg.RepackObjectsStrategyFullWithUnreachable,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 2,
				},
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":  2,
					"housekeeping_task=total,stage=verify":   2,
					"housekeeping_task=total,stage=apply":    2,
					"housekeeping_task=repack,stage=prepare": 2,
					"housekeeping_task=repack,stage=verify":  2,
					"housekeeping_task=repack,stage=apply":   2,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: []*PackfileState{
								{
									Objects: append(defaultReachableObjects,
										setup.Commits.Orphan.OID,
										setup.Commits.Unreachable.OID,
									),
									HasBitmap:       false,
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: true,
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
				},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy:            housekeepingcfg.RepackObjectsStrategyFullWithUnreachable,
						WriteBitmap:         true,
						WriteMultiPackIndex: true,
					},
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Third.OID, NewOID: setup.Commits.Second.OID},
					},
					ExpectedError: errHousekeepingConflictOtherUpdates,
				},
				AssertMetrics{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References:    &ReferencesState{LooseReferences: defaultReferences},
						Packfiles: &PackfilesState{
							LooseObjects:      append(defaultReachableObjects, setup.Commits.Unreachable.OID, setup.Commits.Orphan.OID),
							Packfiles:         []*PackfileState{},
							HasMultiPackIndex: false,
						},
					},
				},
			},
		},
	}
}

// generateHousekeepingRepackingConcurrentTests returns a set of tests which run repacking before, after, or alongside
// with other transactions.
func generateHousekeepingRepackingConcurrentTests(t *testing.T, ctx context.Context, setup testTransactionSetup) []transactionTestCase {
	assertRepackingMetrics := AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
		"housekeeping_task=total,stage=prepare":  1,
		"housekeeping_task=total,stage=verify":   1,
		"housekeeping_task=total,stage=apply":    1,
		"housekeeping_task=repack,stage=prepare": 1,
		"housekeeping_task=repack,stage=verify":  1,
		"housekeeping_task=repack,stage=apply":   1,
	}}

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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 2,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									// Diverging commit is gone.
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
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
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Third.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.Third.OID,
									},
									HasReverseIndex: true,
								},
								{
									// Diverging commit is gone.
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
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
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Third.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
								{
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.Third.OID,
									},
									HasReverseIndex: true,
								},
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
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
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
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
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(5).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Diverging.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
									},
									HasReverseIndex: true,
								},
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.Diverging.OID,
									},
									HasReverseIndex: true,
								},
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.Third.OID,
									},
									HasReverseIndex: true,
								},
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithUnreachable,
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
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(4).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyGeometric,
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
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(4).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
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
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":  1,
					"housekeeping_task=total,stage=verify":   1,
					"housekeeping_task=repack,stage=prepare": 1,
					"housekeeping_task=repack,stage=verify":  1,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
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
			desc: "run repacking (FullWithCruft) concurrently with another transaction depending on object in an in-between reference update",
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
						"refs/heads/branch-1": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Diverging.OID},
					QuarantinedPacks: [][]byte{
						setup.Commits.First.Pack,
						setup.Commits.Diverging.Pack,
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
				UpdateReferences{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-2": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Diverging.OID},
					},
				},
				UpdateReferences{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-2": {OldOID: setup.Commits.Diverging.OID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID: 2,
				},
				RunRepack{
					TransactionID: 3,
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 3,
					ExpectedError: errRepackConflictPrunedObject,
				},
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":  1,
					"housekeeping_task=total,stage=verify":   1,
					"housekeeping_task=repack,stage=prepare": 1,
					"housekeeping_task=repack,stage=verify":  1,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-1": setup.Commits.First.OID,
								"refs/heads/branch-2": setup.Commits.First.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Diverging.OID,
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
			desc: "run repacking (FullWithCruft) concurrently with another transaction's packfile depending on pruned objects",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID:    1,
					IncludeObjects:   []git.ObjectID{setup.Commits.First.OID},
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
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Second.Pack,
					},
				},
				RunRepack{
					TransactionID: 3,
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 3,
					ExpectedError: errRepackConflictPrunedObject,
				},
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":  1,
					"housekeeping_task=total,stage=verify":   1,
					"housekeeping_task=repack,stage=prepare": 1,
					"housekeeping_task=repack,stage=verify":  1,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
									},
									HasReverseIndex: true,
								},
								{
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
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
			desc: "run repacking (FullWithCruft) concurrently with another transaction including but not referencing an object",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID:    1,
					IncludeObjects:   []git.ObjectID{setup.Commits.First.OID},
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
					TransactionID:  2,
					IncludeObjects: []git.ObjectID{setup.Commits.Second.OID},
					QuarantinedPacks: [][]byte{
						setup.Commits.Second.Pack,
					},
				},
				RunRepack{
					TransactionID: 3,
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 3,
					ExpectedError: errRepackConflictPrunedObject,
				},
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":  1,
					"housekeeping_task=total,stage=verify":   1,
					"housekeeping_task=repack,stage=prepare": 1,
					"housekeeping_task=repack,stage=verify":  1,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
									},
									HasReverseIndex: true,
								},
								{
									Objects: []git.ObjectID{
										gittest.DefaultObjectHash.EmptyTreeOID,
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
					TransactionID:            2,
					RelativePath:             "member",
					SnapshottedRelativePaths: []string{"pool"},
					ExpectedSnapshotLSN:      1,
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithUnreachable,
					},
				},
				Commit{
					TransactionID: 6,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                     storage.LSN(6).ToProto(),
					"kv/" + string(relativePathKey("pool")):   string(""),
					"kv/" + string(relativePathKey("member")): string(""),
				},
				Repositories: RepositoryStates{
					"pool": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							// First commit and its tree object.
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
									},
									HasReverseIndex: true,
								},
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
						},
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
					"member": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: []*PackfileState{
								// Packfile containing second commit (reachable) and
								// third commit (unreachable). Redundant objects in
								// quarantined packs are removed.
								{
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
						},
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch": setup.Commits.Second.OID,
							},
						},
						Alternate:           "../../pool/objects",
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
				},
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
					TransactionID:            2,
					RelativePath:             "member",
					SnapshottedRelativePaths: []string{"pool"},
					ExpectedSnapshotLSN:      1,
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithUnreachable,
					},
				},
				Commit{
					TransactionID: 5,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                     storage.LSN(5).ToProto(),
					"kv/" + string(relativePathKey("pool")):   string(""),
					"kv/" + string(relativePathKey("member")): string(""),
				},
				Repositories: RepositoryStates{
					"pool": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: []*PackfileState{
								{
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
						},
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main":   setup.Commits.First.OID,
								"refs/heads/branch": setup.Commits.Second.OID,
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
					"member": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles:    []*PackfileState{},
							// All objects are accessible in member.
							PooledObjects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								setup.Commits.Second.OID,
								setup.Commits.Third.OID,
							},
							HasMultiPackIndex: false,
						},
						References:          nil,
						Alternate:           "../../pool/objects",
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
				},
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
					TransactionID:            2,
					RelativePath:             "member",
					SnapshottedRelativePaths: []string{"pool"},
					ExpectedSnapshotLSN:      1,
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
						setup.Commits.Diverging.Pack,
					},
					IncludeObjects: []git.ObjectID{setup.Commits.Diverging.OID},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyGeometric,
					},
				},
				Commit{
					TransactionID: 6,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                     storage.LSN(6).ToProto(),
					"kv/" + string(relativePathKey("pool")):   string(""),
					"kv/" + string(relativePathKey("member")): string(""),
				},
				Repositories: RepositoryStates{
					"pool": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
									},
									HasReverseIndex: true,
								},
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
						},
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
					"member": {
						Packfiles: &PackfilesState{
							LooseObjects: nil,
							PooledObjects: []git.ObjectID{
								setup.ObjectHash.EmptyTreeOID,
								setup.Commits.First.OID,
								// The geometric repack triggered merging of the packs
								// produced by transactions 3 and 4. While they were rewritten,
								// the objects in the alternate were deduplicated from the member.
								setup.Commits.Second.OID,
							},
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										// This commit isn't present in the pool and was thus left
										// in the member itself.
										setup.Commits.Third.OID,
										// This commit is unreachable. Geometric repacking does not
										// prune unreachable objects.
										setup.Commits.Diverging.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
						},
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch": setup.Commits.Third.OID,
							},
						},
						Alternate:           "../../pool/objects",
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
				},
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
					TransactionID:            2,
					RelativePath:             "member",
					SnapshottedRelativePaths: []string{"pool"},
					ExpectedSnapshotLSN:      1,
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 5,
				},
				assertRepackingMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN):                     storage.LSN(5).ToProto(),
					"kv/" + string(relativePathKey("pool")):   string(""),
					"kv/" + string(relativePathKey("member")): string(""),
				},
				Repositories: RepositoryStates{
					"pool": {
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
									},
									HasReverseIndex: true,
								},
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.Second.OID,
									},
									HasReverseIndex: true,
								},
							},
							HasMultiPackIndex: false,
						},
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
					"member": {
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
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
						},
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/branch-1": setup.Commits.Second.OID,
								"refs/heads/branch-2": setup.Commits.Third.OID,
							},
						},
						Alternate:           "../../pool/objects",
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
				},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithUnreachable,
					},
				},
				RunRepack{
					TransactionID: 2,
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithUnreachable,
					},
				},
				Commit{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 1,
					ExpectedError: errHousekeepingConflictConcurrent,
				},
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":  2,
					"housekeeping_task=total,stage=verify":   2,
					"housekeeping_task=total,stage=apply":    1,
					"housekeeping_task=repack,stage=prepare": 2,
					"housekeeping_task=repack,stage=verify":  1,
					"housekeeping_task=repack,stage=apply":   1,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						Packfiles: &PackfilesState{
							// Unreachable objects are packed.
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
										setup.Commits.Third.OID,
										setup.Commits.Diverging.OID,
									},
									HasReverseIndex: true,
								},
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
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
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithUnreachable,
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
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":     2,
					"housekeeping_task=total,stage=verify":      2,
					"housekeeping_task=total,stage=apply":       1,
					"housekeeping_task=pack-refs,stage=prepare": 1,
					"housekeeping_task=pack-refs,stage=verify":  1,
					"housekeeping_task=pack-refs,stage=apply":   1,
					"housekeeping_task=repack,stage=prepare":    1,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
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
							Packfiles: []*PackfileState{},
						},
					},
				},
			},
		},
	}
}

func generateHousekeepingCommitGraphsTests(t *testing.T, ctx context.Context, setup testTransactionSetup) []transactionTestCase {
	defaultLooseObjects := []git.ObjectID{
		setup.Commits.First.OID,
		setup.Commits.Second.OID,
		setup.Commits.Third.OID,
		setup.Commits.Diverging.OID,
		setup.ObjectHash.EmptyTreeOID,
	}
	assertCommitGraphMetrics := AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
		"housekeeping_task=total,stage=prepare":        1,
		"housekeeping_task=total,stage=verify":         1,
		"housekeeping_task=total,stage=apply":          1,
		"housekeeping_task=commit-graph,stage=prepare": 1,
		"housekeeping_task=commit-graph,stage=verify":  1,
		"housekeeping_task=commit-graph,stage=apply":   1,
	}}
	return []transactionTestCase{
		{
			desc: "run writing commit graph on a repository without existing commit graph",
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
				WriteCommitGraphs{
					TransactionID: 2,
					Config: housekeepingcfg.WriteCommitGraphConfig{
						ReplaceChain: true,
					},
				},
				Commit{
					TransactionID: 2,
				},
				assertCommitGraphMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
										setup.ObjectHash.EmptyTreeOID,
									},
									HasReverseIndex: true,
								},
							},
							CommitGraphs: &stats.CommitGraphInfo{
								Exists:                 true,
								CommitGraphChainLength: 1,
								HasBloomFilters:        true,
								HasGenerationData:      true,
							},
						},
					},
				},
			},
		},
		{
			desc: "run writing commit graph on an empty repository",
			steps: steps{
				Prune{},
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				WriteCommitGraphs{
					TransactionID: 1,
					Config: housekeepingcfg.WriteCommitGraphConfig{
						ReplaceChain: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertCommitGraphMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
						},
						Packfiles: &PackfilesState{
							CommitGraphs: &stats.CommitGraphInfo{
								Exists:                 true,
								CommitGraphChainLength: 1,
								HasBloomFilters:        true,
								HasGenerationData:      true,
							},
							Packfiles: []*PackfileState{},
						},
					},
				},
			},
		},
		{
			desc: "run writing commit graph on a repository having existing commit graph",
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--changed-paths", "--size-multiple=4", "--split=replace")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				WriteCommitGraphs{
					TransactionID: 1,
					Config: housekeepingcfg.WriteCommitGraphConfig{
						ReplaceChain: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertCommitGraphMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						Packfiles: &PackfilesState{
							LooseObjects: defaultLooseObjects,
							Packfiles:    []*PackfileState{},
							CommitGraphs: &stats.CommitGraphInfo{
								Exists:                 true,
								CommitGraphChainLength: 1,
								HasBloomFilters:        true,
								HasGenerationData:      true,
							},
						},
					},
				},
			},
		},
		{
			desc: "run writing commit graph on a repository having existing commit graph without replacing chain",
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
					IncludeObjects: []git.ObjectID{
						setup.Commits.Diverging.OID,
					},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				WriteCommitGraphs{
					TransactionID: 2,
					Config: housekeepingcfg.WriteCommitGraphConfig{
						ReplaceChain: false,
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
				RunRepack{
					TransactionID: 3,
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 3,
				},
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 3,
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Third.OID},
					},
					QuarantinedPacks: [][]byte{
						setup.Commits.Third.Pack,
					},
				},
				Begin{
					TransactionID:       5,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 4,
				},
				WriteCommitGraphs{
					TransactionID: 5,
					Config: housekeepingcfg.WriteCommitGraphConfig{
						ReplaceChain: false,
					},
				},
				Commit{
					TransactionID: 5,
				},
				AssertMetrics{histogramMetric("gitaly_housekeeping_tasks_latency"): {
					"housekeeping_task=total,stage=prepare":        3,
					"housekeeping_task=total,stage=verify":         3,
					"housekeeping_task=total,stage=apply":          3,
					"housekeeping_task=commit-graph,stage=prepare": 2,
					"housekeeping_task=commit-graph,stage=verify":  2,
					"housekeeping_task=commit-graph,stage=apply":   2,
					"housekeeping_task=repack,stage=prepare":       1,
					"housekeeping_task=repack,stage=verify":        1,
					"housekeeping_task=repack,stage=apply":         1,
				}},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(5).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main":   setup.Commits.Second.OID,
								"refs/heads/branch": setup.Commits.Third.OID,
							},
						},
						Packfiles: &PackfilesState{
							Packfiles: []*PackfileState{
								{
									Objects: []git.ObjectID{
										setup.Commits.First.OID,
										setup.Commits.Second.OID,
										setup.ObjectHash.EmptyTreeOID,
									},
									HasReverseIndex: true,
								},
								{
									Objects: []git.ObjectID{
										setup.ObjectHash.EmptyTreeOID,
										setup.Commits.Third.OID,
									},
									HasReverseIndex: true,
								},
							},
							CommitGraphs: &stats.CommitGraphInfo{
								Exists:                 true,
								CommitGraphChainLength: 1,
								HasBloomFilters:        true,
								HasGenerationData:      true,
							},
						},
						FullRepackTimestamp: &FullRepackTimestamp{Exists: true},
					},
				},
			},
		},
		{
			desc: "run writing commit graph on a repository having monolithic commit graph file",
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						repoPath := filepath.Join(storagePath, setup.RelativePath)
						gittest.Exec(tb, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--changed-paths")
					},
				},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				WriteCommitGraphs{
					TransactionID: 1,
					Config: housekeepingcfg.WriteCommitGraphConfig{
						ReplaceChain: true,
					},
				},
				Commit{
					TransactionID: 1,
				},
				assertCommitGraphMetrics,
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						Packfiles: &PackfilesState{
							LooseObjects: defaultLooseObjects,
							Packfiles:    []*PackfileState{},
							CommitGraphs: &stats.CommitGraphInfo{
								Exists:                 true,
								CommitGraphChainLength: 1,
								HasBloomFilters:        true,
								HasGenerationData:      true,
							},
						},
					},
				},
			},
		},
	}
}
