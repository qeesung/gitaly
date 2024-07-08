package objectpool

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestDisconnectGitAlternates(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repoProto, repoPath, _, client := setup(t, ctx)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
	gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad")

	// We create the object pool, link the original repository to it and then repack the pool
	// member. As the linking step should've pulled all objects into the pool, the repack should
	// get rid of the now-duplicate objects in the repository in favor of the pooled ones.
	_, pool, _ := createObjectPool(t, ctx, cfg, repoProto)
	require.NoError(t, pool.Link(ctx, repo))
	gittest.Exec(t, cfg, "-C", repoPath, "gc")

	// Corrupt the repository to check that the commit we have created can no longer be read.
	// This is done to ensure that the object really only exists in the pool repository now.
	altPath, err := repo.InfoAlternatesPath(ctx)
	require.NoError(t, err)
	require.NoError(t, os.Remove(altPath))
	gittest.RequireObjectNotExists(t, cfg, repoPath, commitID)

	// Recreate the alternates link and assert that we can now read the commit again.
	require.NoError(t, pool.Link(ctx, repo))
	require.FileExists(t, altPath, "objects/info/alternates should be back")
	gittest.RequireObjectExists(t, cfg, repoPath, commitID)

	// At this point we know that the repository has access to the commit, but only if
	// objects/info/alternates is in place.
	_, err = client.DisconnectGitAlternates(ctx, &gitalypb.DisconnectGitAlternatesRequest{Repository: repoProto})
	require.NoError(t, err)

	// Check that the object can still be found, even though objects/info/alternates is gone.
	// This is the purpose of DisconnectGitAlternates.
	require.NoFileExists(t, altPath)
	gittest.RequireObjectExists(t, cfg, repoPath, commitID)
}

func TestDisconnectGitAlternatesNoAlternates(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repoProto, repoPath, _, client := setup(t, ctx)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	altPath, err := repo.InfoAlternatesPath(ctx)
	require.NoError(t, err, "find info/alternates")
	require.NoFileExists(t, altPath)

	_, err = client.DisconnectGitAlternates(ctx, &gitalypb.DisconnectGitAlternatesRequest{Repository: repoProto})
	require.NoError(t, err, "call DisconnectGitAlternates on repository without alternates")

	gittest.Exec(t, cfg, "-C", repoPath, "fsck")
}

func TestDisconnectGitAlternatesUnexpectedAlternates(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, _, _, _, client := setup(t, ctx)

	testCases := []struct {
		desc       string
		altContent string
	}{
		{desc: "multiple alternates", altContent: "/foo/bar\n/qux/baz\n"},
		{desc: "directory not found", altContent: "/does/not/exist/\n"},
		{desc: "not a directory", altContent: "../HEAD\n"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, _ := gittest.CreateRepository(t, ctx, cfg)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			altPath, err := repo.InfoAlternatesPath(ctx)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(altPath, []byte(tc.altContent), perm.PrivateWriteOnceFile))

			_, err = client.DisconnectGitAlternates(ctx, &gitalypb.DisconnectGitAlternatesRequest{Repository: repoProto})
			require.Error(t, err)

			contentAfterRPC := testhelper.MustReadFile(t, altPath)
			require.Equal(t, tc.altContent, string(contentAfterRPC), "objects/info/alternates content should not have changed")
		})
	}
}

func TestDisconnectGitAlternates_validate(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)
	_, _, _, _, client := setup(t, ctx)
	for _, tc := range []struct {
		desc        string
		req         *gitalypb.DisconnectGitAlternatesRequest
		expectedErr error
	}{
		{
			desc:        "repository not provided",
			req:         &gitalypb.DisconnectGitAlternatesRequest{Repository: nil},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.DisconnectGitAlternates(ctx, tc.req)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}
