package internalgitaly

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

func TestCleanRepos(t *testing.T) {
	cfg := testcfg.Build(t)
	storageName := cfg.Storages[0].Name
	storageRoot := cfg.Storages[0].Path

	_, deletedRepoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
		RelativePath: "a",
	})

	// to test a directory being deleted during a walk, we must delete a directory after
	// the file walk has started. To achieve that, we wrap the server to pass down a wrapped
	// stream that allows us to hook in to stream responses. We then delete 'b' when
	// the first repo 'a' is being streamed to the client.
	srv := NewServer([]config.Storage{{Name: storageName, Path: storageRoot}})

	client := setupInternalGitalyService(t, cfg, srv)
	ctx := testhelper.Context(t)

	_, err := client.CleanRepos(ctx, &gitalypb.CleanReposRequest{
		StorageName:   storageName,
		RelativePaths: []string{"a"},
	})
	require.NoError(t, err)
	repoNewDir := filepath.Join(storageRoot, "lost+found", time.Now().Format("2006-01-02"), "a")
	assert.DirExists(
		t,
		repoNewDir,
		"the repository should be moved to lost+found directory",
	)
	assert.True(t, storage.IsGitDirectory(repoNewDir))
	_, err = os.Stat(deletedRepoPath)
	assert.True(t, os.IsNotExist(err))
}
