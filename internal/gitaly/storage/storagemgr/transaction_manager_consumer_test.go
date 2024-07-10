package storagemgr

import (
	"context"
	"io/fs"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func generateConsumerTests(t *testing.T, setup testTransactionSetup) []transactionTestCase {
	customSetup := func(t *testing.T, ctx context.Context, testPartitionID storage.PartitionID, relativePath string) testTransactionSetup {
		setup := setupTest(t, ctx, testPartitionID, relativePath)
		setup.Consumer = &MockLogConsumer{}

		return setup
	}

	return []transactionTestCase{
		{
			desc:        "unacknowledged entry not pruned",
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
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                           {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":                        {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/0000000000001":          {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/0000000000001/MANIFEST": manifestDirectoryEntry(refChangeLogEntry(setup, "refs/heads/main", setup.Commits.First.OID)),
					"/wal/0000000000001/1":        {Mode: storage.ModeFile, Content: []byte(setup.Commits.First.OID + "\n")},
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
				Consumers: ConsumerState{
					ManagerPosition: 0,
					HighWaterMark:   1,
				},
			},
		},
		{
			desc:        "acknowledged entry pruned",
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
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				ConsumerAcknowledge{
					LSN: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
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
								"refs/heads/main": setup.Commits.First.OID,
							},
						},
					},
				},
				Consumers: ConsumerState{
					ManagerPosition: 1,
					HighWaterMark:   1,
				},
			},
		},
		{
			desc:        "acknowledging a later entry prunes prior entries",
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
				ConsumerAcknowledge{
					LSN: 2,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(2).ToProto(),
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
								"refs/heads/main": setup.Commits.Second.OID,
							},
						},
					},
				},
				Consumers: ConsumerState{
					ManagerPosition: 2,
					HighWaterMark:   2,
				},
			},
		},
		{
			desc:        "dependent transaction blocks pruning acknowledged entry",
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
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				ConsumerAcknowledge{
					LSN: 1,
				},
				CloseManager{},
				Commit{
					TransactionID: 2,
					ExpectedError: ErrTransactionProcessingStopped,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                           {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":                        {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/0000000000001":          {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/0000000000001/MANIFEST": manifestDirectoryEntry(refChangeLogEntry(setup, "refs/heads/main", setup.Commits.First.OID)),
					"/wal/0000000000001/1":        {Mode: storage.ModeFile, Content: []byte(setup.Commits.First.OID + "\n")},
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
				Consumers: ConsumerState{
					ManagerPosition: 1,
					HighWaterMark:   1,
				},
			},
		},
		{
			desc:        "consumer position zeroed lsn on restart",
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
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				ConsumerAcknowledge{
					LSN: 1,
				},
				Begin{
					TransactionID:       2,
					RelativePath:        setup.RelativePath,
					ExpectedSnapshotLSN: 1,
				},
				Commit{
					TransactionID: 2,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/other": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Second.OID},
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
				ConsumerAcknowledge{
					LSN: 2,
				},
				Commit{
					TransactionID: 3,
					ReferenceUpdates: ReferenceUpdates{
						"refs/heads/third": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.Third.OID},
					},
				},
				ConsumerAcknowledge{
					LSN: 3,
				},
				CloseManager{},
				StartManager{},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(3).ToProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                           {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":                        {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/0000000000002":          {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/0000000000002/MANIFEST": manifestDirectoryEntry(refChangeLogEntry(setup, "refs/heads/other", setup.Commits.Second.OID)),
					"/wal/0000000000002/1":        {Mode: storage.ModeFile, Content: []byte(setup.Commits.Second.OID + "\n")},
					"/wal/0000000000003":          {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/0000000000003/MANIFEST": manifestDirectoryEntry(refChangeLogEntry(setup, "refs/heads/third", setup.Commits.Third.OID)),
					"/wal/0000000000003/1":        {Mode: storage.ModeFile, Content: []byte(setup.Commits.Third.OID + "\n")},
				},
				Repositories: RepositoryStates{
					setup.RelativePath: {
						DefaultBranch: "refs/heads/main",
						References: &ReferencesState{
							LooseReferences: map[git.ReferenceName]git.ObjectID{
								"refs/heads/main":  setup.Commits.First.OID,
								"refs/heads/other": setup.Commits.Second.OID,
								"refs/heads/third": setup.Commits.Third.OID,
							},
						},
					},
				},
				Consumers: ConsumerState{
					ManagerPosition: 0,
					HighWaterMark:   3,
				},
			},
		},
		{
			desc:        "stopped manager does not prune acknowledged entry",
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
						"refs/heads/main": {OldOID: setup.ObjectHash.ZeroOID, NewOID: setup.Commits.First.OID},
					},
				},
				CloseManager{},
				ConsumerAcknowledge{
					LSN: 1,
				},
			},
			expectedState: StateAssertion{
				Database: DatabaseState{
					string(keyAppliedLSN): storage.LSN(1).ToProto(),
				},
				Directory: testhelper.DirectoryState{
					"/":                           {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal":                        {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/0000000000001":          {Mode: fs.ModeDir | perm.PrivateDir},
					"/wal/0000000000001/MANIFEST": manifestDirectoryEntry(refChangeLogEntry(setup, "refs/heads/main", setup.Commits.First.OID)),
					"/wal/0000000000001/1":        {Mode: storage.ModeFile, Content: []byte(setup.Commits.First.OID + "\n")},
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
				Consumers: ConsumerState{
					ManagerPosition: 1,
					HighWaterMark:   1,
				},
			},
		},
	}
}
