package storagemgr

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dgraph-io/badger/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

type partitionAssignments map[string]partitionID

func getPartitionAssignments(tb testing.TB, db Database) partitionAssignments {
	tb.Helper()

	state := partitionAssignments{}
	require.NoError(tb, db.View(func(txn DatabaseTransaction) error {
		it := txn.NewIterator(badger.IteratorOptions{
			Prefix: []byte(prefixPartitionAssignment),
		})
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			value, err := it.Item().ValueCopy(nil)
			require.NoError(tb, err)

			var ptnID partitionID
			ptnID.UnmarshalBinary(value)

			relativePath := strings.TrimPrefix(string(it.Item().Key()), prefixPartitionAssignment)
			state[relativePath] = ptnID
		}

		return nil
	}))

	return state
}

func TestPartitionAssigner(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc                string
		run                 func(t *testing.T, ctx context.Context, cfg config.Cfg, pa *partitionAssigner)
		expectedAssignments partitionAssignments
	}{
		{
			desc: "repositories get assigned into partitions",
			run: func(t *testing.T, ctx context.Context, cfg config.Cfg, pa *partitionAssigner) {
				ptnID1, err := pa.getPartitionID(ctx, "repository-1", "")
				require.NoError(t, err)
				require.EqualValues(t, ptnID1, 1)

				ptnID2, err := pa.getPartitionID(ctx, "repository-2", "")
				require.NoError(t, err)
				require.EqualValues(t, ptnID2, 2)
			},
			expectedAssignments: partitionAssignments{
				"repository-1": 1,
				"repository-2": 2,
			},
		},
		{
			desc: "repository's assigned partition is returned",
			run: func(t *testing.T, ctx context.Context, cfg config.Cfg, pa *partitionAssigner) {
				assignedID, err := pa.getPartitionID(ctx, "repository-1", "")
				require.NoError(t, err)
				require.EqualValues(t, assignedID, 1)

				retrievedID, err := pa.getPartitionID(ctx, "repository-1", "")
				require.NoError(t, err)
				require.Equal(t, assignedID, retrievedID)
			},
			expectedAssignments: partitionAssignments{
				"repository-1": 1,
			},
		},
		{
			desc: "repository and alternate are assigned into same partition",
			run: func(t *testing.T, ctx context.Context, cfg config.Cfg, pa *partitionAssigner) {
				assignedID, err := pa.getPartitionID(ctx, "repository", "alternate")
				require.NoError(t, err)
				require.EqualValues(t, assignedID, 1)
			},
			expectedAssignments: partitionAssignments{
				"repository": 1,
				"alternate":  1,
			},
		},
		{
			desc: "repository is assigned into the same partition as alternate",
			run: func(t *testing.T, ctx context.Context, cfg config.Cfg, pa *partitionAssigner) {
				ptnID1, err := pa.getPartitionID(ctx, "alternate", "")
				require.NoError(t, err)
				require.EqualValues(t, ptnID1, 1)

				ptnID2, err := pa.getPartitionID(ctx, "repository", "alternate")
				require.NoError(t, err)
				require.EqualValues(t, ptnID2, ptnID1)
			},
			expectedAssignments: partitionAssignments{
				"repository": 1,
				"alternate":  1,
			},
		},
		{
			desc: "alternate is assigned into the same partition as repository",
			run: func(t *testing.T, ctx context.Context, cfg config.Cfg, pa *partitionAssigner) {
				ptnID1, err := pa.getPartitionID(ctx, "repository", "")
				require.NoError(t, err)
				require.EqualValues(t, ptnID1, 1)

				ptnID2, err := pa.getPartitionID(ctx, "repository", "alternate")
				require.NoError(t, err)
				require.EqualValues(t, ptnID2, ptnID1)
			},
			expectedAssignments: partitionAssignments{
				"repository": 1,
				"alternate":  1,
			},
		},
		{
			desc: "getting a partition fails is repositories are in different partitions",
			run: func(t *testing.T, ctx context.Context, cfg config.Cfg, pa *partitionAssigner) {
				ptnID1, err := pa.getPartitionID(ctx, "repository-1", "")
				require.NoError(t, err)
				require.EqualValues(t, ptnID1, 1)

				ptnID2, err := pa.getPartitionID(ctx, "repository-2", "")
				require.NoError(t, err)
				require.EqualValues(t, ptnID2, 2)

				ptnID, err := pa.getPartitionID(ctx, "repository-1", "repository-2")
				require.Equal(t, ErrRepositoriesAreInDifferentPartitions, err)
				require.Zero(t, ptnID)
			},
			expectedAssignments: partitionAssignments{
				"repository-1": 1,
				"repository-2": 2,
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			db, err := OpenDatabase(testhelper.SharedLogger(t), t.TempDir())
			require.NoError(t, err)
			defer testhelper.MustClose(t, db)

			cfg := testcfg.Build(t)
			pa, err := newPartitionAssigner(db, cfg.Storages[0].Path)
			require.NoError(t, err)
			defer testhelper.MustClose(t, pa)

			tc.run(t, testhelper.Context(t), cfg, pa)

			require.Equal(t, tc.expectedAssignments, getPartitionAssignments(t, db))
		})
	}
}

func TestPartitionAssigner_alternates(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc                         string
		memberAlternatesContent      []byte
		poolAlternatesContent        []byte
		expectedError                error
		expectedPartitionAssignments partitionAssignments
	}{
		{
			desc: "no alternates file",
			expectedPartitionAssignments: partitionAssignments{
				"member": 1,
				"pool":   2,
			},
		},
		{
			desc:                    "empty alternates file",
			memberAlternatesContent: []byte(""),
			expectedPartitionAssignments: partitionAssignments{
				"member": 1,
				"pool":   2,
			},
		},
		{
			desc:                    "not a git directory",
			memberAlternatesContent: []byte("../.."),
			expectedError:           storage.InvalidGitDirectoryError{MissingEntry: "objects"},
		},
		{
			desc:                    "points to pool",
			memberAlternatesContent: []byte("../../pool/objects"),
			expectedPartitionAssignments: partitionAssignments{
				"member": 1,
				"pool":   1,
			},
		},
		{
			desc:                    "points to pool with newline",
			memberAlternatesContent: []byte("../../pool/objects\n"),
			expectedPartitionAssignments: partitionAssignments{
				"member": 1,
				"pool":   1,
			},
		},
		{
			desc:                    "multiple alternates fail",
			memberAlternatesContent: []byte("../../pool/objects\nother-alternate"),
			expectedError:           errMultipleAlternates,
		},
		{
			desc:                    "alternate pointing to self fails",
			memberAlternatesContent: []byte("../objects"),
			expectedError:           errAlternatePointsToSelf,
		},
		{
			desc:                    "alternate having an alternate fails",
			poolAlternatesContent:   []byte("unexpected"),
			memberAlternatesContent: []byte("../../pool/objects"),
			expectedError:           errAlternateHasAlternate,
		},
		{
			desc:                    "alternate points outside the storage",
			memberAlternatesContent: []byte("../../../../.."),
			expectedError:           storage.ErrRelativePathEscapesRoot,
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			cfg := testcfg.Build(t)

			ctx := testhelper.Context(t)
			poolRepo, poolPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
				RelativePath:           "pool",
			})

			memberRepo, memberPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
				RelativePath:           "member",
			})

			writeAlternatesFile := func(t *testing.T, repoPath string, content []byte) {
				t.Helper()
				require.NoError(t, os.WriteFile(stats.AlternatesFilePath(repoPath), content, os.ModePerm))
			}

			if tc.poolAlternatesContent != nil {
				writeAlternatesFile(t, poolPath, tc.poolAlternatesContent)
			}

			if tc.memberAlternatesContent != nil {
				writeAlternatesFile(t, memberPath, tc.memberAlternatesContent)
			}

			db, err := OpenDatabase(testhelper.NewLogger(t), t.TempDir())
			require.NoError(t, err)
			defer testhelper.MustClose(t, db)

			pa, err := newPartitionAssigner(db, cfg.Storages[0].Path)
			require.NoError(t, err)
			defer testhelper.MustClose(t, pa)

			expectedPartitionAssignments := tc.expectedPartitionAssignments
			if expectedPartitionAssignments == nil {
				expectedPartitionAssignments = partitionAssignments{}
			}

			if memberPartitionID, err := pa.getPartitionID(ctx, memberRepo.RelativePath, ""); tc.expectedError != nil {
				require.ErrorIs(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, expectedPartitionAssignments["member"], memberPartitionID)

				poolPartitionID, err := pa.getPartitionID(ctx, poolRepo.RelativePath, "")
				require.NoError(t, err)
				require.Equal(t, expectedPartitionAssignments["pool"], poolPartitionID)
			}

			require.Equal(t, expectedPartitionAssignments, getPartitionAssignments(t, db))
		})
	}
}

func TestPartitionAssigner_close(t *testing.T) {
	dbDir := t.TempDir()

	db, err := OpenDatabase(testhelper.SharedLogger(t), dbDir)
	require.NoError(t, err)

	cfg := testcfg.Build(t)

	pa, err := newPartitionAssigner(db, cfg.Storages[0].Path)
	require.NoError(t, err)
	testhelper.MustClose(t, pa)
	testhelper.MustClose(t, db)

	db, err = OpenDatabase(testhelper.SharedLogger(t), dbDir)
	require.NoError(t, err)
	defer testhelper.MustClose(t, db)

	pa, err = newPartitionAssigner(db, cfg.Storages[0].Path)
	require.NoError(t, err)
	defer testhelper.MustClose(t, pa)

	// A block of ID is loaded into memory when the partitionAssigner is initialized.
	// Closing the partitionAssigner is expected to return the unused IDs in the block
	// back to the database.
	ptnID, err := pa.getPartitionID(testhelper.Context(t), "relative-path", "")
	require.NoError(t, err)
	require.EqualValues(t, 1, ptnID)
}

func TestPartitionAssigner_concurrentAccess(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc          string
		withAlternate bool
	}{
		{
			desc: "without alternate",
		},
		{
			desc:          "with alternate",
			withAlternate: true,
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			db, err := OpenDatabase(testhelper.SharedLogger(t), t.TempDir())
			require.NoError(t, err)
			defer testhelper.MustClose(t, db)

			cfg := testcfg.Build(t)

			pa, err := newPartitionAssigner(db, cfg.Storages[0].Path)
			require.NoError(t, err)
			defer testhelper.MustClose(t, pa)

			// Access 10 repositories concurrently.
			repositoryCount := 10
			// Access each repository from 10 goroutines concurrently.
			goroutineCount := 10

			collectedIDs := make([][]partitionID, repositoryCount)
			ctx := testhelper.Context(t)
			wg := sync.WaitGroup{}
			start := make(chan struct{})

			pool, poolPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})

			for i := 0; i < repositoryCount; i++ {
				i := i
				collectedIDs[i] = make([]partitionID, goroutineCount)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				if tc.withAlternate {
					// Link the repositories to the pool.
					alternateRelativePath, err := filepath.Rel(
						filepath.Join(repoPath, "objects"),
						filepath.Join(poolPath, "objects"),
					)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filepath.Join(repoPath, "objects", "info", "alternates"), []byte(alternateRelativePath), fs.ModePerm))

					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						_, err := pa.getPartitionID(ctx, repo.RelativePath, "")
						assert.NoError(t, err)
					}()
				}

				for j := 0; j < goroutineCount; j++ {
					j := j
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						ptnID, err := pa.getPartitionID(ctx, repo.RelativePath, "")
						assert.NoError(t, err)
						collectedIDs[i][j] = ptnID
					}()
				}
			}

			close(start)
			wg.Wait()

			var partitionIDs []partitionID
			for _, ids := range collectedIDs {
				partitionIDs = append(partitionIDs, ids[0])
				for i := range ids {
					// We expect all goroutines accessing a given repository to get the
					// same partition ID for it.
					require.Equal(t, ids[0], ids[i], ids)
				}
			}

			if tc.withAlternate {
				// We expect all repositories to have been assigned to the same partition as they are all linked to the same pool.
				require.Equal(t, []partitionID{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, partitionIDs)
				ptnID, err := pa.getPartitionID(ctx, pool.RelativePath, "")
				require.NoError(t, err)
				require.Equal(t, partitionID(1), ptnID, "pool should have been assigned into the same partition as the linked repositories")
				return
			}

			// We expect to have 10 unique partition IDs as there are 10 repositories being accessed.
			require.ElementsMatch(t, []partitionID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, partitionIDs)
		})
	}
}
