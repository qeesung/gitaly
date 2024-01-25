package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

func TestCalculateChecksum(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	type setupData struct {
		request          *gitalypb.CalculateChecksumRequest
		expectedResponse *gitalypb.CalculateChecksumResponse
		requireError     func(error)
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "unset repository",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.CalculateChecksumRequest{
						Repository: nil,
					},
					requireError: func(actual error) {
						testhelper.RequireGrpcError(t,
							structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
							actual,
						)
					},
				}
			},
		},
		{
			desc: "nonexistent storage",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.CalculateChecksumRequest{
						Repository: &gitalypb.Repository{
							StorageName:  "fake",
							RelativePath: gittest.NewRepositoryName(t),
						},
					},
					requireError: func(actual error) {
						testhelper.RequireGrpcError(t,
							testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
								"%w", storage.NewStorageNotFoundError("fake"),
							)),
							actual,
						)
					},
				}
			},
		},
		{
			desc: "broken repository",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				// Force an empty HEAD file such that the repository becomes broken.
				require.NoError(t, os.Truncate(filepath.Join(repoPath, "HEAD"), 0))

				return setupData{
					request: &gitalypb.CalculateChecksumRequest{
						Repository: repo,
					},
					requireError: func(err error) {
						require.Regexp(t, `^rpc error: code = DataLoss desc = not a git repository '.+'$`, err.Error())
						testhelper.RequireGrpcCode(t, err, codes.DataLoss)
					},
				}
			},
		},
		{
			desc: "empty repository",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.CalculateChecksumRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.CalculateChecksumResponse{
						Checksum: git.ZeroChecksum,
					},
				}
			},
		},
		{
			desc: "populated repository",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				for _, ref := range []string{"refs/heads/branch", "refs/tags/v1.0.0", "refs/notes/note"} {
					gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage(ref), gittest.WithReference(ref))
				}

				return setupData{
					request: &gitalypb.CalculateChecksumRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.CalculateChecksumResponse{
						Checksum: gittest.ObjectHashDependent(t, map[string]string{
							"sha1":   "7e3f9735e6f6c7de4f21b123cb6e34f428118a7e",
							"sha256": "daa22f3ab9dd539002a7931e42af041429f0346f",
						}),
					},
				}
			},
		},
		{
			desc: "unknown references are ignored",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				for _, ref := range []string{"refs/heads/branch", "refs/tags/v1.0.0", "refs/notes/note", "refs/unknown/namespace"} {
					gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage(ref), gittest.WithReference(ref))
				}

				return setupData{
					request: &gitalypb.CalculateChecksumRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.CalculateChecksumResponse{
						// Note that the checksum here is the same as in the preceding testcase.
						// This is because any references outside of well-known namespaces are
						// simply ignored. It's quite debatable whether this behaviour is
						// correct, but I'm not here to judge at the time of writing this test.
						Checksum: gittest.ObjectHashDependent(t, map[string]string{
							"sha1":   "7e3f9735e6f6c7de4f21b123cb6e34f428118a7e",
							"sha256": "daa22f3ab9dd539002a7931e42af041429f0346f",
						}),
					},
				}
			},
		},
		{
			desc: "invalid reference",
			setup: func(t *testing.T) setupData {
				testhelper.SkipWithReftable(t, "writing an invalid packed-refs has no effect for the refstable backend")

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

				// We write a known-broken reference into the packed-refs file. We expect that this
				// issue should be detected and reported to the caller. The existing behaviour is
				// somewhat weird though as it's impossible for the caller to distinguish an empty
				// repository from a corrupt repository given that both cases return the zero checksum.
				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "packed-refs"),
					[]byte(fmt.Sprintf("# pack-refs with: peeled fully-peeled sorted\n%s refs/heads/broken:reference\n", commitID)),
					perm.PrivateFile,
				))

				return setupData{
					request: &gitalypb.CalculateChecksumRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.CalculateChecksumResponse{
						Checksum: git.ZeroChecksum,
					},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			response, err := client.CalculateChecksum(ctx, setup.request)
			if setup.requireError != nil {
				setup.requireError(err)
				return
			}

			require.NoError(t, err)
			testhelper.ProtoEqual(t, setup.expectedResponse, response)
		})
	}
}
