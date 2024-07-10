package repository

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func TestCreateBundle_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	// Create a work tree with a HEAD pointing to a commit that is missing. CreateBundle should
	// clean this up before creating the bundle.
	worktreeCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("worktree"), gittest.WithBranch("branch"))
	require.NoError(t, os.MkdirAll(filepath.Join(repoPath, housekeeping.GitlabWorktreePrefix), perm.PrivateDir))
	gittest.Exec(t, cfg, "-C", repoPath, "worktree", "add", filepath.Join(housekeeping.GitlabWorktreePrefix, "worktree1"), worktreeCommit.String())
	require.NoError(t, os.Chtimes(filepath.Join(repoPath, housekeeping.GitlabWorktreePrefix, "worktree1"), time.Now().Add(-7*time.Hour), time.Now().Add(-7*time.Hour)))
	gittest.Exec(t, cfg, "-C", repoPath, "branch", "-D", "branch")
	require.NoError(t, os.Remove(filepath.Join(repoPath, "objects", worktreeCommit.String()[0:2], worktreeCommit.String()[2:])))

	stream, err := client.CreateBundle(ctx, &gitalypb.CreateBundleRequest{Repository: repo})
	require.NoError(t, err)

	reader := streamio.NewReader(func() ([]byte, error) {
		response, err := stream.Recv()
		return response.GetData(), err
	})

	bundleFile, err := os.Create(filepath.Join(testhelper.TempDir(t), "bundle"))
	require.NoError(t, err)
	defer testhelper.MustClose(t, bundleFile)

	_, err = io.Copy(bundleFile, reader)
	require.NoError(t, err)

	// Extra sanity check: running verify should fail on bad bundles.
	output := gittest.Exec(t, cfg, "-C", repoPath, "bundle", "verify", bundleFile.Name())
	require.Contains(t, string(output), "The bundle records a complete history")
}

func TestCreateBundle_validations(t *testing.T) {
	t.Parallel()

	_, client := setupRepositoryService(t)

	for _, tc := range []struct {
		desc        string
		request     *gitalypb.CreateBundleRequest
		expectedErr error
	}{
		{
			desc:        "empty repository",
			request:     &gitalypb.CreateBundleRequest{},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			stream, err := client.CreateBundle(ctx, tc.request)
			require.NoError(t, err)

			_, err = stream.Recv()
			require.NotEqual(t, io.EOF, err)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}
