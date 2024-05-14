package backup

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestPartitionManager_Create(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T, partitionManager *PartitionManager)
	}{
		{
			desc: "success",
			setup: func(t *testing.T, partitionManager *PartitionManager) {
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)

			cfg := testcfg.Build(t)

			logger := testhelper.SharedLogger(t)
			cmdFactory := gittest.NewCommandFactory(t, cfg)
			catfileCache := catfile.NewCache(cfg)
			t.Cleanup(catfileCache.Stop)

			localRepoFactory := localrepo.NewFactory(logger, config.NewLocator(cfg), cmdFactory, catfileCache)

			partitionManager, err := storagemgr.NewPartitionManager(
				cfg.Storages,
				cmdFactory,
				localRepoFactory,
				logger,
				storagemgr.DatabaseOpenerFunc(storagemgr.OpenDatabase),
				helper.NewNullTickerFactory(),
				cfg.Prometheus,
				nil,
			)
			require.NoError(t, err)
			t.Cleanup(partitionManager.Close)

			backupRoot := testhelper.TempDir(t)

			sink, err := ResolveSink(ctx, backupRoot)
			require.NoError(t, err)

			var m Strategy = NewPartitionManager(sink, localRepoFactory)
			repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{SkipCreationViaService: true})

			txn, err := partitionManager.Begin(ctx, repo.GetStorageName(), repo.GetRelativePath(), storagemgr.TransactionOptions{
				ReadOnly: false,
			})
			require.NoError(t, err)

			gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

			require.NoError(t, txn.Commit(ctx))

			txn, err = partitionManager.Begin(ctx, repo.GetStorageName(), repo.GetRelativePath(), storagemgr.TransactionOptions{
				ReadOnly: false,
			})
			require.NoError(t, err)

			ctx = storagectx.ContextWithTransaction(ctx, txn)

			err = m.Create(ctx, &CreateRequest{
				BackupID:   "abc123",
				Repository: repo,
			})
			require.NoError(t, err)

			require.NoError(t, txn.Commit(ctx))

			manifestLoader := NewManifestLoader(sink)
			manifest, err := manifestLoader.ReadManifest(ctx, repo, "abc123")
			require.NoError(t, err)

			require.Equal(t, &Backup{
				ID:         "abc123",
				Repository: repo,
				WALPartition: WALPartition{
					ID:  "1",
					LSN: "1",
				},
			}, manifest)
		})
	}
}
