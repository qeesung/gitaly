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
func setup(t testing.TB) (config.Cfg, *gitalypb.Repository, string) {
	t.Helper()

	rootDir := testhelper.TempDir(t)

	ctx := testhelper.Context(t)
	var cfg config.Cfg

	cfg.SocketPath = "it is a stub to bypass Validate method"
	cfg.Git.IgnoreGitconfig = true

	cfg.Storages = []config.Storage{
		{
			Name: "default",
			Path: filepath.Join(rootDir, "storage.d"),
		},
	}
	require.NoError(t, os.Mkdir(cfg.Storages[0].Path, 0o755))

	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "could not get caller info")
	cfg.Ruby.Dir = filepath.Join(filepath.Dir(currentFile), "../../../ruby")

	cfg.GitlabShell.Dir = filepath.Join(rootDir, "shell.d")
	require.NoError(t, os.Mkdir(cfg.GitlabShell.Dir, 0o755))

	cfg.BinDir = filepath.Join(rootDir, "bin.d")
	require.NoError(t, os.Mkdir(cfg.BinDir, 0o755))

	cfg.RuntimeDir = filepath.Join(rootDir, "run.d")
	require.NoError(t, os.Mkdir(cfg.RuntimeDir, 0o700))
	require.NoError(t, os.Mkdir(cfg.InternalSocketDir(), 0o700))

	require.NoError(t, cfg.Validate())

	repo, repoPath := CreateRepository(ctx, t, cfg, CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	return cfg, repo, repoPath
}
