package stats

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

// RepositoryProfile contains information about a git repository.
type RepositoryProfile struct {
	hasBitmap       bool
	packfiles       int64
	unpackedObjects int64
	looseObjects    int64
}

// HasBitmap returns whether or not the repository contains an object bitmap.
func (r *RepositoryProfile) HasBitmap() bool {
	return r.hasBitmap
}

// Packfiles returns the number of packfiles a repository has.
func (r *RepositoryProfile) Packfiles() int64 {
	return r.packfiles
}

// UnpackedObjects returns the number of loose objects that have a timestamp later than the newest
// packfile.
func (r *RepositoryProfile) UnpackedObjects() int64 {
	return r.unpackedObjects
}

// LooseObjects returns the number of loose objects that are not in a packfile.
func (r *RepositoryProfile) LooseObjects() int64 {
	return r.looseObjects
}

// GetProfile returns a RepositoryProfile given a context and a repository.GitRepo
func GetProfile(ctx context.Context, repo repository.GitRepo) (*RepositoryProfile, error) {
	repoPath, err := helper.GetRepoPath(repo)
	if err != nil {
		return nil, err
	}

	cmd, err := git.SafeCmd(ctx, repo, nil, git.SubCmd{Name: "count-objects", Flags: []git.Option{git.Flag{Name: "--verbose"}}})
	if err != nil {
		return nil, err
	}

	objectStats, err := readObjectInfoStatistic(cmd)
	if err != nil {
		return nil, err
	}

	count, ok := objectStats["count"].(int64)
	if !ok {
		return nil, errors.New("could not get object count")
	}

	packs, ok := objectStats["packs"].(int64)
	if !ok {
		return nil, errors.New("could not get packfile count")
	}

	unpackedObjects, err := getUnpackedObjects(repoPath)
	if err != nil {
		return nil, err
	}

	hasBitmap, err := hasBitmap(repoPath)
	if err != nil {
		return nil, err
	}

	return &RepositoryProfile{
		hasBitmap:       hasBitmap,
		packfiles:       packs,
		unpackedObjects: unpackedObjects,
		looseObjects:    count,
	}, nil
}

func hasBitmap(repoPath string) (bool, error) {
	bitmap, err := filepath.Glob(filepath.Join(repoPath, "objects", "pack", "*.bitmap"))
	if err != nil {
		return false, err
	}

	return len(bitmap) > 0, nil
}

func getUnpackedObjects(repoPath string) (int64, error) {
	objectDir := filepath.Join(repoPath, "objects")

	packFiles, err := filepath.Glob(filepath.Join(objectDir, "pack", "*.pack"))
	if err != nil {
		return 0, err
	}

	var newestPackfileModtime time.Time

	for _, packFilePath := range packFiles {
		stat, err := os.Stat(packFilePath)
		if err != nil {
			return 0, err
		}
		if stat.ModTime().After(newestPackfileModtime) {
			newestPackfileModtime = stat.ModTime()
		}
	}

	var unpackedObjects int64
	if err = filepath.Walk(objectDir, func(path string, info os.FileInfo, err error) error {
		if objectDir == path {
			return nil
		}

		if info.IsDir() {
			if err := skipNonObjectDir(objectDir, path); err != nil {
				return err
			}
		}

		if !info.IsDir() && info.ModTime().After(newestPackfileModtime) {
			unpackedObjects++
		}

		return nil
	}); err != nil {
		return 0, err
	}

	return unpackedObjects, nil
}

func skipNonObjectDir(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}

	if len(rel) != 2 {
		return filepath.SkipDir
	}

	if _, err := strconv.ParseUint(rel, 16, 8); err != nil {
		return filepath.SkipDir
	}

	return nil
}
