package objectpool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

func TestReduplicate(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg, repoProto, repoPath, _, client := setup(ctx, t)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	pool := initObjectPool(t, cfg, cfg.Storages[0])
	require.NoError(t, pool.Create(ctx, repo))
	require.NoError(t, pool.Link(ctx, repo))

	gittest.Exec(t, cfg, "-C", repoPath, "gc")

	// git-gc(1) invokes git-repack(1), which by defaults generates these files. Manually remove
	// them so that we can assert further down that repository reduplication doesn't regenerate
	// those paths.
	require.NoError(t, os.Remove(filepath.Join(repoPath, "info", "refs")))
	require.NoError(t, os.Remove(filepath.Join(repoPath, "objects", "info", "packs")))

	existingObjectID := "55bc176024cfa3baaceb71db584c7e5df900ea65"

	// Corrupt the repository to check if the object can't be found
	altPath, err := repo.InfoAlternatesPath()
	require.NoError(t, err, "find info/alternates")
	require.NoError(t, os.RemoveAll(altPath))

	cmd, err := gitCmdFactory.New(ctx, repo,
		git.SubCmd{Name: "cat-file", Flags: []git.Option{git.Flag{Name: "-e"}}, Args: []string{existingObjectID}})
	require.NoError(t, err)
	require.Error(t, cmd.Wait())

	// Reduplicate and check if the objects appear again
	require.NoError(t, pool.Link(ctx, repo))
	_, err = client.ReduplicateRepository(ctx, &gitalypb.ReduplicateRepositoryRequest{Repository: repoProto})
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(altPath))
	gittest.Exec(t, cfg, "-C", repoPath, "cat-file", "-e", existingObjectID)

	require.NoFileExists(t, filepath.Join(repoPath, "info", "refs"))
	require.NoFileExists(t, filepath.Join(repoPath, "objects", "info", "packs"))
}
