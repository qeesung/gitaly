package repository

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bundleuri"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestServer_GenerateBundleURI(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	type setupData struct {
		cfg    config.Cfg
		client gitalypb.RepositoryServiceClient
		repo   *gitalypb.Repository
	}

	for _, tc := range []struct {
		desc        string
		setup       func(t *testing.T, ctx context.Context, tempDir string) setupData
		expectedErr error
	}{
		{
			desc: "no bundle-URI sink",
			setup: func(t *testing.T, ctx context.Context, tempDir string) setupData {
				cfg, client := setupRepositoryService(t,
					testserver.WithBundleURISink(nil),
				)

				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					repo:   repo,
				}
			},
			expectedErr: structerr.NewFailedPrecondition("no bundle-URI sink available"),
		},
		{
			desc: "no valid repo",
			setup: func(t *testing.T, ctx context.Context, tempDir string) setupData {
				sink, err := bundleuri.NewSink(ctx, "file://"+tempDir)
				require.NoError(t, err)

				cfg, client := setupRepositoryService(t,
					testserver.WithBundleURISink(sink),
				)

				return setupData{
					cfg:    cfg,
					client: client,
				}
			},
			expectedErr: structerr.NewInvalidArgument("repository not set"),
		},
		{
			desc: "empty repo",
			setup: func(t *testing.T, ctx context.Context, tempDir string) setupData {
				sink, err := bundleuri.NewSink(ctx, "file://"+tempDir)
				require.NoError(t, err)

				cfg, client := setupRepositoryService(t,
					testserver.WithBundleURISink(sink),
				)

				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					repo:   repo,
				}
			},
			expectedErr: structerr.NewFailedPrecondition("generate bundle: ref %q does not exist: create bundle: refusing to create empty bundle", "refs/heads/main"),
		},
		{
			desc: "success",
			setup: func(t *testing.T, ctx context.Context, tempDir string) setupData {
				sink, err := bundleuri.NewSink(ctx, "file://"+tempDir)
				require.NoError(t, err)

				cfg, client := setupRepositoryService(t,
					testserver.WithBundleURISink(sink),
				)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

				return setupData{
					cfg:    cfg,
					client: client,
					repo:   repo,
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			tempDir := testhelper.TempDir(t)
			data := tc.setup(t, ctx, tempDir)

			_, err := data.client.GenerateBundleURI(ctx, &gitalypb.GenerateBundleURIRequest{
				Repository: data.repo,
			})
			if tc.expectedErr == nil {
				require.NoError(t, err)

				var bundleFound bool
				require.NoError(t, filepath.WalkDir(tempDir, func(path string, d fs.DirEntry, err error) error {
					require.NoError(t, err)

					if filepath.Ext(path) == ".bundle" && !d.IsDir() {
						bundleFound = true
					}

					return nil
				}))
				require.Truef(t, bundleFound, "no .bundle found in %s", tempDir)
			} else {
				testhelper.RequireGrpcError(t, tc.expectedErr, err)
			}
		})
	}
}
