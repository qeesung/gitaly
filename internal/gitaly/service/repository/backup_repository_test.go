package repository

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestServerBackupRepository(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	type setupData struct {
		cfg         config.Cfg
		client      gitalypb.RepositoryServiceClient
		repo        *gitalypb.Repository
		backupID    string
		incremental bool
	}

	for _, tc := range []struct {
		desc        string
		setup       func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData
		expectedErr error
	}{
		{
			desc: "success",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryService(t,
					testserver.WithBackupSink(backupSink),
					testserver.WithBackupLocator(backupLocator),
				)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

				return setupData{
					cfg:      cfg,
					client:   client,
					repo:     repo,
					backupID: "abc123",
				}
			},
		},
		{
			desc: "success - incremental",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryService(t,
					testserver.WithBackupSink(backupSink),
					testserver.WithBackupLocator(backupLocator),
				)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

				return setupData{
					cfg:         cfg,
					client:      client,
					repo:        repo,
					backupID:    "abc123",
					incremental: true,
				}
			},
		},
		{
			desc: "missing backup ID",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryService(t,
					testserver.WithBackupSink(backupSink),
					testserver.WithBackupLocator(backupLocator),
				)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

				return setupData{
					cfg:      cfg,
					client:   client,
					repo:     repo,
					backupID: "",
				}
			},
			expectedErr: structerr.NewInvalidArgument("empty BackupId"),
		},
		{
			desc: "missing repository",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryService(t,
					testserver.WithBackupSink(backupSink),
					testserver.WithBackupLocator(backupLocator),
				)

				return setupData{
					cfg:      cfg,
					client:   client,
					repo:     nil,
					backupID: "abc123",
				}
			},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "repository with no branches",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryService(t,
					testserver.WithBackupSink(backupSink),
					testserver.WithBackupLocator(backupLocator),
				)

				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:      cfg,
					client:   client,
					repo:     repo,
					backupID: "abc123",
				}
			},
		},
		{
			desc: "missing backup sink",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryService(t,
					testserver.WithBackupLocator(backupLocator),
				)

				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:      cfg,
					client:   client,
					repo:     repo,
					backupID: "abc123",
				}
			},
			expectedErr: structerr.NewFailedPrecondition("backup repository: server-side backups are not configured"),
		},
		{
			desc: "missing backup locator",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryService(t,
					testserver.WithBackupSink(backupSink),
				)

				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:      cfg,
					client:   client,
					repo:     repo,
					backupID: "abc123",
				}
			},
			expectedErr: structerr.NewFailedPrecondition("backup repository: server-side backups are not configured"),
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			backupRoot := testhelper.TempDir(t)
			backupSink, err := backup.ResolveSink(ctx, backupRoot)
			require.NoError(t, err)

			backupLocator, err := backup.ResolveLocator("manifest", backupSink)
			require.NoError(t, err)

			vanityRepo := &gitalypb.Repository{
				StorageName:  "does-not-exist",
				RelativePath: "@test/repo.git",
			}

			data := tc.setup(t, ctx, backupSink, backupLocator)

			response, err := data.client.BackupRepository(ctx, &gitalypb.BackupRepositoryRequest{
				Repository:       data.repo,
				VanityRepository: vanityRepo,
				BackupId:         data.backupID,
				Incremental:      data.incremental,
			})
			if tc.expectedErr != nil {
				testhelper.RequireGrpcError(t, tc.expectedErr, err)
				return
			}

			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.BackupRepositoryResponse{}, response)

			manifestLoader := backup.NewManifestLoader(backupSink)
			_, err = manifestLoader.ReadManifest(ctx, vanityRepo, data.backupID)
			require.NoError(t, err)
		})
	}
}

func BenchmarkBackupRepository(b *testing.B) {
	ctx := testhelper.Context(b)

	backupRoot := testhelper.TempDir(b)
	backupSink, err := backup.ResolveSink(ctx, backupRoot)
	require.NoError(b, err)

	backupLocator, err := backup.ResolveLocator("manifest", backupSink)
	require.NoError(b, err)

	cfg, client := setupRepositoryService(b,
		testserver.WithBackupSink(backupSink),
		testserver.WithBackupLocator(backupLocator),
	)

	repo, _ := gittest.CreateRepository(b, ctx, cfg, gittest.CreateRepositoryConfig{
		Seed: "benchmark.git",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.BackupRepository(ctx, &gitalypb.BackupRepositoryRequest{
			Repository:       repo,
			VanityRepository: repo,
			BackupId:         strconv.Itoa(i),
		})
		require.NoError(b, err)
	}
}
