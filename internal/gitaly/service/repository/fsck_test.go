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

func TestFsck(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	equalResponse := func(tb testing.TB, expected *gitalypb.FsckResponse) func(*gitalypb.FsckResponse) {
		return func(actual *gitalypb.FsckResponse) { testhelper.ProtoEqual(tb, expected, actual) }
	}

	type setupData struct {
		repo            *gitalypb.Repository
		requireError    func(error)
		requireResponse func(*gitalypb.FsckResponse)
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "request is missing repository",
			setup: func(t *testing.T) setupData {
				return setupData{
					repo: nil,
					requireError: func(actual error) {
						testhelper.RequireGrpcError(t, structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet), actual)
					},
				}
			},
		},
		{
			desc: "empty repository",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					repo:            repo,
					requireResponse: equalResponse(t, &gitalypb.FsckResponse{}),
				}
			},
		},
		{
			desc: "repository with commit",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

				return setupData{
					repo:            repo,
					requireResponse: equalResponse(t, &gitalypb.FsckResponse{}),
				}
			},
		},
		{
			desc: "invalid object directory",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				// This makes the repo severely broken so that `git` does not
				// identify it as a proper repository anymore.
				require.NoError(t, os.RemoveAll(filepath.Join(repoPath, "objects")))
				require.NoError(t, os.WriteFile(filepath.Join(repoPath, "objects"), nil, perm.PrivateWriteOnceFile))

				setupData := setupData{
					repo: repo,
					requireResponse: func(actual *gitalypb.FsckResponse) {
						require.Regexp(t, "^fatal: not a git repository: '.+'\n$", string(actual.Error))
					},
				}

				if testhelper.IsWALEnabled() {
					setupData.requireResponse = nil
					setupData.requireError = func(actual error) {
						require.Regexp(t, "begin transaction: .+/objects/info/alternates: not a directory$", actual.Error())
					}
				}

				return setupData
			},
		},
		{
			desc: "missing objects",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				// We write a commit and repack it into a packfile...
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad")
				// ... but then subsequently delete all the packfiles. This should
				// lead to git-fsck(1) complaining about missing objects.
				require.NoError(t, os.RemoveAll(filepath.Join(repoPath, "objects", "pack")))

				expectedErr := strings.Join([]string{
					"error: refs/heads/main: invalid sha1 pointer " + commitID.String(),
					"error: HEAD: invalid sha1 pointer " + commitID.String(),
					"notice: No default references",
				}, "\n") + "\n"

				return setupData{
					repo: repo,
					requireResponse: equalResponse(t, &gitalypb.FsckResponse{
						Error: []byte(expectedErr),
					}),
				}
			},
		},
		{
			desc: "dangling blob",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				// A dangling blob should not cause the consistency check to fail as
				// it is totally expected that repositories accumulate unreachable
				// objects.
				gittest.WriteBlob(t, cfg, repoPath, []byte("content"))

				return setupData{
					repo:            repo,
					requireResponse: equalResponse(t, &gitalypb.FsckResponse{}),
				}
			},
		},
		{
			desc: "invalid tree object",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				// Create the default branch so that git-fsck(1) doesn't complain
				// about it missing.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

				// Write a tree object that has the same path twice. We use this as
				// an example to verify that git-fsck(1) indeed also verifies
				// objects as expected just because writing trees with two entries
				// is so easy.
				//
				// Furthermore, the tree is also dangling on purpose to verify that
				// we don't complain about it dangling.
				treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "duplicate", Mode: "100644", Content: "foo"},
					{Path: "duplicate", Mode: "100644", Content: "bar"},
				})

				expectedErr := "error in tree " + treeID.String() + ": duplicateEntries: contains duplicate file entries\n"

				return setupData{
					repo: repo,
					requireResponse: equalResponse(t, &gitalypb.FsckResponse{
						Error: []byte(expectedErr),
					}),
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setupData := tc.setup(t)

			response, err := client.Fsck(ctx, &gitalypb.FsckRequest{
				Repository: setupData.repo,
			})
			if setupData.requireError != nil {
				setupData.requireError(err)
				return
			}

			require.NoError(t, err)
			setupData.requireResponse(response)
		})
	}
}
