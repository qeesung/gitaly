package gitalybackup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRestoreSubcommand(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)

	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)

	// This is an example of a "dangling" repository (one that was created after a backup was taken) that should be
	// removed after the backup is restored.
	existingRepo, existRepoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		RelativePath: "existing_repo",
	})
	gittest.WriteCommit(t, cfg, existRepoPath, gittest.WithBranch(git.DefaultBranch))

	// The backupDir contains the artifacts that would've been created as part of a backup.
	backupDir := testhelper.TempDir(t)
	testhelper.WriteFiles(t, backupDir, map[string]any{
		filepath.Join(existingRepo.RelativePath + ".bundle"): gittest.Exec(t, cfg, "-C", existRepoPath, "bundle", "create", "-", "--all"),
		filepath.Join(existingRepo.RelativePath + ".refs"):   gittest.Exec(t, cfg, "-C", existRepoPath, "show-ref", "--head"),
	})

	// These repos are the ones being restored, and should exist after the restore.
	var repos []*gitalypb.Repository
	for i := 0; i < 2; i++ {
		repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			RelativePath: fmt.Sprintf("repo-%d", i),
			Storage:      cfg.Storages[0],
		})

		testhelper.WriteFiles(t, backupDir, map[string]any{
			filepath.Join("manifests", repo.StorageName, repo.RelativePath, "+latest.toml"): fmt.Sprintf(`
object_format = '%[1]s'
head_reference = '%[3]s'

[[steps]]
bundle_path = '%[2]s.bundle'
ref_path = '%[2]s.refs'
custom_hooks_path = '%[2]s/custom_hooks.tar'
`, gittest.DefaultObjectHash.Format, existingRepo.RelativePath, git.DefaultRef.String()),
		})

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
		backupDir,
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

	// Ensure the repos were restored correctly.
	for _, repo := range repos {
		repoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, repo))
		bundlePath := filepath.Join(backupDir, existingRepo.RelativePath+".bundle")

		output := gittest.Exec(t, cfg, "-C", repoPath, "bundle", "verify", bundlePath)
		require.Contains(t, string(output), "The bundle records a complete history")
	}
}

func TestRestoreSubcommand_serverSide(t *testing.T) {
	ctx := testhelper.Context(t)

	backupDir := testhelper.TempDir(t)
	backupSink, err := backup.ResolveSink(ctx, backupDir)
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

	testhelper.WriteFiles(t, backupDir, map[string]any{
		filepath.Join(existingRepo.RelativePath + ".bundle"): gittest.Exec(t, cfg, "-C", existRepoPath, "bundle", "create", "-", "--all"),
		filepath.Join(existingRepo.RelativePath + ".refs"):   gittest.Exec(t, cfg, "-C", existRepoPath, "show-ref", "--head"),
	})

	var repos []*gitalypb.Repository
	for i := 0; i < 2; i++ {
		repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			RelativePath: fmt.Sprintf("repo-%d", i),
			Storage:      cfg.Storages[0],
		})

		testhelper.WriteFiles(t, backupDir, map[string]any{
			filepath.Join("manifests", repo.StorageName, repo.RelativePath, "+latest.toml"): fmt.Sprintf(`
object_format = '%[1]s'
head_reference = '%[3]s'

[[steps]]
bundle_path = '%[2]s.bundle'
ref_path = '%[2]s.refs'
custom_hooks_path = '%[2]s/custom_hooks.tar'
`, gittest.DefaultObjectHash.Format, existingRepo.RelativePath, git.DefaultRef.String()),
		})

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
		bundlePath := filepath.Join(backupDir, existingRepo.RelativePath+".bundle")

		output := gittest.Exec(t, cfg, "-C", repoPath, "bundle", "verify", bundlePath)
		require.Contains(t, string(output), "The bundle records a complete history")
	}
}
