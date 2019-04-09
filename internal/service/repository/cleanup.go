package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/helper"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var lockFiles = []string{"config.lock", "HEAD.lock"}

func (server) Cleanup(ctx context.Context, in *gitalypb.CleanupRequest) (*gitalypb.CleanupResponse, error) {
	if err := cleanupRepo(ctx, in.GetRepository()); err != nil {
		return nil, err
	}

	return &gitalypb.CleanupResponse{}, nil
}

func cleanupRepo(ctx context.Context, repo *gitalypb.Repository) error {
	repoPath, err := helper.GetRepoPath(repo)
	if err != nil {
		return err
	}

	threshold := time.Now().Add(-1 * time.Hour)
	if err := cleanRefsLocks(filepath.Join(repoPath, "refs"), threshold); err != nil {
		return status.Errorf(codes.Internal, "Cleanup: cleanRefsLocks: %v", err)
	}
	if err := cleanPackedRefsLock(repoPath, threshold); err != nil {
		return status.Errorf(codes.Internal, "Cleanup: cleanPackedRefsLock: %v", err)
	}

	if err := cleanStaleWorktrees(ctx, repo); err != nil {
		return status.Errorf(codes.Internal, "Cleanup: cleanStaleWorktrees: %v", err)
	}

	configLockThreshod := time.Now().Add(-15 * time.Minute)
	if err := cleanFileLocks(repoPath, configLockThreshod); err != nil {
		return status.Errorf(codes.Internal, "Cleanup: cleanupConfigLock: %v", err)
	}

	return nil
}

func cleanRefsLocks(rootPath string, threshold time.Time) error {
	return filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if os.IsNotExist(err) {
			// Race condition: somebody already deleted the file for us. Ignore this file.
			return nil
		}

		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if strings.HasSuffix(info.Name(), ".lock") && info.ModTime().Before(threshold) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}

		return nil
	})
}

func cleanPackedRefsLock(repoPath string, threshold time.Time) error {
	path := filepath.Join(repoPath, "packed-refs.lock")
	fileInfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if fileInfo.ModTime().Before(threshold) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}

func cleanStaleWorktrees(ctx context.Context, repo *gitalypb.Repository) error {
	// prune all disconnected work trees immediately
	err := worktreePruneCmd(ctx, repo, 0)
	if err != nil {
		return err
	}

	// prune all stale work trees older than 6 hours
	err = worktreePruneCmd(ctx, repo, 6)
	return err
}

func worktreePruneCmd(ctx context.Context, repo repository.GitRepo, hoursAgo int) error {
	pruneWorktreeArgs := []string{
		"worktree", "prune",
	}

	if hoursAgo > 0 {
		pruneWorktreeArgs = append(
			pruneWorktreeArgs,
			fmt.Sprintf("--expire=%d.hours.ago", hoursAgo),
		)
	}

	cmd, err := git.Command(ctx, repo, pruneWorktreeArgs...)
	if err != nil {
		return err
	}

	return cmd.Wait()
}

func cleanFileLocks(repoPath string, threshold time.Time) error {
	for _, fileName := range lockFiles {
		lockPath := filepath.Join(repoPath, fileName)

		fi, err := os.Stat(lockPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}

		if fi.ModTime().Before(threshold) {
			if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}

	return nil
}
