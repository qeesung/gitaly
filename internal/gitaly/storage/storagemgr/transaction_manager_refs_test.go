package storagemgr

import (
	"fmt"
	"path/filepath"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
)

func generateInvalidReferencesTests(t *testing.T, setup testTransactionSetup) []transactionTestCase {
	type invalidReferenceTestCase struct {
		desc          string
		referenceName git.ReferenceName
	}

	commit := setup.Commits.First
	testCases := []transactionTestCase{
		{
			desc: "invalid reference aborts the entire transaction",
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					SkipVerificationFailures: true,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main":    {OldOID: setup.ObjectHash.ZeroOID, NewOID: commit.OID},
						"refs/heads/../main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: commit.OID},
					},
					ExpectedError: InvalidReferenceFormatError{ReferenceName: "refs/heads/../main"},
				},
			},
		},
		{
			desc: "continues processing after aborting due to an invalid reference",
			steps: steps{
				StartManager{},
				Begin{
					TransactionID: 1,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 1,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/../main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: commit.OID},
					},
					ExpectedError: InvalidReferenceFormatError{ReferenceName: "refs/heads/../main"},
				},
				Begin{
					TransactionID: 2,
					RelativePath:  setup.RelativePath,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: commit.OID},
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
								"refs/heads/main": commit.OID,
							},
						},
					},
				},
			},
		},
	}

	appendInvalidReferenceTestCase := func(tc invalidReferenceTestCase) {
		testCases = append(testCases, transactionTestCase{
			desc: fmt.Sprintf("invalid reference %s", tc.desc),
			steps: steps{
				StartManager{},
				Begin{
					RelativePath: setup.RelativePath,
				},
				Commit{
					ReferenceUpdates: ReferenceUpdates{
						tc.referenceName: {OldOID: setup.ObjectHash.ZeroOID, NewOID: commit.OID},
					},
					ExpectedError: InvalidReferenceFormatError{ReferenceName: tc.referenceName},
				},
			},
		})
	}

	// Generate test cases for the reference format rules according to https://git-scm.com/docs/git-check-ref-format.
	// This is to ensure the references are correctly validated prior to logging so they are guaranteed to apply later.
	// We also have two levels for catching invalid refs, the first is part of the transaction_manager, the second is
	// the errors thrown by git-update-ref(1) itself.
	for _, tc := range []invalidReferenceTestCase{
		// 1. They can include slash / for hierarchical (directory) grouping, but no slash-separated
		// component can begin with a dot . or end with the sequence .lock.
		{"starting with a period", ".refs/heads/main"},
		{"subcomponent starting with a period", "refs/heads/.main"},
		{"ending in .lock", "refs/heads/main.lock"},
		{"subcomponent ending in .lock", "refs/heads/main.lock/main"},
		// 2. They must contain at least one /. This enforces the presence of a category like heads/,
		// tags/ etc. but the actual names are not restricted.
		{"without a /", "one-level"},
		{"with refs without a /", "refs"},
		// We restrict this further by requiring a 'refs/' prefix to ensure loose references only end up
		// in the 'refs/' folder.
		{"without refs/ prefix ", "nonrefs/main"},
		// 3. They cannot have two consecutive dots .. anywhere.
		{"containing two consecutive dots", "refs/heads/../main"},
		// 4. They cannot have ASCII control characters ... (\177 DEL), space, tilde ~, caret ^, or colon : anywhere.
		//
		// Tests for control characters < \040 generated further down.
		{"containing DEL", "refs/heads/ma\177in"},
		{"containing space", "refs/heads/ma in"},
		{"containing ~", "refs/heads/ma~in"},
		{"containing ^", "refs/heads/ma^in"},
		{"containing :", "refs/heads/ma:in"},
		// 5. They cannot have question-mark ?, asterisk *, or open bracket [ anywhere.
		{"containing ?", "refs/heads/ma?in"},
		{"containing *", "refs/heads/ma*in"},
		{"containing [", "refs/heads/ma[in"},
		// 6. They cannot begin or end with a slash / or contain multiple consecutive slashes
		{"begins with /", "/refs/heads/main"},
		{"ends with /", "refs/heads/main/"},
		{"contains consecutive /", "refs/heads//main"},
		// 7. They cannot end with a dot.
		{"ending in a dot", "refs/heads/main."},
		// 8. They cannot contain a sequence @{.
		{"invalid reference contains @{", "refs/heads/m@{n"},
		// 9. They cannot be the single character @.
		{"is a single character @", "@"},
		// 10. They cannot contain a \.
		{`containing \`, `refs/heads\main`},
	} {
		appendInvalidReferenceTestCase(tc)
	}

	// Rule 4. They cannot have ASCII control characters i.e. bytes whose values are lower than \040,
	for i := byte(0); i < '\040'; i++ {
		appendInvalidReferenceTestCase(invalidReferenceTestCase{
			desc:          fmt.Sprintf(`containing ASCII control character %d`, i),
			referenceName: git.ReferenceName(fmt.Sprintf("refs/heads/ma%sin", []byte{i})),
		})
	}

	return testCases
}

func generateModifyReferencesTests(t *testing.T, setup testTransactionSetup) []transactionTestCase {
	return []transactionTestCase{
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
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
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/parent": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.ObjectHash.ZeroOID},
					},
					ExpectedError: updateref.FileDirectoryConflictError{
						ExistingReferenceName:    "refs/heads/parent/child",
						ConflictingReferenceName: "refs/heads/parent",
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
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
					Config: housekeeping.RepackObjectsConfig{
						Strategy: housekeeping.RepackObjectsStrategyFullWithCruft,
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
					ExpectedError: updateref.NonExistentObjectError{
						ReferenceName: "refs/heads/main",
						ObjectID:      setup.Commits.First.OID.String(),
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
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
						ReferenceName: "refs/heads/main",
						ExpectedOID:   setup.ObjectHash.ZeroOID,
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
						ReferenceName: "refs/heads/main",
						ExpectedOID:   setup.ObjectHash.ZeroOID,
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
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
						ReferenceName: "refs/heads/main",
						ExpectedOID:   setup.Commits.Second.OID,
						ActualOID:     setup.Commits.First.OID,
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
						ReferenceName: "refs/heads/main",
						ExpectedOID:   setup.Commits.First.OID,
						ActualOID:     setup.ObjectHash.ZeroOID,
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(3).toProto(),
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
						ReferenceName: "refs/heads/main",
						ExpectedOID:   setup.Commits.Second.OID,
						ActualOID:     setup.Commits.First.OID,
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
						ReferenceName: "refs/heads/main",
						ExpectedOID:   setup.Commits.First.OID,
						ActualOID:     setup.ObjectHash.ZeroOID,
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
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
						ReferenceName: "refs/heads/main",
						ExpectedOID:   setup.ObjectHash.ZeroOID,
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
					InitialValues: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
					},
				},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
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
					InitialValues: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.ObjectHash.ZeroOID,
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
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
					InitialValues: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.ObjectHash.ZeroOID,
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
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
					InitialValues: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.First.OID,
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
					string(keyAppliedLSN(setup.PartitionID)): LSN(2).toProto(),
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
					InitialValues: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.Third.OID,
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
						ReferenceName: "refs/heads/main",
						ExpectedOID:   setup.Commits.Third.OID,
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
					InitialValues: map[git.ReferenceName]git.ObjectID{
						"refs/heads/main": setup.Commits.Third.OID,
					},
				},
				Commit{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN(setup.PartitionID)): LSN(1).toProto(),
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
	}
}
