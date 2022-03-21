package housekeeping

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
)

func requireObjectCount(t *testing.T, repoPath string, expectedObjects int64) {
	t.Helper()

	objects, err := stats.UnpackedObjects(repoPath)
	require.NoError(t, err)
	require.Equal(t, expectedObjects, objects)
}

func requirePackfileCount(t *testing.T, repoPath string, expectedPackfiles int) {
	t.Helper()

	packfiles, err := stats.PackfilesCount(repoPath)
	require.NoError(t, err)
	require.Equal(t, expectedPackfiles, packfiles)
}

func TestRepackObjects(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	t.Run("no server info is written", func(t *testing.T) {
		repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(), gittest.WithBranch("main"))

		requireObjectCount(t, repoPath, 1)
		requirePackfileCount(t, repoPath, 0)

		require.NoError(t, RepackObjects(ctx, repo, RepackObjectsConfig{}))

		requireObjectCount(t, repoPath, 0)
		requirePackfileCount(t, repoPath, 1)

		require.NoFileExists(t, filepath.Join(repoPath, "info", "refs"))
		require.NoFileExists(t, filepath.Join(repoPath, "objects", "info", "packs"))
	})
}
