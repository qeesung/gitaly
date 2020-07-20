// +build postgres

package datastore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepositoryStore_Postgres(t *testing.T) {
	testRepositoryStore(t, func(t *testing.T, storages map[string][]string) (RepositoryStore, requireState) {
		db := getDB(t)
		gs := NewPostgresRepositoryStore(db, storages)

		requireVirtualStorageState := func(t *testing.T, ctx context.Context, exp virtualStorageState) {
			rows, err := db.QueryContext(ctx, `
SELECT virtual_storage, relative_path, generation
FROM repositories
				`)
			require.NoError(t, err)
			defer rows.Close()

			act := make(virtualStorageState)
			for rows.Next() {
				var vs, rel string
				var gen int
				require.NoError(t, rows.Scan(&vs, &rel, &gen))
				if act[vs] == nil {
					act[vs] = make(map[string]int)
				}

				act[vs][rel] = gen
			}

			require.NoError(t, rows.Err())
			require.Equal(t, exp, act)
		}

		requireStorageState := func(t *testing.T, ctx context.Context, exp storageState) {
			rows, err := db.QueryContext(ctx, `
SELECT virtual_storage, relative_path, storage, generation
FROM storage_repositories
	`)
			require.NoError(t, err)
			defer rows.Close()

			act := make(storageState)
			for rows.Next() {
				var vs, rel, storage string
				var gen int
				require.NoError(t, rows.Scan(&vs, &rel, &storage, &gen))

				if act[vs] == nil {
					act[vs] = make(map[string]map[string]int)
				}
				if act[vs][rel] == nil {
					act[vs][rel] = make(map[string]int)
				}

				act[vs][rel][storage] = gen
			}

			require.NoError(t, rows.Err())
			require.Equal(t, exp, act)
		}

		return gs, func(t *testing.T, ctx context.Context, vss virtualStorageState, ss storageState) {
			t.Helper()
			requireVirtualStorageState(t, ctx, vss)
			requireStorageState(t, ctx, ss)
		}
	})
}
