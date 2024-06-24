package quarantine_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/quarantine"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestQuarantine_localrepo(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	locator := config.NewLocator(cfg)

	quarantine, _, err := quarantine.New(ctx, repoProto, testhelper.NewLogger(t), locator)
	require.NoError(t, err)

	quarantined := localrepo.NewTestRepo(t, cfg, quarantine.QuarantinedRepo())

	t.Run("reading unquarantined objects succeeds", func(t *testing.T) {
		_, err := quarantined.ReadObject(ctx, "HEAD^{commit}")
		require.NoError(t, err)
	})

	t.Run("writes are not visible in parent repo", func(t *testing.T) {
		blobID, err := quarantined.WriteBlob(ctx, strings.NewReader("contents"), localrepo.WriteBlobConfig{})
		require.NoError(t, err)

		_, err = repo.ReadObject(ctx, blobID)
		require.Error(t, err)

		blobContents, err := quarantined.ReadObject(ctx, blobID)
		require.NoError(t, err)
		require.Equal(t, "contents", string(blobContents))
	})

	t.Run("writes are visible after migrating", func(t *testing.T) {
		blobID, err := quarantined.WriteBlob(ctx, strings.NewReader("contents"), localrepo.WriteBlobConfig{})
		require.NoError(t, err)

		_, err = repo.ReadObject(ctx, blobID)
		require.Error(t, err)

		require.NoError(t, quarantine.Migrate())

		blobContents, err := repo.ReadObject(ctx, blobID)
		require.NoError(t, err)
		require.Equal(t, "contents", string(blobContents))
	})
}
