package objectpool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestGetObjectPoolSuccess(t *testing.T) {
	poolCtx := testhelper.Context(t)
	cfg, repoProto, _, _, client := setup(poolCtx, t)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	pool := initObjectPool(t, cfg, cfg.Storages[0])
	relativePoolPath := pool.GetRelativePath()

	require.NoError(t, pool.Create(poolCtx, repo))
	require.NoError(t, pool.Link(poolCtx, repo))

	ctx := testhelper.Context(t)
	defer func() {
		require.NoError(t, pool.Remove(ctx))
	}()

	resp, err := client.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{
		Repository: repoProto,
	})

	require.NoError(t, err)
	require.Equal(t, relativePoolPath, resp.GetObjectPool().GetRepository().GetRelativePath())
}

func TestGetObjectPoolNoFile(t *testing.T) {
	ctx := testhelper.Context(t)
	_, repoo, _, _, client := setup(ctx, t)

	resp, err := client.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{
		Repository: repoo,
	})

	require.NoError(t, err)
	require.Nil(t, resp.GetObjectPool())
}

func TestGetObjectPoolBadFile(t *testing.T) {
	ctx := testhelper.Context(t)
	_, repo, repoPath, _, client := setup(ctx, t)

	alternatesFilePath := filepath.Join(repoPath, "objects", "info", "alternates")
	require.NoError(t, os.MkdirAll(filepath.Dir(alternatesFilePath), 0o755))
	require.NoError(t, os.WriteFile(alternatesFilePath, []byte("not-a-directory"), 0o644))

	resp, err := client.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{
		Repository: repo,
	})

	require.NoError(t, err)
	require.Nil(t, resp.GetObjectPool())
}
