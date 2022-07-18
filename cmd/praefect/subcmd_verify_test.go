//go:build !gitaly_test_sha256

package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service/info"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
)

func TestVerifySubcommand(t *testing.T) {
	t.Parallel()

	// state contains the asserted state as virtual storage -> relative path -> storage -> verified/not verified.
	type state map[string]map[string]map[string]bool

	startingState := state{
		"virtual-storage-1": {
			"relative-path-1": {
				"unverified": false,
				"verified-1": true,
				"verified-2": true,
			},
			"relative-path-2": {
				"unverified": false,
				"verified-1": true,
				"verified-2": true,
			},
		},
		"virtual-storage-2": {
			"relative-path-1": {
				"unverified": false,
				"verified-1": true,
				"verified-2": true,
			},
		},
	}

	for _, tc := range []struct {
		desc           string
		args           []string
		replicasMarked int
		error          error
		expectedState  state
	}{
		{
			desc:          "no selector given",
			error:         errors.New("(repository id), (virtual storage) or (virtual storage, storage) required"),
			expectedState: startingState,
		},
		{
			desc:          "virtual storage passed with repository id",
			args:          []string{"-repository-id=1", "-virtual-storage=virtual-storage"},
			error:         errors.New("virtual storage and storage can't be provided with a repository ID"),
			expectedState: startingState,
		},
		{
			desc:          "storage passed with repository id",
			args:          []string{"-repository-id=1", "-storage=storage"},
			error:         errors.New("virtual storage and storage can't be provided with a repository ID"),
			expectedState: startingState,
		},
		{
			desc:          "storage passed without virtual storage",
			args:          []string{"-storage=storage"},
			error:         errors.New("virtual storage must be passed with storage"),
			expectedState: startingState,
		},
		{
			desc:          "no repository matched",
			args:          []string{"-repository-id=1000"},
			expectedState: startingState,
		},
		{
			desc:           "scheduled by repository id",
			args:           []string{"-repository-id=1"},
			replicasMarked: 2,
			expectedState: state{
				"virtual-storage-1": {
					"relative-path-1": {
						"unverified": false,
						"verified-1": false,
						"verified-2": false,
					},
					"relative-path-2": {
						"unverified": false,
						"verified-1": true,
						"verified-2": true,
					},
				},
				"virtual-storage-2": {
					"relative-path-1": {
						"unverified": false,
						"verified-1": true,
						"verified-2": true,
					},
				},
			},
		},
		{
			desc:           "scheduled by virtual storage",
			args:           []string{"-virtual-storage=virtual-storage-1"},
			replicasMarked: 4,
			expectedState: state{
				"virtual-storage-1": {
					"relative-path-1": {
						"unverified": false,
						"verified-1": false,
						"verified-2": false,
					},
					"relative-path-2": {
						"unverified": false,
						"verified-1": false,
						"verified-2": false,
					},
				},
				"virtual-storage-2": {
					"relative-path-1": {
						"unverified": false,
						"verified-1": true,
						"verified-2": true,
					},
				},
			},
		},
		{
			desc:           "scheduled by storage",
			args:           []string{"-virtual-storage=virtual-storage-1", "-storage=verified-1"},
			replicasMarked: 2,
			expectedState: state{
				"virtual-storage-1": {
					"relative-path-1": {
						"unverified": false,
						"verified-1": false,
						"verified-2": true,
					},
					"relative-path-2": {
						"unverified": false,
						"verified-1": false,
						"verified-2": true,
					},
				},
				"virtual-storage-2": {
					"relative-path-1": {
						"unverified": false,
						"verified-1": true,
						"verified-2": true,
					},
				},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db := testdb.New(t)

			rs := datastore.NewPostgresRepositoryStore(db, nil)

			ln, clean := listenAndServe(t, []svcRegistrar{
				registerPraefectInfoServer(info.NewServer(config.Config{}, rs, nil, nil, nil)),
			})
			defer clean()

			ctx := testhelper.Context(t)

			require.NoError(t,
				rs.CreateRepository(ctx, 1, "virtual-storage-1", "relative-path-1", "replica-path-1", "unverified", []string{"verified-1", "verified-2"}, nil, false, false),
			)
			require.NoError(t,
				rs.CreateRepository(ctx, 2, "virtual-storage-1", "relative-path-2", "replica-path-2", "unverified", []string{"verified-1", "verified-2"}, nil, false, false),
			)
			require.NoError(t,
				rs.CreateRepository(ctx, 3, "virtual-storage-2", "relative-path-1", "replica-path-3", "unverified", []string{"verified-1", "verified-2"}, nil, false, false),
			)

			_, err := db.ExecContext(ctx, `
				UPDATE storage_repositories
				SET verified_at = now()
				FROM repositories
				WHERE repositories.repository_id = storage_repositories.repository_id
				AND   storage != 'unverified'
			`)
			require.NoError(t, err)

			stdout := &bytes.Buffer{}
			cmd := newVerifySubcommand(stdout)

			fs := cmd.FlagSet()
			require.NoError(t, fs.Parse(tc.args))
			err = cmd.Exec(fs, config.Config{SocketPath: ln.Addr().String()})
			testhelper.RequireGrpcError(t, tc.error, err)
			if tc.error != nil {
				return
			}

			require.Equal(t, fmt.Sprintf("%d replicas marked unverified\n", tc.replicasMarked), stdout.String())

			actualState := state{}
			rows, err := db.QueryContext(ctx, `
				SELECT
					repositories.virtual_storage,
					repositories.relative_path,
					storage,
					verified_at IS NOT NULL as verified
				FROM repositories
				JOIN storage_repositories USING (repository_id)
			`)
			require.NoError(t, err)
			defer rows.Close()

			for rows.Next() {
				var virtualStorage, relativePath, storage string
				var verified bool
				require.NoError(t, rows.Scan(&virtualStorage, &relativePath, &storage, &verified))

				if actualState[virtualStorage] == nil {
					actualState[virtualStorage] = map[string]map[string]bool{}
				}

				if actualState[virtualStorage][relativePath] == nil {
					actualState[virtualStorage][relativePath] = map[string]bool{}
				}

				actualState[virtualStorage][relativePath][storage] = verified
			}

			require.NoError(t, rows.Err())
			require.Equal(t, tc.expectedState, actualState)
		})
	}
}
