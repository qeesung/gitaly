package gittest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

// setup sets up a test configuration and repository. Ideally we'd use our central test helpers to
// do this, but because of an import cycle we can't.
func setup(tb testing.TB) (config.Cfg, *gitalypb.Repository, string) {
	tb.Helper()

	rootDir := testhelper.TempDir(tb)

	ctx := testhelper.Context(tb)
	var cfg config.Cfg

	cfg.SocketPath = "it is a stub to bypass Validate method"

	cfg.Storages = []config.Storage{
		{
			Name: "default",
			Path: filepath.Join(rootDir, "storage.d"),
		},
	}
	require.NoError(tb, os.Mkdir(cfg.Storages[0].Path, perm.PrivateDir))

	cfg.GitlabShell.Dir = filepath.Join(rootDir, "shell.d")
	require.NoError(tb, os.Mkdir(cfg.GitlabShell.Dir, perm.PrivateDir))

	cfg.BinDir = filepath.Join(rootDir, "bin.d")
	require.NoError(tb, os.Mkdir(cfg.BinDir, perm.PrivateDir))

	cfg.RuntimeDir = filepath.Join(rootDir, "run.d")
	require.NoError(tb, os.Mkdir(cfg.RuntimeDir, perm.PrivateDir))
	require.NoError(tb, os.Mkdir(cfg.InternalSocketDir(), perm.PrivateDir))

	require.NoError(tb, cfg.Validate())

	repo, repoPath := CreateRepository(tb, ctx, cfg, CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	return cfg, repo, repoPath
}
