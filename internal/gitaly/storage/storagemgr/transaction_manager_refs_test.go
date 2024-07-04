package storagemgr

import (
	"path/filepath"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	housekeepingcfg "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func generateModifyReferencesTests(t *testing.T, setup testTransactionSetup) []transactionTestCase {
	return []transactionTestCase{
		{
			desc: "create reference with non-utf-8 sequence",
			skip: func(t *testing.T) {
				testhelper.SkipWithMacOS(t, "macOS does not support non-UTF-8 in file system paths")
			},
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/non-\xE5-utf8-directory/non-\xE5-utf8-file": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/non-\xE5-utf8-directory/non-\xE5-utf8-file": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "delete reference with non-utf-8 sequence",
			skip: func(t *testing.T) {
				testhelper.SkipWithMacOS(t, "macOS does not support non-UTF-8 in file system paths")
			},
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/non-\xE5-utf8-directory/non-\xE5-utf8-file": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
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
						"refs/heads/non-\xE5-utf8-directory/non-\xE5-utf8-file": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
			},
		},
		{
			desc: "create reference",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
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
		{
			desc: "create reference outside of refs",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					ReferenceUpdates: ReferenceUpdates{
						"not-in-refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					ExpectedError: InvalidReferenceFormatError{
						ReferenceName: "not-in-refs/heads/main",
					},
				},
			},
		},
		{
			desc: "create a file-directory reference conflict different transaction",
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
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/parent": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/parent/child": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					ExpectedError: updateref.FileDirectoryConflictError{
						ExistingReferenceName:    "refs/heads/parent",
						ConflictingReferenceName: "refs/heads/parent/child",
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/parent": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "file-directory conflict aborts the transaction with verification failures skipped",
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
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/parent": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID:            1,
					SkipVerificationFailures: true,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":         {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
						"refs/heads/parent/child": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					ExpectedError: updateref.FileDirectoryConflictError{
						ExistingReferenceName:    "refs/heads/parent",
						ConflictingReferenceName: "refs/heads/parent/child",
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/parent": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "delete file-directory conflict in different transaction",
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
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/parent/child": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					// This is a no-op and thus is dropped.
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/parent": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/parent/child": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "file-directory conflict solved in the same transaction",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/parent": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				UpdateReferences{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/parent": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/parent/child": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/parent/child": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "create a tag to a non-commit object",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					ReferenceUpdates: ReferenceUpdates{
						"refs/tags/v1.0.0": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.ObjectHash.EmptyTreeOID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/tags/v1.0.0": setup.ObjectHash.EmptyTreeOID,
							},
						},
					},
				},
			},
		},
		{
			desc: "create a reference to non-existent object",
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
					IncludeObjects:   []git.ObjectID{setup.Commits.First.OID},
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
					TransactionID: 3,
					Config: housekeepingcfg.RepackObjectsConfig{
						Strategy: housekeepingcfg.RepackObjectsStrategyFullWithCruft,
					},
				},
				Commit{
					TransactionID: 3,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					ExpectedError: localrepo.InvalidObjectError(setup.Commits.First.OID),
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						Objects: []git.ObjectID{},
					},
				},
			},
		},
		{
			desc: "create reference ignoring verification failure",
			steps: steps{
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
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID:            2,
					SkipVerificationFailures: true,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":            {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.Third.OID},
						"refs/heads/non-conflicting": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
					},
				},
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
								"refs/heads/main":            setup.Commits.First.OID,
								"refs/heads/non-conflicting": setup.Commits.Second.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "create reference that already exists",
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
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":            {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
						"refs/heads/non-conflicting": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
					},
					ExpectedError: ReferenceVerificationError{
						ReferenceName:  "refs/heads/main",
						ExpectedOldOID: setup.ObjectHash.ZeroOID,
						ActualOldOID:   setup.Commits.First.OID,
						NewOID:         setup.Commits.Second.OID,
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
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
		{
			desc: "create reference no-op",
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
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
					ExpectedError: ReferenceVerificationError{
						ReferenceName:  "refs/heads/main",
						ExpectedOldOID: setup.ObjectHash.ZeroOID,
						ActualOldOID:   setup.Commits.First.OID,
						NewOID:         setup.Commits.First.OID,
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
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
		{
			desc: "update reference",
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
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
				},
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
					},
				},
			},
		},
		{
			desc: "update reference ignoring verification failures",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":            {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
						"refs/heads/non-conflicting": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
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
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID:            2,
					SkipVerificationFailures: true,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":            {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.Third.OID},
						"refs/heads/non-conflicting": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Third.OID},
					},
				},
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
								"refs/heads/main":            setup.Commits.First.OID,
								"refs/heads/non-conflicting": setup.Commits.Third.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "update reference with incorrect old tip",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":            {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
						"refs/heads/non-conflicting": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
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
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":            {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.Third.OID},
						"refs/heads/non-conflicting": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Third.OID},
					},
					ExpectedError: ReferenceVerificationError{
						ReferenceName:  "refs/heads/main",
						ExpectedOldOID: setup.Commits.Second.OID,
						ActualOldOID:   setup.Commits.First.OID,
						NewOID:         setup.Commits.Third.OID,
					},
				},
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
								"refs/heads/main":            setup.Commits.First.OID,
								"refs/heads/non-conflicting": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "update non-existent reference",
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
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
					ExpectedError: ReferenceVerificationError{
						ReferenceName:  "refs/heads/main",
						ExpectedOldOID: setup.Commits.First.OID,
						ActualOldOID:   setup.ObjectHash.ZeroOID,
						NewOID:         setup.Commits.Second.OID,
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
			},
		},
		{
			desc: "update reference no-op",
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
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.First.OID},
					},
				},
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
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "delete reference",
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
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
			},
		},
		{
			desc: "concurrently delete two packed references",
			skip: func(t *testing.T) {
				testhelper.SkipWithReftable(t, "transactional housekeeping doesn't support reftabled yet")
			},
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-1": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
						"refs/heads/branch-2": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
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
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-1": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-2": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(4).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{},
							LooseReferences:  map[git.ReferenceName]git.ObjectID{},
						},
					},
				},
			},
		},
		{
			desc: "concurrently delete two loose references",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-1":        {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
						"refs/heads/subdir/branch-2": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
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
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-1": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/subdir/branch-2": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
					},
				},
			},
		},
		{
			desc: "concurrently delete loose and packed references",
			skip: func(t *testing.T) {
				testhelper.SkipWithReftable(t, "transactional housekeeping doesn't support reftabled yet")
			},
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-packed":   {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
						"refs/heads/sentinel-packed": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
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
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-loose":   {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
						"refs/heads/sentinel-loose": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Begin{
					TransactionID:       4,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 3,
				},
				Begin{
					TransactionID:       5,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 3,
				},
				Commit{
					TransactionID: 4,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-packed": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				Commit{
					TransactionID: 5,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/branch-loose": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(5).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/sentinel-packed": setup.Commits.First.OID,
							},
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/sentinel-loose": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "delete symbolic reference pointing to non-existent reference",
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						gittest.Exec(tb, cfg,
							"-C", filepath.Join(storagePath, setup.RelativePath),
							"symbolic-ref", "refs/heads/symbolic", "refs/heads/main",
						)
					},
				},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/symbolic": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
			},
		},
		{
			desc: "delete symbolic reference",
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						gittest.Exec(tb, cfg,
							"-C", filepath.Join(storagePath, setup.RelativePath),
							"symbolic-ref", "refs/heads/symbolic", "refs/heads/main",
						)
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
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/symbolic": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "update symbolic reference",
			steps: steps{
				StartManager{
					ModifyStorage: func(tb testing.TB, cfg config.Cfg, storagePath string) {
						gittest.Exec(tb, cfg,
							"-C", filepath.Join(storagePath, setup.RelativePath),
							"symbolic-ref", "refs/heads/symbolic", "refs/heads/main",
						)
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
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/symbolic": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
								// The symbolic reference should be converted to a normal reference if it is
								// updated.
								"refs/heads/symbolic": setup.Commits.Second.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "delete reference ignoring verification failures",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":            {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
						"refs/heads/non-conflicting": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
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
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID:            2,
					SkipVerificationFailures: true,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":            {OldOID: setup.Commits.Second.OID, NewOID: setup.ObjectHash.ZeroOID},
						"refs/heads/non-conflicting": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
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
					},
				},
			},
		},
		{
			desc: "delete reference with incorrect old tip",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":            {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
						"refs/heads/non-conflicting": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
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
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.Second.OID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":            {OldOID: setup.Commits.Second.OID, NewOID: setup.ObjectHash.ZeroOID},
						"refs/heads/non-conflicting": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
					ExpectedError: ReferenceVerificationError{
						ReferenceName:  "refs/heads/main",
						ExpectedOldOID: setup.Commits.Second.OID,
						ActualOldOID:   setup.Commits.First.OID,
						NewOID:         setup.ObjectHash.ZeroOID,
					},
				},
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
								"refs/heads/main":            setup.Commits.First.OID,
								"refs/heads/non-conflicting": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "delete non-existent reference",
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
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
					ExpectedError: ReferenceVerificationError{
						ReferenceName:  "refs/heads/main",
						ExpectedOldOID: setup.Commits.First.OID,
						ActualOldOID:   setup.ObjectHash.ZeroOID,
						NewOID:         setup.ObjectHash.ZeroOID,
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
			},
		},
		{
			desc: "delete reference no-op",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
			},
		},
		{
			desc: "update reference multiple times successfully in a transaction",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				UpdateReferences{
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				UpdateReferences{
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.Second.OID},
					},
				},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
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
			desc: "update reference multiple times fails due to wrong initial value",
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
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				UpdateReferences{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
					},
				},
				UpdateReferences{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						// The old oid should be ignored since there's already a recorded initial value for the
						// reference.
						"refs/heads/main": {NewOID: setup.Commits.Third.OID},
					},
				},
				Commit{
					TransactionID: 1,
					ExpectedError: ReferenceVerificationError{
						ReferenceName:  "refs/heads/main",
						ExpectedOldOID: setup.ObjectHash.ZeroOID,
						ActualOldOID:   setup.Commits.First.OID,
						NewOID:         setup.Commits.Third.OID,
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "recording initial value of a reference stages no updates",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				RecordInitialReferenceValues{
					InitialValues: map[git.ReferenceName]git.Reference{
						"refs/heads/main": git.NewReference("refs/heads/main", setup.Commits.First.OID),
					},
				},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
			},
		},
		{
			desc: "update reference with non-existent initial value",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				RecordInitialReferenceValues{
					InitialValues: map[git.ReferenceName]git.Reference{
						"refs/heads/main": git.NewReference("refs/heads/main", setup.ObjectHash.ZeroOID),
					},
				},
				UpdateReferences{
					// The old oid is ignored as the references old value was already recorded.
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {NewOID: setup.Commits.First.OID},
					},
				},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "update reference with the zero oid initial value",
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
				RecordInitialReferenceValues{
					TransactionID: 2,
					InitialValues: map[git.ReferenceName]git.Reference{
						"refs/heads/main": git.NewReference("refs/heads/main", setup.ObjectHash.ZeroOID),
					},
				},
				UpdateReferences{
					TransactionID: 2,
					// The old oid is ignored as the references old value was already recorded.
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {NewOID: setup.Commits.Second.OID},
					},
				},
				Commit{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
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
			desc: "update reference with the correct initial value",
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
				RecordInitialReferenceValues{
					TransactionID: 2,
					InitialValues: map[git.ReferenceName]git.Reference{
						"refs/heads/main": git.NewReference("refs/heads/main", setup.Commits.First.OID),
					},
				},
				UpdateReferences{
					TransactionID: 2,
					// The old oid is ignored as the references old value was already recorded.
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {NewOID: setup.Commits.Second.OID},
					},
				},
				Commit{
					TransactionID: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
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
			desc: "update reference with the incorrect initial value",
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
				RecordInitialReferenceValues{
					TransactionID: 2,
					InitialValues: map[git.ReferenceName]git.Reference{
						"refs/heads/main": git.NewReference("refs/heads/main", setup.Commits.Third.OID),
					},
				},
				UpdateReferences{
					TransactionID: 2,
					// The old oid is ignored as the references old value was already recorded.
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {NewOID: setup.Commits.Second.OID},
					},
				},
				Commit{
					TransactionID: 2,
					ExpectedError: ReferenceVerificationError{
						ReferenceName:  "refs/heads/main",
						ExpectedOldOID: setup.Commits.Third.OID,
						ActualOldOID:   setup.Commits.First.OID,
						NewOID:         setup.Commits.Second.OID,
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "initial value is set on the first update",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				UpdateReferences{
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				RecordInitialReferenceValues{
					InitialValues: map[git.ReferenceName]git.Reference{
						"refs/heads/main": git.NewReference("refs/heads/main", setup.Commits.Third.OID),
					},
				},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "no-op update of a packed reference",
			skip: func(t *testing.T) {
				testhelper.SkipWithReftable(t, "transactional housekeeping doesn't support reftabled yet")
			},
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
				RunPackRefs{
					TransactionID: 2,
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
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.First.OID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{},
							PackedReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "no-op update of a loose reference",
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
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.Commits.First.OID, NewOID: setup.Commits.First.OID},
					},
				},
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
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
		{
			desc: "remotes directory created by a reference deletion",
			skip: func(t *testing.T) {
				testhelper.SkipWithReftable(t, "transactional housekeeping doesn't support reftabled yet")
			},
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/remotes/upstream/deleted-branch": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				// Move the to-be-deleted reference to packed-refs. Our packing task deletes all empty
				// directories including `refs/remote`.
				RunPackRefs{
					TransactionID: 2,
				},
				Commit{
					TransactionID: 2,
				},
				Begin{
					TransactionID:       3,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 2,
				},
				UpdateReferences{
					TransactionID: 3,
					// Delete the branch in the same transaction as we create another one in the `refs/remotes`
					// directory. The reference deletion creates the `refs/remotes` directory that was removed
					// by the repacking task.
					ReferenceUpdates: ReferenceUpdates{
						"refs/remotes/upstream/deleted-branch": {OldOID: setup.Commits.First.OID, NewOID: setup.ObjectHash.ZeroOID},
					},
				},
				UpdateReferences{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/remotes/upstream/created-branch": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Commit{
					TransactionID: 3,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							PackedReferences: map[git.ReferenceName]git.ObjectID{},
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/remotes/upstream/created-branch": setup.Commits.First.OID,
							},
						},
					},
				},
			},
		},
	}
}
