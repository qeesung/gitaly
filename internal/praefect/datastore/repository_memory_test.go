package datastore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

type requireState func(t *testing.T, ctx context.Context, vss virtualStorageState, ss storageState)
type repositoryStoreFactory func(t *testing.T, storages map[string][]string) (RepositoryStore, requireState)

func TestRepositoryStore_Memory(t *testing.T) {
	testRepositoryStore(t, func(t *testing.T, storages map[string][]string) (RepositoryStore, requireState) {
		rs := NewMemoryRepositoryStore(storages)
		return rs, func(t *testing.T, _ context.Context, vss virtualStorageState, ss storageState) {
			t.Helper()
			require.Equal(t, vss, rs.virtualStorageState)
			require.Equal(t, ss, rs.storageState)
		}
	})
}

func testRepositoryStore(t *testing.T, newStore repositoryStoreFactory) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	const (
		vs   = "virtual-storage-1"
		repo = "repository-1"
		stor = "storage-1"
	)

	t.Run("IncrementGeneration", func(t *testing.T) {
		t.Run("creates a new record for primary", func(t *testing.T) {
			rs, requireState := newStore(t, nil)

			generation, err := rs.IncrementGeneration(ctx, vs, repo, "primary", []string{"secondary-1"})
			require.NoError(t, err)
			require.Equal(t, 0, generation)
			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"repository-1": 0,
					},
				},
				storageState{
					"virtual-storage-1": {
						"repository-1": {
							"primary": 0,
						},
					},
				},
			)
		})

		t.Run("increments existing record for primary", func(t *testing.T) {
			rs, requireState := newStore(t, nil)

			require.NoError(t, rs.SetGeneration(ctx, vs, repo, "primary", 0))
			generation, err := rs.IncrementGeneration(ctx, vs, repo, "primary", []string{"secondary-1"})
			require.NoError(t, err)
			require.Equal(t, 1, generation)
			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"repository-1": 1,
					},
				},
				storageState{
					"virtual-storage-1": {
						"repository-1": {
							"primary": 1,
						},
					},
				},
			)
		})

		t.Run("increments existing for up to date secondary", func(t *testing.T) {
			rs, requireState := newStore(t, nil)

			require.NoError(t, rs.SetGeneration(ctx, vs, repo, "primary", 1))
			require.NoError(t, rs.SetGeneration(ctx, vs, repo, "up-to-date-secondary", 1))
			require.NoError(t, rs.SetGeneration(ctx, vs, repo, "outdated-secondary", 0))
			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"repository-1": 1,
					},
				},
				storageState{
					"virtual-storage-1": {
						"repository-1": {
							"primary":              1,
							"up-to-date-secondary": 1,
							"outdated-secondary":   0,
						},
					},
				},
			)

			generation, err := rs.IncrementGeneration(ctx, vs, repo, "primary", []string{
				"up-to-date-secondary", "outdated-secondary", "non-existing-secondary"})
			require.NoError(t, err)
			require.Equal(t, 2, generation)
			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"repository-1": 2,
					},
				},
				storageState{
					"virtual-storage-1": {
						"repository-1": {
							"primary":              2,
							"up-to-date-secondary": 2,
							"outdated-secondary":   0,
						},
					},
				},
			)
		})
	})

	t.Run("SetGeneration", func(t *testing.T) {
		t.Run("creates a record", func(t *testing.T) {
			rs, requireState := newStore(t, nil)

			err := rs.SetGeneration(ctx, vs, repo, stor, 1)
			require.NoError(t, err)
			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"repository-1": 1,
					},
				},
				storageState{
					"virtual-storage-1": {
						"repository-1": {
							"storage-1": 1,
						},
					},
				},
			)
		})

		t.Run("updates existing record", func(t *testing.T) {
			rs, requireState := newStore(t, nil)

			require.NoError(t, rs.SetGeneration(ctx, vs, repo, stor, 1))
			require.NoError(t, rs.SetGeneration(ctx, vs, repo, stor, 0))
			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"repository-1": 1,
					},
				},
				storageState{
					"virtual-storage-1": {
						"repository-1": {
							"storage-1": 0,
						},
					},
				},
			)
		})

		t.Run("increments stays monotonic", func(t *testing.T) {
			rs, requireState := newStore(t, nil)

			require.NoError(t, rs.SetGeneration(ctx, vs, repo, stor, 1))
			require.NoError(t, rs.SetGeneration(ctx, vs, repo, stor, 0))

			generation, err := rs.IncrementGeneration(ctx, vs, repo, stor, nil)
			require.NoError(t, err)
			require.Equal(t, 2, generation)

			generation, err = rs.IncrementGeneration(ctx, vs, repo, "storage-2", nil)
			require.NoError(t, err)
			require.Equal(t, 3, generation)

			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"repository-1": 3,
					},
				},
				storageState{
					"virtual-storage-1": {
						"repository-1": {
							"storage-1": 2,
							"storage-2": 3,
						},
					},
				},
			)
		})
	})

	t.Run("GetGeneration", func(t *testing.T) {
		rs, _ := newStore(t, nil)

		generation, err := rs.GetGeneration(ctx, vs, repo, stor)
		require.NoError(t, err)
		require.Equal(t, GenerationUnknown, generation)

		require.NoError(t, rs.SetGeneration(ctx, vs, repo, stor, 0))

		generation, err = rs.GetGeneration(ctx, vs, repo, stor)
		require.NoError(t, err)
		require.Equal(t, 0, generation)
	})

	t.Run("EnsureUpgrade", func(t *testing.T) {
		t.Run("no previous record allowed", func(t *testing.T) {
			rs, _ := newStore(t, nil)
			require.NoError(t, rs.EnsureUpgrade(ctx, vs, repo, stor, GenerationUnknown))
			require.NoError(t, rs.EnsureUpgrade(ctx, vs, repo, stor, 0))
		})

		t.Run("upgrade allowed", func(t *testing.T) {
			rs, requireState := newStore(t, nil)

			require.NoError(t, rs.SetGeneration(ctx, vs, repo, stor, 0))
			require.NoError(t, rs.EnsureUpgrade(ctx, vs, repo, stor, 1))
			require.Error(t,
				downgradeAttemptedError{vs, repo, stor, 1, GenerationUnknown},
				rs.EnsureUpgrade(ctx, vs, repo, stor, GenerationUnknown))
			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"repository-1": 0,
					},
				},
				storageState{
					"virtual-storage-1": {
						"repository-1": {
							"storage-1": 0,
						},
					},
				},
			)
		})

		t.Run("downgrade prevented", func(t *testing.T) {
			rs, requireState := newStore(t, nil)

			require.NoError(t, rs.SetGeneration(ctx, vs, repo, stor, 1))
			require.Equal(t,
				downgradeAttemptedError{vs, repo, stor, 1, 0},
				rs.EnsureUpgrade(ctx, vs, repo, stor, 0))
			require.Error(t,
				downgradeAttemptedError{vs, repo, stor, 1, GenerationUnknown},
				rs.EnsureUpgrade(ctx, vs, repo, stor, GenerationUnknown))
			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"repository-1": 1,
					},
				},
				storageState{
					"virtual-storage-1": {
						"repository-1": {
							"storage-1": 1,
						},
					},
				},
			)
		})

		t.Run("same version prevented", func(t *testing.T) {
			rs, requireState := newStore(t, nil)

			require.NoError(t, rs.SetGeneration(ctx, vs, repo, stor, 1))
			require.Equal(t,
				downgradeAttemptedError{vs, repo, stor, 1, 1},
				rs.EnsureUpgrade(ctx, vs, repo, stor, 1))
			require.Error(t,
				downgradeAttemptedError{vs, repo, stor, 1, GenerationUnknown},
				rs.EnsureUpgrade(ctx, vs, repo, stor, GenerationUnknown))
			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"repository-1": 1,
					},
				},
				storageState{
					"virtual-storage-1": {
						"repository-1": {
							"storage-1": 1,
						},
					},
				},
			)
		})
	})

	t.Run("DeleteRepository", func(t *testing.T) {
		t.Run("delete non-existing", func(t *testing.T) {
			rs, _ := newStore(t, nil)

			require.Equal(t,
				RepositoryNotExistsError{vs, repo, stor},
				rs.DeleteRepository(ctx, vs, repo, stor),
			)
		})

		t.Run("delete existing", func(t *testing.T) {
			rs, requireState := newStore(t, nil)

			require.NoError(t, rs.SetGeneration(ctx, "deleted", "deleted", "deleted", 0))

			require.NoError(t, rs.SetGeneration(ctx, "virtual-storage-1", "other-storages-remain", "deleted-storage", 0))
			require.NoError(t, rs.SetGeneration(ctx, "virtual-storage-1", "other-storages-remain", "remaining-storage", 0))

			require.NoError(t, rs.SetGeneration(ctx, "virtual-storage-2", "deleted-repo", "deleted-storage", 0))
			require.NoError(t, rs.SetGeneration(ctx, "virtual-storage-2", "other-repo-remains", "remaining-storage", 0))

			requireState(t, ctx,
				virtualStorageState{
					"deleted": {
						"deleted": 0,
					},
					"virtual-storage-1": {
						"other-storages-remain": 0,
					},
					"virtual-storage-2": {
						"deleted-repo":       0,
						"other-repo-remains": 0,
					},
				},
				storageState{
					"deleted": {
						"deleted": {
							"deleted": 0,
						},
					},
					"virtual-storage-1": {
						"other-storages-remain": {
							"deleted-storage":   0,
							"remaining-storage": 0,
						},
					},
					"virtual-storage-2": {
						"deleted-repo": {
							"deleted-storage": 0,
						},
						"other-repo-remains": {
							"remaining-storage": 0,
						},
					},
				},
			)

			require.NoError(t, rs.DeleteRepository(ctx, "deleted", "deleted", "deleted"))
			require.NoError(t, rs.DeleteRepository(ctx, "virtual-storage-1", "other-storages-remain", "deleted-storage"))
			require.NoError(t, rs.DeleteRepository(ctx, "virtual-storage-2", "deleted-repo", "deleted-storage"))

			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-2": {
						"other-repo-remains": 0,
					},
				},
				storageState{
					"virtual-storage-1": {
						"other-storages-remain": {
							"remaining-storage": 0,
						},
					},
					"virtual-storage-2": {
						"other-repo-remains": {
							"remaining-storage": 0,
						},
					},
				},
			)
		})
	})

	t.Run("RenameRepository", func(t *testing.T) {
		t.Run("rename non-existing", func(t *testing.T) {
			rs, _ := newStore(t, nil)

			require.Equal(t,
				RepositoryNotExistsError{vs, repo, stor},
				rs.RenameRepository(ctx, vs, repo, stor, "repository-2"),
			)
		})

		t.Run("rename existing", func(t *testing.T) {
			rs, requireState := newStore(t, nil)

			require.NoError(t, rs.SetGeneration(ctx, vs, "renamed-all", "storage-1", 0))
			require.NoError(t, rs.SetGeneration(ctx, vs, "renamed-some", "storage-1", 0))
			require.NoError(t, rs.SetGeneration(ctx, vs, "renamed-some", "storage-2", 0))

			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"renamed-all":  0,
						"renamed-some": 0,
					},
				},
				storageState{
					"virtual-storage-1": {
						"renamed-all": {
							"storage-1": 0,
						},
						"renamed-some": {
							"storage-1": 0,
							"storage-2": 0,
						},
					},
				},
			)

			require.NoError(t, rs.RenameRepository(ctx, vs, "renamed-all", "storage-1", "renamed-all-new"))
			require.NoError(t, rs.RenameRepository(ctx, vs, "renamed-some", "storage-1", "renamed-some-new"))

			requireState(t, ctx,
				virtualStorageState{
					"virtual-storage-1": {
						"renamed-all-new":  0,
						"renamed-some-new": 0,
					},
				},
				storageState{
					"virtual-storage-1": {
						"renamed-all-new": {
							"storage-1": 0,
						},
						"renamed-some-new": {
							"storage-1": 0,
						},
						"renamed-some": {
							"storage-2": 0,
						},
					},
				},
			)
		})
	})
}
