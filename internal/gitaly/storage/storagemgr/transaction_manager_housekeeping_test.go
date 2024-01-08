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
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func generateHousekeepingTests(t *testing.T, ctx context.Context, testPartitionID partitionID, relativePath string) []transactionTestCase {
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

	directoryStateWithPackedRefs := func(lsn LSN) testhelper.DirectoryState {
		return testhelper.DirectoryState{
			"/":    {Mode: fs.ModeDir | perm.PrivateDir},
			"/wal": {Mode: fs.ModeDir | perm.PrivateDir},
			// LSN is when a log entry is appended, it's different from transaction ID.
			fmt.Sprintf("/wal/%d", lsn):             {Mode: fs.ModeDir | perm.PrivateDir},
			fmt.Sprintf("/wal/%s/packed-refs", lsn): packRefsDirectoryEntry(setup.Config),
		}
	}

	defaultRefs := []git.Reference{
		{Name: "refs/heads/branch-1", Target: setup.Commits.Second.OID.String()},
		{Name: "refs/heads/branch-2", Target: setup.Commits.Third.OID.String()},
		{Name: "refs/heads/main", Target: setup.Commits.First.OID.String()},
		{Name: "refs/tags/v1.0.0", Target: lightweightTag.String()},
		{Name: "refs/tags/v2.0.0", Target: annotatedTag.OID.String()},
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
				Directory: directoryStateWithPackedRefs(1),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: []git.Reference{
							{Name: "refs/heads/branch-1", Target: setup.Commits.Second.OID.String()},
							{Name: "refs/heads/branch-2", Target: setup.Commits.Third.OID.String()},
							// `main` points to the second commit now
							{Name: "refs/heads/main", Target: setup.Commits.Second.OID.String()},
							{Name: "refs/tags/v1.0.0", Target: lightweightTag.String()},
							{Name: "refs/tags/v2.0.0", Target: annotatedTag.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/branch-1", setup.Commits.Second.OID.String()),
								fmt.Sprintf("%s refs/heads/branch-2", setup.Commits.Third.OID.String()),
								// But `main` in packed-refs file points to the first
								// commit.
								fmt.Sprintf("%s refs/heads/main", setup.Commits.First.OID.String()),
								fmt.Sprintf("%s refs/tags/v1.0.0", lightweightTag.String()),
								fmt.Sprintf("%s refs/tags/v2.0.0", annotatedTag.OID.String()),
								fmt.Sprintf("^%s", setup.Commits.Diverging.OID.String()),
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
				Directory: directoryStateWithPackedRefs(2),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: []git.Reference{
							{Name: "refs/heads/branch-1", Target: setup.Commits.Second.OID.String()},
							{Name: "refs/heads/branch-2", Target: setup.Commits.Third.OID.String()},
							{Name: "refs/heads/branch-3", Target: setup.Commits.Diverging.OID.String()},
							{Name: "refs/heads/main", Target: setup.Commits.Second.OID.String()},
							{Name: "refs/tags/v1.0.0", Target: lightweightTag.String()},
							{Name: "refs/tags/v2.0.0", Target: annotatedTag.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								// All refs are packed to the packed-refs file.
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/branch-1", setup.Commits.Second.OID.String()),
								fmt.Sprintf("%s refs/heads/branch-2", setup.Commits.Third.OID.String()),
								fmt.Sprintf("%s refs/heads/branch-3", setup.Commits.Diverging.OID.String()),
								fmt.Sprintf("%s refs/heads/main", setup.Commits.Second.OID.String()),
								fmt.Sprintf("%s refs/tags/v1.0.0", lightweightTag.String()),
								fmt.Sprintf("%s refs/tags/v2.0.0", annotatedTag.OID.String()),
								fmt.Sprintf("^%s", setup.Commits.Diverging.OID.String()),
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
				Directory: directoryStateWithPackedRefs(2),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: []git.Reference{
							{Name: "refs/heads/branch-1", Target: setup.Commits.Second.OID.String()},
							{Name: "refs/heads/branch-2", Target: setup.Commits.Third.OID.String()},
							{Name: "refs/heads/main", Target: setup.Commits.First.OID.String()},
							{Name: "refs/keep-around/1", Target: setup.Commits.First.OID.String()},
							{Name: "refs/merge-requests/1", Target: setup.Commits.Second.OID.String()},
							{Name: "refs/tags/v1.0.0", Target: lightweightTag.String()},
							{Name: "refs/tags/v2.0.0", Target: annotatedTag.OID.String()},
							{Name: "refs/very/deep/nested/ref", Target: setup.Commits.Third.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/branch-1", setup.Commits.Second.OID.String()),
								fmt.Sprintf("%s refs/heads/branch-2", setup.Commits.Third.OID.String()),
								fmt.Sprintf("%s refs/heads/main", setup.Commits.First.OID.String()),
								fmt.Sprintf("%s refs/keep-around/1", setup.Commits.First.OID.String()),
								fmt.Sprintf("%s refs/merge-requests/1", setup.Commits.Second.OID.String()),
								fmt.Sprintf("%s refs/tags/v1.0.0", lightweightTag.String()),
								fmt.Sprintf("%s refs/tags/v2.0.0", annotatedTag.OID.String()),
								fmt.Sprintf("^%s", setup.Commits.Diverging.OID.String()),
								fmt.Sprintf("%s refs/very/deep/nested/ref", setup.Commits.Third.OID.String()),
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
				Directory: directoryStateWithPackedRefs(2),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: []git.Reference{
							{Name: "refs/heads/branch-1", Target: setup.Commits.Second.OID.String()},
							{Name: "refs/heads/branch-2", Target: setup.Commits.Third.OID.String()},
							{Name: "refs/heads/branch-3", Target: setup.Commits.Diverging.OID.String()},
							{Name: "refs/heads/main", Target: setup.Commits.First.OID.String()},
							{Name: "refs/keep-around/1", Target: setup.Commits.First.OID.String()},
							{Name: "refs/tags/v1.0.0", Target: lightweightTag.String()},
							{Name: "refs/tags/v2.0.0", Target: annotatedTag.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/branch-1", setup.Commits.Second.OID.String()),
								fmt.Sprintf("%s refs/heads/branch-2", setup.Commits.Third.OID.String()),
								fmt.Sprintf("%s refs/heads/main", setup.Commits.First.OID.String()),
								fmt.Sprintf("%s refs/tags/v1.0.0", lightweightTag.String()),
								fmt.Sprintf("%s refs/tags/v2.0.0", annotatedTag.OID.String()),
								fmt.Sprintf("^%s", setup.Commits.Diverging.OID.String()),
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
				Directory: directoryStateWithPackedRefs(1),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: []git.Reference{
							{Name: "refs/heads/branch-1", Target: setup.Commits.Second.OID.String()},
							{Name: "refs/heads/branch-2", Target: setup.Commits.Third.OID.String()},
							{Name: "refs/heads/branch-3", Target: setup.Commits.Diverging.OID.String()},
							{Name: "refs/heads/main", Target: setup.Commits.First.OID.String()},
							{Name: "refs/keep-around/1", Target: setup.Commits.First.OID.String()},
							{Name: "refs/tags/v1.0.0", Target: lightweightTag.String()},
							{Name: "refs/tags/v2.0.0", Target: annotatedTag.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/branch-1", setup.Commits.Second.OID.String()),
								fmt.Sprintf("%s refs/heads/branch-2", setup.Commits.Third.OID.String()),
								fmt.Sprintf("%s refs/heads/main", setup.Commits.First.OID.String()),
								fmt.Sprintf("%s refs/tags/v1.0.0", lightweightTag.String()),
								fmt.Sprintf("%s refs/tags/v2.0.0", annotatedTag.OID.String()),
								fmt.Sprintf("^%s", setup.Commits.Diverging.OID.String()),
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
				Directory: directoryStateWithPackedRefs(2),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: []git.Reference{
							{Name: "refs/heads/branch-1", Target: setup.Commits.Third.OID.String()},
							{Name: "refs/heads/branch-2", Target: setup.Commits.Diverging.OID.String()},
							{Name: "refs/heads/main", Target: setup.Commits.Second.OID.String()},
							{Name: "refs/tags/v1.0.0", Target: setup.Commits.First.OID.String()},
							{Name: "refs/tags/v2.0.0", Target: annotatedTag.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/branch-1", setup.Commits.Second.OID.String()), // Outdated
								fmt.Sprintf("%s refs/heads/branch-2", setup.Commits.Third.OID.String()),  // Outdated
								fmt.Sprintf("%s refs/heads/main", setup.Commits.First.OID.String()),      // Outdated
								fmt.Sprintf("%s refs/tags/v1.0.0", lightweightTag.String()),              // Outdated
								fmt.Sprintf("%s refs/tags/v2.0.0", annotatedTag.OID.String()),            // Still up-to-date
								fmt.Sprintf("^%s", setup.Commits.Diverging.OID.String()),
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
				Directory: directoryStateWithPackedRefs(1),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: []git.Reference{
							{Name: "refs/heads/branch-1", Target: setup.Commits.Third.OID.String()},
							{Name: "refs/heads/branch-2", Target: setup.Commits.Diverging.OID.String()},
							{Name: "refs/heads/main", Target: setup.Commits.Second.OID.String()},
							{Name: "refs/tags/v1.0.0", Target: setup.Commits.First.OID.String()},
							{Name: "refs/tags/v2.0.0", Target: annotatedTag.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/branch-1", setup.Commits.Second.OID.String()), // Outdated
								fmt.Sprintf("%s refs/heads/branch-2", setup.Commits.Third.OID.String()),  // Outdated
								fmt.Sprintf("%s refs/heads/main", setup.Commits.First.OID.String()),      // Outdated
								fmt.Sprintf("%s refs/tags/v1.0.0", lightweightTag.String()),              // Outdated
								fmt.Sprintf("%s refs/tags/v2.0.0", annotatedTag.OID.String()),
								fmt.Sprintf("^%s", setup.Commits.Diverging.OID.String()),
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
						References: []git.Reference{
							{Name: "refs/heads/branch-2", Target: setup.Commits.Third.OID.String()},
							{Name: "refs/heads/main", Target: setup.Commits.First.OID.String()},
							{Name: "refs/tags/v2.0.0", Target: annotatedTag.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							// Empty packed-refs. It means the pack-refs task is not
							// executed.
							PackedRefsContent: []string{""},
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
						References: []git.Reference{
							{Name: "refs/heads/branch-1", Target: setup.Commits.Second.OID.String()},
							{Name: "refs/heads/branch-2", Target: setup.Commits.Third.OID.String()},
							{Name: "refs/tags/v1.0.0", Target: lightweightTag.String()},
							{Name: "refs/tags/v2.0.0", Target: annotatedTag.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{""},
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
						References: []git.Reference{
							{Name: "refs/heads/main", Target: setup.Commits.First.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/main", setup.Commits.First.OID.String()),
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
					"/wal/5/packed-refs": packRefsDirectoryEntry(setup.Config),
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
				Directory: directoryStateWithPackedRefs(1),
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: []git.Reference{
							{Name: "refs/heads/branch-2", Target: setup.Commits.Third.OID.String()},
							{Name: "refs/heads/main", Target: setup.Commits.First.OID.String()},
							{Name: "refs/tags/v2.0.0", Target: annotatedTag.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/branch-2", setup.Commits.Third.OID.String()),
								fmt.Sprintf("%s refs/heads/main", setup.Commits.First.OID.String()),
								fmt.Sprintf("%s refs/tags/v2.0.0", annotatedTag.OID.String()),
								fmt.Sprintf("^%s", setup.Commits.Diverging.OID.String()),
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
					"/wal/2/packed-refs": packRefsDirectoryEntry(setup.Config),
				},
				Repositories: RepositoryStates{
					relativePath: {
						DefaultBranch: "refs/heads/main",
						References: []git.Reference{
							{Name: "refs/heads/empty-dir/parent/main", Target: setup.Commits.First.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/empty-dir/parent/main", setup.Commits.First.OID.String()),
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
						References:    defaultRefs,
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
						References:    defaultRefs,
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
				Directory: directoryStateWithPackedRefs(1),
				Repositories: RepositoryStates{
					relativePath: {
						DefaultBranch: "refs/heads/main",
						References:    defaultRefs,
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/branch-1", setup.Commits.Second.OID.String()),
								fmt.Sprintf("%s refs/heads/branch-2", setup.Commits.Third.OID.String()),
								fmt.Sprintf("%s refs/heads/main", setup.Commits.First.OID.String()),
								fmt.Sprintf("%s refs/tags/v1.0.0", lightweightTag.String()),
								fmt.Sprintf("%s refs/tags/v2.0.0", annotatedTag.OID.String()),
								fmt.Sprintf("^%s", setup.Commits.Diverging.OID.String()),
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
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
						References: []git.Reference{
							{Name: "refs/heads/main", Target: setup.Commits.First.OID.String()},
						},
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/main", setup.Commits.First.OID.String()),
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
					"/wal/3/packed-refs": packRefsDirectoryEntry(setup.Config),
					"/wal/4":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/4/packed-refs": packRefsDirectoryEntry(setup.Config),
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
					"/wal/1/packed-refs": packRefsDirectoryEntry(setup.Config),
					"/wal/2":             {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/2/packed-refs": packRefsDirectoryEntry(setup.Config),
				},
				Repositories: RepositoryStates{
					relativePath: {
						DefaultBranch: "refs/heads/main",
						References:    defaultRefs,
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{
								"# pack-refs with: peeled fully-peeled sorted ",
								fmt.Sprintf("%s refs/heads/branch-1", setup.Commits.Second.OID.String()),
								fmt.Sprintf("%s refs/heads/branch-2", setup.Commits.Third.OID.String()),
								fmt.Sprintf("%s refs/heads/main", setup.Commits.First.OID.String()),
								fmt.Sprintf("%s refs/tags/v1.0.0", lightweightTag.String()),
								fmt.Sprintf("%s refs/tags/v2.0.0", annotatedTag.OID.String()),
								fmt.Sprintf("^%s", setup.Commits.Diverging.OID.String()),
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
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
						References:    nil,
						PackedRefs: &PackedRefsState{
							PackedRefsContent: []string{""},
							LooseReferences:   map[git.ReferenceName]git.ObjectID{},
						},
						Objects: []git.ObjectID{},
					},
				},
			},
		},
	}
}
