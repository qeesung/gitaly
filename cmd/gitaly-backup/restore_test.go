package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestRestoreSubcommand(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)

	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, nil, setup.RegisterAll)

	existingRepo, existRepoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed:         gittest.SeedGitLabTest,
		RelativePath: "existing_repo",
	})

	path := testhelper.TempDir(t)
	existingRepoBundlePath := filepath.Join(path, existingRepo.RelativePath+".bundle")
	gittest.Exec(t, cfg, "-C", existRepoPath, "bundle", "create", existingRepoBundlePath, "--all")

	repos := []*gitalypb.Repository{existingRepo}
	for i := 0; i < 2; i++ {
		repo := gittest.InitRepoDir(t, cfg.Storages[0].Path, fmt.Sprintf("repo-%d", i))
		repoBundlePath := filepath.Join(path, repo.RelativePath+".bundle")
		testhelper.CopyFile(t, existingRepoBundlePath, repoBundlePath)
		repos = append(repos, repo)
	}

	var stdin bytes.Buffer

	encoder := json.NewEncoder(&stdin)
	for _, repo := range repos {
		require.NoError(t, encoder.Encode(map[string]string{
			"address":         cfg.SocketPath,
			"token":           cfg.Auth.Token,
			"storage_name":    repo.StorageName,
			"relative_path":   repo.RelativePath,
			"gl_project_path": repo.GlProjectPath,
		}))
	}

	require.NoError(t, encoder.Encode(map[string]string{
		"address":       "invalid",
		"token":         "invalid",
		"relative_path": "invalid",
	}))

	cmd := restoreSubcommand{}

	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	cmd.Flags(fs)

	require.NoError(t, fs.Parse([]string{"-path", path}))
	require.EqualError(t,
		cmd.Run(ctx, &stdin, io.Discard),
		"restore: pipeline: 1 failures encountered:\n - invalid: manager: remove repository: could not dial source: invalid connection string: \"invalid\"\n")

	for _, repo := range repos {
		repoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(ctx, t, cfg, repo))
		bundlePath := filepath.Join(path, repo.RelativePath+".bundle")

		output := gittest.Exec(t, cfg, "-C", repoPath, "bundle", "verify", bundlePath)
		require.Contains(t, string(output), "The bundle records a complete history")
	}
}
