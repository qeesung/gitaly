package housekeeping

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func testRepoAndPool(t *testing.T, desc string, testFunc func(t *testing.T, relativePath string)) {
	t.Helper()
	t.Run(desc, func(t *testing.T) {
		t.Run("normal repository", func(t *testing.T) {
			testFunc(t, gittest.NewRepositoryName(t))
		})

		t.Run("object pool", func(t *testing.T) {
			testFunc(t, gittest.NewObjectPoolName(t))
		})
	})
}

type objectsState struct {
	looseObjects            uint64
	packfiles               uint64
	cruftPacks              uint64
	keepPacks               uint64
	hasBitmap               bool
	hasMultiPackIndex       bool
	hasMultiPackIndexBitmap bool
}

func requireObjectsState(tb testing.TB, repo *localrepo.Repo, expectedState objectsState) {
	tb.Helper()
	ctx := testhelper.Context(tb)

	repoInfo, err := stats.RepositoryInfoForRepository(ctx, repo)
	require.NoError(tb, err)

	require.Equal(tb, expectedState, objectsState{
		looseObjects:            repoInfo.LooseObjects.Count,
		packfiles:               repoInfo.Packfiles.Count,
		cruftPacks:              repoInfo.Packfiles.CruftCount,
		keepPacks:               repoInfo.Packfiles.KeepCount,
		hasBitmap:               repoInfo.Packfiles.Bitmap.Exists,
		hasMultiPackIndex:       repoInfo.Packfiles.MultiPackIndex.Exists,
		hasMultiPackIndexBitmap: repoInfo.Packfiles.MultiPackIndexBitmap.Exists,
	})
}
