package objectpool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestGetObjectPoolSuccess(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repoProto, _, _, client := setup(t, ctx)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	poolProto, pool, _ := createObjectPool(t, ctx, cfg, repoProto)
	relativePoolPath := poolProto.GetRepository().GetRelativePath()
	require.NoError(t, pool.Link(ctx, repo))

	resp, err := client.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{
		Repository: repoProto,
	})

	require.NoError(t, err)
	require.Equal(t, relativePoolPath, resp.GetObjectPool().GetRepository().GetRelativePath())
}

func TestGetObjectPoolNoFile(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, _, client := setup(t, ctx)

	resp, err := client.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{
		Repository: repo,
	})

	require.NoError(t, err)
	require.Nil(t, resp.GetObjectPool())
}

func TestGetObjectPoolBadFile(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, repoPath, _, client := setup(t, ctx)

	alternatesFilePath := filepath.Join(repoPath, "objects", "info", "alternates")
	require.NoError(t, os.MkdirAll(filepath.Dir(alternatesFilePath), perm.PrivateDir))
	require.NoError(t, os.WriteFile(alternatesFilePath, []byte("not-a-directory"), perm.PrivateWriteOnceFile))

	resp, err := client.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{
		Repository: repo,
	})

	if testhelper.IsWALEnabled() {
		require.Regexp(t, "begin transaction: get snapshot: new shared snapshot: create repository snapshots: create alternate snapshot: create directory snapshot: walk: create dir: mkdir .+/objects: file exists$", err.Error())
		return
	}

	require.NoError(t, err)
	require.Nil(t, resp.GetObjectPool())
}

func TestGetObjectPool_validate(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)
	_, _, _, _, client := setup(t, ctx)
	for _, tc := range []struct {
		desc        string
		req         *gitalypb.GetObjectPoolRequest
		expectedErr error
	}{
		{
			desc:        "repository not provided",
			req:         &gitalypb.GetObjectPoolRequest{Repository: nil},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.GetObjectPool(ctx, tc.req)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}
