package remoterepo_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/remoterepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestRepository(t *testing.T) {
	t.Parallel()

	cfg := setupGitalyServer(t)

	pool := client.NewPool()
	t.Cleanup(func() { testhelper.MustClose(t, pool) })

	gittest.TestRepository(t, cfg, func(tb testing.TB, ctx context.Context) (git.Repository, string) {
		tb.Helper()

		ctx, err := storage.InjectGitalyServers(ctx, "default", cfg.SocketPath, cfg.Auth.Token)
		require.NoError(tb, err)

		repoProto, repoPath := gittest.CreateRepository(tb, ctx, cfg)

		repo, err := remoterepo.New(metadata.OutgoingToIncoming(ctx), repoProto, pool)
		require.NoError(tb, err)
		return repo, repoPath
	})
}

func TestRepository_ObjectHash(t *testing.T) {
	t.Parallel()

	cfg := setupGitalyServer(t)

	ctx := testhelper.Context(t)
	ctx, err := storage.InjectGitalyServers(ctx, "default", cfg.SocketPath, cfg.Auth.Token)
	require.NoError(t, err)
	ctx = metadata.OutgoingToIncoming(ctx)

	pool := client.NewPool()
	defer testhelper.MustClose(t, pool)

	type setupData struct {
		repo           *remoterepo.Repo
		requireError   func(testing.TB, error)
		expectedFormat string
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "SHA1",
			setup: func(t *testing.T) setupData {
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					ObjectFormat: "sha1",
				})

				repo, err := remoterepo.New(ctx, repoProto, pool)
				require.NoError(t, err)

				return setupData{
					repo:           repo,
					expectedFormat: "sha1",
				}
			},
		},
		{
			desc: "SHA256",
			setup: func(t *testing.T) setupData {
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					ObjectFormat: "sha256",
				})

				repo, err := remoterepo.New(ctx, repoProto, pool)
				require.NoError(t, err)

				return setupData{
					repo:           repo,
					expectedFormat: "sha256",
				}
			},
		},
		{
			desc: "invalid object format",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					ObjectFormat: "sha256",
				})

				// We write the config file manually so that we can use an
				// exact-match for the error down below.
				//
				// Remove the config file first as files are read-only with transactions.
				configPath := filepath.Join(repoPath, "config")
				require.NoError(t, os.Remove(configPath))
				require.NoError(t, os.WriteFile(configPath, []byte(
					strings.Join([]string{
						"[core]",
						"repositoryformatversion = 1",
						"bare = true",
						"[extensions]",
						"objectFormat = blake2b",
					}, "\n"),
				), perm.PrivateWriteOnceFile))

				repo, err := remoterepo.New(ctx, repoProto, pool)
				require.NoError(t, err)

				return setupData{
					repo: repo,
					requireError: func(tb testing.TB, actual error) {
						testhelper.RequireStatusWithErrorMetadataRegexp(tb, structerr.NewInternal("detecting object hash: reading object format: exit status 128"), actual, map[string]string{
							"stderr": "error: invalid value for 'extensions.objectformat': 'blake2b'\nfatal: bad config line 5 in file .+/config\n",
						})
					},
				}
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			setupData := tc.setup(t)

			objectHash, err := setupData.repo.ObjectHash(ctx)
			if setupData.requireError != nil {
				setupData.requireError(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, setupData.expectedFormat, objectHash.Format)
		})
	}
}
