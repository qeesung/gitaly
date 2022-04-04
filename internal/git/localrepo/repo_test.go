package localrepo

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

func TestRepo(t *testing.T) {
	cfg := testcfg.Build(t)

	gittest.TestRepository(t, cfg, func(ctx context.Context, t testing.TB, seeded bool) (git.Repository, string) {
		t.Helper()

		var (
			pbRepo   *gitalypb.Repository
			repoPath string
		)

		if seeded {
			pbRepo, repoPath = gittest.CloneRepo(t, cfg, cfg.Storages[0])
		} else {
			pbRepo, repoPath = gittest.InitRepo(t, cfg, cfg.Storages[0])
		}

		gitCmdFactory := gittest.NewCommandFactory(t, cfg)
		catfileCache := catfile.NewCache(cfg)
		t.Cleanup(catfileCache.Stop)
		return New(config.NewLocator(cfg), gitCmdFactory, catfileCache, pbRepo), repoPath
	})
}

func TestSize(t *testing.T) {
	cfg := testcfg.Build(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	testCases := []struct {
		desc         string
		setup        func(repoPath string, t *testing.T)
		expectedSize int64
	}{
		{
			desc:         "empty repository",
			expectedSize: 0,
		},
		{
			desc: "one committed file",
			setup: func(repoPath string, t *testing.T) {
				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "file"),
					bytes.Repeat([]byte("a"), 1000),
					0o644,
				))

				cmd := gittest.NewCommand(t, cfg, "-C", repoPath, "add", "file")
				require.NoError(t, cmd.Run())
				cmd = gittest.NewCommand(t, cfg, "-C", repoPath, "commit", "-m", "initial")
				require.NoError(t, cmd.Run())
			},
			expectedSize: 202,
		},
		{
			desc: "one large loose blob",
			setup: func(repoPath string, t *testing.T) {
				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "file"),
					bytes.Repeat([]byte("a"), 1000),
					0o644,
				))

				cmd := gittest.NewCommand(t, cfg, "-C", repoPath, "checkout", "-b", "branch-a")
				require.NoError(t, cmd.Run())
				cmd = gittest.NewCommand(t, cfg, "-C", repoPath, "add", "file")
				require.NoError(t, cmd.Run())
				cmd = gittest.NewCommand(t, cfg, "-C", repoPath, "commit", "-m", "initial")
				require.NoError(t, cmd.Run())
				cmd = gittest.NewCommand(t, cfg, "-C", repoPath, "update-ref", "-d", "refs/heads/branch-a")
				require.NoError(t, cmd.Run())
			},
			expectedSize: 0,
		},
		{
			desc: "modification to blob without repack",
			setup: func(repoPath string, t *testing.T) {
				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "file"),
					bytes.Repeat([]byte("a"), 1000),
					0o644,
				))

				cmd := gittest.NewCommand(t, cfg, "-C", repoPath, "add", "file")
				require.NoError(t, cmd.Run())
				cmd = gittest.NewCommand(t, cfg, "-C", repoPath, "commit", "-m", "initial")
				require.NoError(t, cmd.Run())

				f, err := os.OpenFile(
					filepath.Join(repoPath, "file"),
					os.O_APPEND|os.O_WRONLY,
					0o644)
				require.NoError(t, err)
				defer f.Close()
				_, err = f.WriteString("a")
				assert.NoError(t, err)

				cmd = gittest.NewCommand(t, cfg, "-C", repoPath, "commit", "-am", "modification")
				require.NoError(t, cmd.Run())
			},
			expectedSize: 437,
		},
		{
			desc: "modification to blob after repack",
			setup: func(repoPath string, t *testing.T) {
				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "file"),
					bytes.Repeat([]byte("a"), 1000),
					0o644,
				))

				cmd := gittest.NewCommand(t, cfg, "-C", repoPath, "add", "file")
				require.NoError(t, cmd.Run())
				cmd = gittest.NewCommand(t, cfg, "-C", repoPath, "commit", "-m", "initial")
				require.NoError(t, cmd.Run())

				f, err := os.OpenFile(
					filepath.Join(repoPath, "file"),
					os.O_APPEND|os.O_WRONLY,
					0o644)
				require.NoError(t, err)
				defer f.Close()
				_, err = f.WriteString("a")
				assert.NoError(t, err)

				cmd = gittest.NewCommand(t, cfg, "-C", repoPath, "commit", "-am", "modification")
				require.NoError(t, cmd.Run())

				cmd = gittest.NewCommand(t, cfg, "-C", repoPath, "repack", "-a", "-d")
				require.NoError(t, cmd.Run())
			},
			expectedSize: 391,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			pbRepo, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
				WithWorktree: true,
			})
			repo := New(config.NewLocator(cfg), gitCmdFactory, catfileCache, pbRepo)
			if tc.setup != nil {
				tc.setup(repoPath, t)
			}

			ctx := testhelper.Context(t)
			size, err := repo.Size(ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedSize, size)
		})
	}
}
