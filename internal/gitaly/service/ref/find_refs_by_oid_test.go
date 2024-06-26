package ref

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFindRefsByOID_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRefService(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	oid := gittest.WriteCommit(t, cfg, repoPath)

	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-1", string(oid))
	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-2", string(oid))
	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-3", string(oid))
	gittest.Exec(t, cfg, "-C", repoPath, "tag", "v100.0.0", string(oid))
	gittest.Exec(t, cfg, "-C", repoPath, "tag", "v100.1.0", string(oid))
	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-4", string(oid))
	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-5", string(oid))

	t.Run("tags come first", func(t *testing.T) {
		resp, err := client.FindRefsByOID(ctx, &gitalypb.FindRefsByOIDRequest{
			Repository: repo,
			Oid:        string(oid),
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"refs/heads/branch-1",
			"refs/heads/branch-2",
			"refs/heads/branch-3",
			"refs/heads/branch-4",
			"refs/heads/branch-5",
			"refs/tags/v100.0.0",
			"refs/tags/v100.1.0",
		}, resp.GetRefs())
	})

	t.Run("limit the response", func(t *testing.T) {
		resp, err := client.FindRefsByOID(ctx, &gitalypb.FindRefsByOIDRequest{
			Repository: repo,
			Oid:        string(oid),
			Limit:      3,
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"refs/heads/branch-1",
			"refs/heads/branch-2",
			"refs/heads/branch-3",
		}, resp.GetRefs())
	})

	t.Run("excludes other tags", func(t *testing.T) {
		anotherSha := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("hello! this is another commit"))
		gittest.Exec(t, cfg, "-C", repoPath, "tag", "v101.1.0", string(anotherSha))

		resp, err := client.FindRefsByOID(ctx, &gitalypb.FindRefsByOIDRequest{
			Repository: repo,
			Oid:        string(oid),
		})
		assert.NoError(t, err)
		assert.NotContains(t, resp.GetRefs(), "refs/tags/v101.1.0")
	})

	t.Run("oid prefix", func(t *testing.T) {
		resp, err := client.FindRefsByOID(ctx, &gitalypb.FindRefsByOIDRequest{
			Repository: repo,
			Oid:        string(oid)[:6],
			Limit:      1,
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"refs/heads/branch-1",
		}, resp.GetRefs())
	})

	t.Run("sort field", func(t *testing.T) {
		resp, err := client.FindRefsByOID(ctx, &gitalypb.FindRefsByOIDRequest{
			Repository:  repo,
			Oid:         string(oid),
			RefPatterns: []string{"refs/heads/"},
			Limit:       3,
			SortField:   "-refname",
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"refs/heads/branch-5",
			"refs/heads/branch-4",
			"refs/heads/branch-3",
		}, resp.GetRefs())
	})

	t.Run("ref patterns", func(t *testing.T) {
		resp, err := client.FindRefsByOID(ctx, &gitalypb.FindRefsByOIDRequest{
			Repository:  repo,
			Oid:         string(oid),
			RefPatterns: []string{"refs/tags/"},
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{
			"refs/tags/v100.0.0",
			"refs/tags/v100.1.0",
		}, resp.GetRefs())
	})
}

func TestFindRefsByOID_failure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRefService(t)

	equalError := func(t *testing.T, expected error) func(error) {
		return func(actual error) {
			testhelper.RequireGrpcError(t, expected, actual)
		}
	}

	testCases := []struct {
		desc  string
		setup func(t *testing.T) (*gitalypb.FindRefsByOIDRequest, func(error))
	}{
		{
			desc: "no ref exists for OID",
			setup: func(t *testing.T) (*gitalypb.FindRefsByOIDRequest, func(error)) {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				oid := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("no ref exists for OID"))

				return &gitalypb.FindRefsByOIDRequest{
					Repository: repo,
					Oid:        oid.String(),
				}, nil
			},
		},
		{
			desc: "repository is corrupted",
			setup: func(t *testing.T) (*gitalypb.FindRefsByOIDRequest, func(error)) {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				oid := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("no ref exists for OID"))
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/corrupted-repo-branch", oid.String())

				require.NoError(t, os.RemoveAll(filepath.Join(repoPath, "objects")))

				return &gitalypb.FindRefsByOIDRequest{
						Repository: repo,
						Oid:        oid.String(),
					}, func(actual error) {
						if testhelper.IsWALEnabled() {
							testhelper.RequireGrpcError(t,
								status.Error(codes.Internal, "begin transaction: get snapshot: new shared snapshot: create repository snapshots: validate git directory: invalid git directory"),
								actual,
							)

							return
						}

						testhelper.RequireStatusWithErrorMetadataRegexp(t,
							structerr.NewFailedPrecondition("%w: %q does not exist", storage.ErrRepositoryNotValid, "objects"),
							actual,
							map[string]string{
								"repository_path": ".+",
							},
						)
					}
			},
		},
		{
			desc: "repository is missing",
			setup: func(t *testing.T) (*gitalypb.FindRefsByOIDRequest, func(error)) {
				relativePath := gittest.NewRepositoryName(t)

				return &gitalypb.FindRefsByOIDRequest{
						Repository: &gitalypb.Repository{
							StorageName:  cfg.Storages[0].Name,
							RelativePath: relativePath,
						},
						Oid: strings.Repeat("a", gittest.DefaultObjectHash.EncodedLen()),
					}, equalError(t, testhelper.ToInterceptedMetadata(
						structerr.New("%w", storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, relativePath)),
					))
			},
		},
		{
			desc: "oid is not a commit",
			setup: func(t *testing.T) (*gitalypb.FindRefsByOIDRequest, func(error)) {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				oid := gittest.WriteBlob(t, cfg, repoPath, []byte("the blob"))

				return &gitalypb.FindRefsByOIDRequest{
					Repository: repo,
					Oid:        oid.String(),
				}, nil
			},
		},
		{
			desc: "oid prefix too short",
			setup: func(t *testing.T) (*gitalypb.FindRefsByOIDRequest, func(error)) {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				oid := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("oid prefix too short"))
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/short-oid", oid.String())

				return &gitalypb.FindRefsByOIDRequest{
					Repository: repo,
					Oid:        oid.String()[:2],
				}, equalError(t, structerr.NewInvalidArgument("for-each-ref pipeline command: exit status 129"))
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			request, requireError := tc.setup(t)

			response, err := client.FindRefsByOID(ctx, request)
			require.Empty(t, response.GetRefs())
			if requireError != nil {
				requireError(err)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestFindRefsByOID_validation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRefService(t)
	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	testCases := map[string]struct {
		req         *gitalypb.FindRefsByOIDRequest
		expectedErr error
	}{
		"no repository": {
			req: &gitalypb.FindRefsByOIDRequest{
				Repository: nil,
				Oid:        "abcdefg",
			},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		"no oid": {
			req: &gitalypb.FindRefsByOIDRequest{
				Repository: repo,
				Oid:        "",
			},
			expectedErr: status.Error(codes.InvalidArgument, "empty Oid"),
		},
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			_, err := client.FindRefsByOID(ctx, tc.req)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}
