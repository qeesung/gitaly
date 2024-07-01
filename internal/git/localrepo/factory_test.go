package localrepo

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestFactory(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t, testcfg.WithStorages("storage-1", "storage-2"))

	locator := config.NewLocator(cfg)
	cmdFactory := gittest.NewCommandFactory(t, cfg)

	catfileCache := catfile.NewCache(cfg)
	defer catfileCache.Stop()

	factory := NewFactory(testhelper.NewLogger(t), locator, cmdFactory, catfileCache)

	t.Run("Build", func(t *testing.T) {
		t.Run("parameters are passthrough", func(t *testing.T) {
			repo := factory.Build(&gitalypb.Repository{
				StorageName:  "non-existent",
				RelativePath: "relative-path-1",
			})

			require.Equal(t, "non-existent", repo.GetStorageName())
			require.Equal(t, "relative-path-1", repo.GetRelativePath())
		})
	})

	t.Run("ScopeByStorage/Build", func(t *testing.T) {
		t.Run("non-existent storage fails", func(t *testing.T) {
			scopedFactory, err := factory.ScopeByStorage(ctx, "non-existent")
			require.Equal(t, fmt.Errorf("get storage by name: %w", storage.NewStorageNotFoundError("non-existent")), err)
			require.Empty(t, scopedFactory)
		})

		t.Run("successfully builds repositories", func(t *testing.T) {
			scopedFactory1, err := factory.ScopeByStorage(ctx, "storage-1")
			require.NoError(t, err)

			repo1 := scopedFactory1.Build("relative-path-1")
			require.Equal(t, "storage-1", repo1.GetStorageName())
			require.Equal(t, "relative-path-1", repo1.GetRelativePath())

			scopedFactory2, err := factory.ScopeByStorage(ctx, "storage-2")
			require.NoError(t, err)

			repo2 := scopedFactory2.Build("relative-path-2")
			require.Equal(t, "storage-2", repo2.GetStorageName())
			require.Equal(t, "relative-path-2", repo2.GetRelativePath())
		})
	})
}
