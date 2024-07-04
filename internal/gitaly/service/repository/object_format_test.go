package repository

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestObjectFormat(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	equalError := func(tb testing.TB, expected error) func(error) {
		return func(actual error) {
			tb.Helper()
			testhelper.RequireGrpcError(tb, expected, actual)
		}
	}

	type setupData struct {
		request          *gitalypb.ObjectFormatRequest
		requireError     func(error)
		expectedResponse *gitalypb.ObjectFormatResponse
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "unset repository",
			setup: func(t *testing.T) setupData {
				return setupData{
					request:      &gitalypb.ObjectFormatRequest{},
					requireError: equalError(t, structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet)),
				}
			},
		},
		{
			desc: "missing storage name",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.ObjectFormatRequest{
						Repository: &gitalypb.Repository{
							RelativePath: "path",
						},
					},
					requireError: equalError(t, structerr.NewInvalidArgument("%w", storage.ErrStorageNotSet)),
				}
			},
		},
		{
			desc: "missing relative path",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.ObjectFormatRequest{
						Repository: &gitalypb.Repository{
							StorageName: cfg.Storages[0].Name,
						},
					},
					requireError: equalError(t, structerr.NewInvalidArgument("%w", storage.ErrRepositoryPathNotSet)),
				}
			},
		},
		{
			desc: "nonexistent repository",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.ObjectFormatRequest{
						Repository: &gitalypb.Repository{
							StorageName:  cfg.Storages[0].Name,
							RelativePath: "nonexistent.git",
						},
					},
					requireError: equalError(t, testhelper.ToInterceptedMetadata(
						structerr.New("%w", storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, "nonexistent.git")),
					)),
				}
			},
		},
		{
			desc: "SHA1",
			setup: func(t *testing.T) setupData {
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					ObjectFormat: "sha1",
				})

				return setupData{
					request: &gitalypb.ObjectFormatRequest{
						Repository: repoProto,
					},
					expectedResponse: &gitalypb.ObjectFormatResponse{
						Format: gitalypb.ObjectFormat_OBJECT_FORMAT_SHA1,
					},
				}
			},
		},
		{
			desc: "SHA256",
			setup: func(t *testing.T) setupData {
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					ObjectFormat: "sha256",
				})

				return setupData{
					request: &gitalypb.ObjectFormatRequest{
						Repository: repoProto,
					},
					expectedResponse: &gitalypb.ObjectFormatResponse{
						Format: gitalypb.ObjectFormat_OBJECT_FORMAT_SHA256,
					},
				}
			},
		},
		{
			desc: "invalid object format",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

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
				), perm.SharedFile))

				return setupData{
					request: &gitalypb.ObjectFormatRequest{
						Repository: repoProto,
					},
					requireError: func(actual error) {
						testhelper.RequireStatusWithErrorMetadataRegexp(t,
							structerr.NewInternal("detecting object hash: reading object format: exit status 128"),
							actual,
							map[string]string{
								"stderr": "^error: invalid value for 'extensions.objectformat': 'blake2b'\nfatal: bad config line 5 in file .+/config\n$",
							},
						)
					},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setupData := tc.setup(t)
			response, err := client.ObjectFormat(ctx, setupData.request)
			if setupData.requireError != nil {
				setupData.requireError(err)
				return
			}

			require.NoError(t, err)
			testhelper.ProtoEqual(t, setupData.expectedResponse, response)
		})
	}
}
