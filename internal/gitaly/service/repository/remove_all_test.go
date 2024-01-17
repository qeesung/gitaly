package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRemoveAll(t *testing.T) {
	testhelper.SkipWithWAL(t, `
RemoveAll is removing the entire content of the storage. This would also remove the database's and
the transaction manager's disk state. The RPC needs to be updated to shut down all partitions and
the database and only then perform the removal.

Issue: https://gitlab.com/gitlab-org/gitaly/-/issues/5269`)

	t.Parallel()

	cfg, client := setupRepositoryService(t)
	ctx := testhelper.Context(t)

	_, repo1Path := gittest.CreateRepository(t, ctx, cfg)
	_, repo2Path := gittest.CreateRepository(t, ctx, cfg)

	require.DirExists(t, repo1Path)
	require.DirExists(t, repo1Path)

	//nolint:staticcheck
	_, err := client.RemoveAll(ctx, &gitalypb.RemoveAllRequest{
		StorageName: cfg.Storages[0].Name,
	})
	require.NoError(t, err)

	require.NoDirExists(t, repo1Path)
	require.NoDirExists(t, repo2Path)
}
