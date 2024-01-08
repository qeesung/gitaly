package gitalybackup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRestoreSubcommand(t *testing.T) {
	gittest.SkipWithSHA256(t)

	testhelper.SkipWithWAL(t, `
RemoveAll is removing the entire content of the storage. This would also remove the database's and
the transaction manager's disk state. The RPC needs to be updated to shut down all partitions and
the database and only then perform the removal.

Issue: https://gitlab.com/gitlab-org/gitaly/-/issues/5269`)

	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)

	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)

	existingRepo, existRepoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		RelativePath: "existing_repo",
	})
	gittest.WriteCommit(t, cfg, existRepoPath, gittest.WithBranch(git.DefaultBranch))

	path := testhelper.TempDir(t)
	existingRepoBundlePath := filepath.Join(path, existingRepo.RelativePath+".bundle")
	existingRepoRefPath := filepath.Join(path, existingRepo.RelativePath+".refs")

	gittest.Exec(t, cfg, "-C", existRepoPath, "bundle", "create", existingRepoBundlePath, "--all")
	require.NoError(t, os.WriteFile(existingRepoRefPath, gittest.Exec(t, cfg, "-C", existRepoPath, "show-ref"), perm.SharedFile))

	var repos []*gitalypb.Repository
	for i := 0; i < 2; i++ {
		repo := gittest.InitRepoDir(t, cfg.Storages[0].Path, fmt.Sprintf("repo-%d", i))
		repoBundlePath := filepath.Join(path, repo.RelativePath+".bundle")
		repoRefPath := filepath.Join(path, repo.RelativePath+".refs")
		testhelper.CopyFile(t, existingRepoBundlePath, repoBundlePath)
		testhelper.CopyFile(t, existingRepoRefPath, repoRefPath)
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

	ctx = testhelper.MergeIncomingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

	args := []string{
		progname,
		"restore",
		"--path",
		path,
		"--parallel",
		strconv.Itoa(runtime.NumCPU()),
		"--parallel-storage",
		"2",
		"--layout",
		"pointer",
		"--remove-all-repositories",
		existingRepo.StorageName,
	}
	cmd := NewApp()
	cmd.Reader = &stdin
	cmd.Writer = io.Discard

	require.DirExists(t, existRepoPath)

	require.NoError(t, cmd.RunContext(ctx, args))

	require.NoDirExists(t, existRepoPath)

	for _, repo := range repos {
		repoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, repo))
		bundlePath := filepath.Join(path, repo.RelativePath+".bundle")

		output := gittest.Exec(t, cfg, "-C", repoPath, "bundle", "verify", bundlePath)
		require.Contains(t, string(output), "The bundle records a complete history")
	}
}

func TestRestoreSubcommand_serverSide(t *testing.T) {
	gittest.SkipWithSHA256(t)

	testhelper.SkipWithWAL(t, `
RemoveAll is removing the entire content of the storage. This would also remove the database's and
the transaction manager's disk state. The RPC needs to be updated to shut down all partitions and
the database and only then perform the removal.

Issue: https://gitlab.com/gitlab-org/gitaly/-/issues/5269`)

	ctx := testhelper.Context(t)

	path := testhelper.TempDir(t)
	backupSink, err := backup.ResolveSink(ctx, path)
	require.NoError(t, err)

	backupLocator, err := backup.ResolveLocator("pointer", backupSink)
	require.NoError(t, err)

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)

	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll,
		testserver.WithBackupSink(backupSink),
		testserver.WithBackupLocator(backupLocator),
	)

	existingRepo, existRepoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		RelativePath: "existing_repo",
	})
	gittest.WriteCommit(t, cfg, existRepoPath, gittest.WithBranch(git.DefaultBranch))

	existingRepoBundlePath := filepath.Join(path, existingRepo.RelativePath+".bundle")
	existingRepoRefPath := filepath.Join(path, existingRepo.RelativePath+".refs")

	gittest.Exec(t, cfg, "-C", existRepoPath, "bundle", "create", existingRepoBundlePath, "--all")
	require.NoError(t, os.WriteFile(existingRepoRefPath, gittest.Exec(t, cfg, "-C", existRepoPath, "show-ref"), perm.SharedFile))

	var repos []*gitalypb.Repository
	for i := 0; i < 2; i++ {
		repo := gittest.InitRepoDir(t, cfg.Storages[0].Path, fmt.Sprintf("repo-%d", i))
		repoBundlePath := filepath.Join(path, repo.RelativePath+".bundle")
		repoRefPath := filepath.Join(path, repo.RelativePath+".refs")
		testhelper.CopyFile(t, existingRepoBundlePath, repoBundlePath)
		testhelper.CopyFile(t, existingRepoRefPath, repoRefPath)
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

	ctx = testhelper.MergeIncomingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

	args := []string{
		progname,
		"restore",
		"--parallel",
		strconv.Itoa(runtime.NumCPU()),
		"--parallel-storage",
		"2",
		"--layout",
		"pointer",
		"--remove-all-repositories",
		existingRepo.StorageName,
		"--server-side",
		"true",
	}
	cmd := NewApp()
	cmd.Reader = &stdin
	cmd.Writer = io.Discard

	require.DirExists(t, existRepoPath)

	require.NoError(t, cmd.RunContext(ctx, args))

	require.NoDirExists(t, existRepoPath)

	for _, repo := range repos {
		repoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, repo))
		bundlePath := filepath.Join(path, repo.RelativePath+".bundle")

		output := gittest.Exec(t, cfg, "-C", repoPath, "bundle", "verify", bundlePath)
		require.Contains(t, string(output), "The bundle records a complete history")
	}
}
