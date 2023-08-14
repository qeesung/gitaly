//go:build !gitaly_test_sha256

package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRestoreRepository(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	type setupData struct {
		cfg              config.Cfg
		client           gitalypb.RepositoryServiceClient
		repo             *gitalypb.Repository
		repoPath         string
		backupID         string
		expectedChecksum *git.Checksum
	}

	for _, tc := range []struct {
		desc        string
		setup       func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData
		expectedErr error
	}{
		{
			desc: "restore backup ID",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryServiceWithoutRepo(t,
					testserver.WithBackupSink(backupSink),
					testserver.WithBackupLocator(backupLocator),
				)

				_, templateRepoPath := gittest.CreateRepository(t, ctx, cfg)
				oid := gittest.WriteCommit(t, cfg, templateRepoPath, gittest.WithBranch(git.DefaultBranch))
				gittest.WriteCommit(t, cfg, templateRepoPath, gittest.WithBranch("feature"), gittest.WithParents(oid))
				checksum := gittest.ChecksumRepo(t, cfg, templateRepoPath)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				step := backupLocator.BeginFull(ctx, repo, "abc123")

				w, err := backupSink.GetWriter(ctx, step.BundlePath)
				require.NoError(t, err)
				bundle := gittest.BundleRepo(t, cfg, templateRepoPath, "-")
				_, err = w.Write(bundle)
				require.NoError(t, err)
				require.NoError(t, w.Close())

				w, err = backupSink.GetWriter(ctx, step.RefPath)
				require.NoError(t, err)
				refs := gittest.Exec(t, cfg, "-C", templateRepoPath, "show-ref", "--head")
				_, err = w.Write(refs)
				require.NoError(t, err)
				require.NoError(t, w.Close())

				require.NoError(t, backupLocator.Commit(ctx, step))

				return setupData{
					cfg:              cfg,
					client:           client,
					repo:             repo,
					repoPath:         repoPath,
					backupID:         "abc123",
					expectedChecksum: checksum,
				}
			},
		},
		{
			desc: "restore latest",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryServiceWithoutRepo(t,
					testserver.WithBackupSink(backupSink),
					testserver.WithBackupLocator(backupLocator),
				)

				_, templateRepoPath := gittest.CreateRepository(t, ctx, cfg)
				oid := gittest.WriteCommit(t, cfg, templateRepoPath, gittest.WithBranch(git.DefaultBranch))
				gittest.WriteCommit(t, cfg, templateRepoPath, gittest.WithBranch("feature"), gittest.WithParents(oid))
				checksum := gittest.ChecksumRepo(t, cfg, templateRepoPath)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				step := backupLocator.BeginFull(ctx, repo, "abc123")

				w, err := backupSink.GetWriter(ctx, step.BundlePath)
				require.NoError(t, err)
				bundle := gittest.BundleRepo(t, cfg, templateRepoPath, "-")
				_, err = w.Write(bundle)
				require.NoError(t, err)
				require.NoError(t, w.Close())

				w, err = backupSink.GetWriter(ctx, step.RefPath)
				require.NoError(t, err)
				refs := gittest.Exec(t, cfg, "-C", templateRepoPath, "show-ref", "--head")
				_, err = w.Write(refs)
				require.NoError(t, err)
				require.NoError(t, w.Close())

				require.NoError(t, backupLocator.Commit(ctx, step))

				return setupData{
					cfg:              cfg,
					client:           client,
					repo:             repo,
					repoPath:         repoPath,
					backupID:         "",
					expectedChecksum: checksum,
				}
			},
		},
		{
			desc: "restore latest missing",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryServiceWithoutRepo(t,
					testserver.WithBackupSink(backupSink),
					testserver.WithBackupLocator(backupLocator),
				)
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					RelativePath: "@test/restore/latest/missing.git",
				})

				return setupData{
					cfg:      cfg,
					client:   client,
					repo:     repo,
					repoPath: repoPath,
					backupID: "",
				}
			},
			expectedErr: structerr.NewFailedPrecondition("restore repository: manager: repository skipped: read refs: doesn't exist").WithDetail(
				&gitalypb.RestoreRepositoryResponse_SkippedError{},
			),
		},
		{
			desc: "missing repository",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryServiceWithoutRepo(t,
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
			expectedErr: structerr.NewInvalidArgument(testhelper.GitalyOrPraefect(
				"restore repository: repository: repository not set",
				"repository not set",
			)),
		},
		{
			desc: "missing backup sink",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryServiceWithoutRepo(t,
					testserver.WithBackupLocator(backupLocator),
				)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:      cfg,
					client:   client,
					repo:     repo,
					repoPath: repoPath,
					backupID: "abc123",
				}
			},
			expectedErr: structerr.NewFailedPrecondition("restore repository: server-side backups are not configured"),
		},
		{
			desc: "missing backup locator",
			setup: func(t *testing.T, ctx context.Context, backupSink backup.Sink, backupLocator backup.Locator) setupData {
				cfg, client := setupRepositoryServiceWithoutRepo(t,
					testserver.WithBackupSink(backupSink),
				)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:      cfg,
					client:   client,
					repo:     repo,
					repoPath: repoPath,
					backupID: "abc123",
				}
			},
			expectedErr: structerr.NewFailedPrecondition("restore repository: server-side backups are not configured"),
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			backupRoot := testhelper.TempDir(t)
			backupSink, err := backup.ResolveSink(ctx, backupRoot)
			require.NoError(t, err)

			backupLocator, err := backup.ResolveLocator("pointer", backupSink)
			require.NoError(t, err)

			data := tc.setup(t, ctx, backupSink, backupLocator)

			response, err := data.client.RestoreRepository(ctx, &gitalypb.RestoreRepositoryRequest{
				Repository:       data.repo,
				VanityRepository: data.repo,
				BackupId:         data.backupID,
			})
			if tc.expectedErr != nil {
				testhelper.RequireGrpcError(t, tc.expectedErr, err)
				return
			}

			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.RestoreRepositoryResponse{}, response)

			checksum := gittest.ChecksumRepo(t, data.cfg, data.repoPath)
			require.Equal(t, data.expectedChecksum.String(), checksum.String())
		})
	}
}
