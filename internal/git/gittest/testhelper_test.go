package gittest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
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
	cfg.Git.IgnoreGitconfig = true

	cfg.Storages = []config.Storage{
		{
			Name: "default",
			Path: filepath.Join(rootDir, "storage.d"),
		},
	}
	require.NoError(tb, os.Mkdir(cfg.Storages[0].Path, 0o755))

	_, currentFile, _, ok := runtime.Caller(0)
	require.True(tb, ok, "could not get caller info")
	cfg.Ruby.Dir = filepath.Join(filepath.Dir(currentFile), "../../../ruby")

	cfg.GitlabShell.Dir = filepath.Join(rootDir, "shell.d")
	require.NoError(tb, os.Mkdir(cfg.GitlabShell.Dir, 0o755))

	cfg.BinDir = filepath.Join(rootDir, "bin.d")
	require.NoError(tb, os.Mkdir(cfg.BinDir, 0o755))

	cfg.RuntimeDir = filepath.Join(rootDir, "run.d")
	require.NoError(tb, os.Mkdir(cfg.RuntimeDir, 0o700))
	require.NoError(tb, os.Mkdir(cfg.InternalSocketDir(), 0o700))

	require.NoError(tb, cfg.Validate())

	repo, repoPath := CreateRepository(ctx, tb, cfg, CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	return cfg, repo, repoPath
}
