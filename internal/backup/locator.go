package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
)

// LegacyLocator locates backup paths for historic backups. This is the
// structure that gitlab used before incremental backups were introduced.
//
// Existing backup files are expected to be overwritten by the latest backup
// files.
//
// Structure:
//
//	<repo relative path>.bundle
//	<repo relative path>.refs
//	<repo relative path>/custom_hooks.tar
type LegacyLocator struct{}

// BeginFull returns the static paths for a legacy repository backup
func (l LegacyLocator) BeginFull(ctx context.Context, repo storage.Repository, backupID string) *Backup {
	return l.newFull(repo)
}

// BeginIncremental is not supported for legacy backups
func (l LegacyLocator) BeginIncremental(ctx context.Context, repo storage.Repository, backupID string) (*Backup, error) {
	return nil, errors.New("legacy layout: begin incremental: not supported")
}

// Commit is unused as the locations are static
func (l LegacyLocator) Commit(ctx context.Context, full *Backup) error {
	return nil
}

// FindLatest returns the static paths for a legacy repository backup
func (l LegacyLocator) FindLatest(ctx context.Context, repo storage.Repository) (*Backup, error) {
	return l.newFull(repo), nil
}

// Find is not supported for legacy backups.
func (l LegacyLocator) Find(ctx context.Context, repo storage.Repository, backupID string) (*Backup, error) {
	return nil, errors.New("legacy layout: find: not supported")
}

func (l LegacyLocator) newFull(repo storage.Repository) *Backup {
	backupPath := strings.TrimSuffix(repo.GetRelativePath(), ".git")

	return &Backup{
		Repository:   repo,
		ObjectFormat: git.ObjectHashSHA1.Format,
		Steps: []Step{
			{
				BundlePath:      backupPath + ".bundle",
				RefPath:         backupPath + ".refs",
				CustomHooksPath: filepath.Join(backupPath, "custom_hooks.tar"),
			},
		},
	}
}

// PointerLocator locates backup paths where each full backup is put into a
// unique timestamp directory and the latest backup taken is pointed to by a
// file named LATEST.
//
// Structure:
//
//	<repo relative path>/LATEST
//	<repo relative path>/<backup id>/LATEST
//	<repo relative path>/<backup id>/<nnn>.bundle
//	<repo relative path>/<backup id>/<nnn>.refs
//	<repo relative path>/<backup id>/<nnn>.custom_hooks.tar
type PointerLocator struct {
	Sink     Sink
	Fallback Locator
}

// BeginFull returns a tentative first step needed to create a new full backup.
func (l PointerLocator) BeginFull(ctx context.Context, repo storage.Repository, backupID string) *Backup {
	repoPath := strings.TrimSuffix(repo.GetRelativePath(), ".git")

	return &Backup{
		ID:           backupID,
		Repository:   repo,
		ObjectFormat: git.ObjectHashSHA1.Format,
		Steps: []Step{
			{
				BundlePath:      filepath.Join(repoPath, backupID, "001.bundle"),
				RefPath:         filepath.Join(repoPath, backupID, "001.refs"),
				CustomHooksPath: filepath.Join(repoPath, backupID, "001.custom_hooks.tar"),
			},
		},
	}
}

// BeginIncremental returns a tentative step needed to create a new incremental
// backup.  The incremental backup is always based off of the latest full
// backup. If there is no latest backup, a new full backup step is returned
// using fallbackBackupID
func (l PointerLocator) BeginIncremental(ctx context.Context, repo storage.Repository, fallbackBackupID string) (*Backup, error) {
	repoPath := strings.TrimSuffix(repo.GetRelativePath(), ".git")
	backupID, err := l.findLatestID(ctx, repoPath)
	if err != nil {
		if errors.Is(err, ErrDoesntExist) {
			return l.BeginFull(ctx, repo, fallbackBackupID), nil
		}
		return nil, fmt.Errorf("pointer locator: begin incremental: %w", err)
	}
	backup, err := l.find(ctx, repo, backupID)
	if err != nil {
		return nil, err
	}
	if len(backup.Steps) < 1 {
		return nil, fmt.Errorf("pointer locator: begin incremental: no full backup")
	}

	previous := backup.Steps[len(backup.Steps)-1]
	backupPath := filepath.Dir(previous.BundlePath)
	bundleName := filepath.Base(previous.BundlePath)
	incrementID := bundleName[:len(bundleName)-len(filepath.Ext(bundleName))]
	id, err := strconv.Atoi(incrementID)
	if err != nil {
		return nil, fmt.Errorf("pointer locator: begin incremental: determine increment ID: %w", err)
	}
	id++

	backup.ID = fallbackBackupID
	backup.Steps = append(backup.Steps, Step{
		BundlePath:      filepath.Join(backupPath, fmt.Sprintf("%03d.bundle", id)),
		RefPath:         filepath.Join(backupPath, fmt.Sprintf("%03d.refs", id)),
		PreviousRefPath: previous.RefPath,
		CustomHooksPath: filepath.Join(backupPath, fmt.Sprintf("%03d.custom_hooks.tar", id)),
	})

	return backup, nil
}

// Commit persists the step so that it can be looked up by FindLatest
func (l PointerLocator) Commit(ctx context.Context, backup *Backup) error {
	if len(backup.Steps) < 1 {
		return fmt.Errorf("pointer locator: commit: no steps")
	}
	step := backup.Steps[len(backup.Steps)-1]
	backupPath := filepath.Dir(step.BundlePath)
	bundleName := filepath.Base(step.BundlePath)
	repoPath := filepath.Dir(backupPath)
	backupID := filepath.Base(backupPath)
	incrementID := bundleName[:len(bundleName)-len(filepath.Ext(bundleName))]

	if err := l.writeLatest(ctx, backupPath, incrementID); err != nil {
		return fmt.Errorf("pointer locator: commit: %w", err)
	}
	if err := l.writeLatest(ctx, repoPath, backupID); err != nil {
		return fmt.Errorf("pointer locator: commit: %w", err)
	}
	return nil
}

// FindLatest returns the paths committed by the latest call to CommitFull.
//
// If there is no `LATEST` file, the result of the `Fallback` is used.
func (l PointerLocator) FindLatest(ctx context.Context, repo storage.Repository) (*Backup, error) {
	repoPath := strings.TrimSuffix(repo.GetRelativePath(), ".git")

	backupID, err := l.findLatestID(ctx, repoPath)
	if err != nil {
		if l.Fallback != nil && errors.Is(err, ErrDoesntExist) {
			return l.Fallback.FindLatest(ctx, repo)
		}
		return nil, fmt.Errorf("pointer locator: find latest: %w", err)
	}

	backup, err := l.find(ctx, repo, backupID)
	if err != nil {
		return nil, fmt.Errorf("pointer locator: find latest: %w", err)
	}
	return backup, nil
}

// Find returns the repository backup at the given backupID. If the backup does
// not exist then the error ErrDoesntExist is returned.
func (l PointerLocator) Find(ctx context.Context, repo storage.Repository, backupID string) (*Backup, error) {
	backup, err := l.find(ctx, repo, backupID)
	if err != nil {
		return nil, fmt.Errorf("pointer locator: %w", err)
	}
	return backup, nil
}

func (l PointerLocator) find(ctx context.Context, repo storage.Repository, backupID string) (*Backup, error) {
	repoPath := strings.TrimSuffix(repo.GetRelativePath(), ".git")
	backupPath := filepath.Join(repoPath, backupID)

	latestIncrementID, err := l.findLatestID(ctx, backupPath)
	if err != nil {
		return nil, fmt.Errorf("find: %w", err)
	}

	max, err := strconv.Atoi(latestIncrementID)
	if err != nil {
		return nil, fmt.Errorf("find: determine increment ID: %w", err)
	}

	backup := Backup{
		ID:           backupID,
		Repository:   repo,
		ObjectFormat: git.ObjectHashSHA1.Format,
	}

	for i := 1; i <= max; i++ {
		var previousRefPath string
		if i > 1 {
			previousRefPath = filepath.Join(backupPath, fmt.Sprintf("%03d.refs", i-1))
		}
		backup.Steps = append(backup.Steps, Step{
			BundlePath:      filepath.Join(backupPath, fmt.Sprintf("%03d.bundle", i)),
			RefPath:         filepath.Join(backupPath, fmt.Sprintf("%03d.refs", i)),
			PreviousRefPath: previousRefPath,
			CustomHooksPath: filepath.Join(backupPath, fmt.Sprintf("%03d.custom_hooks.tar", i)),
		})
	}

	return &backup, nil
}

func (l PointerLocator) findLatestID(ctx context.Context, backupPath string) (string, error) {
	r, err := l.Sink.GetReader(ctx, filepath.Join(backupPath, "LATEST"))
	if err != nil {
		return "", fmt.Errorf("find latest ID: %w", err)
	}
	defer r.Close()

	latest, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("find latest ID: %w", err)
	}

	return text.ChompBytes(latest), nil
}

func (l PointerLocator) writeLatest(ctx context.Context, path, target string) (returnErr error) {
	latest, err := l.Sink.GetWriter(ctx, filepath.Join(path, "LATEST"))
	if err != nil {
		return fmt.Errorf("write latest: %w", err)
	}
	defer func() {
		if err := latest.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("write latest: %w", err)
		}
	}()

	if _, err := latest.Write([]byte(target)); err != nil {
		return fmt.Errorf("write latest: %w", err)
	}

	return nil
}

const latestManifestName = "+latest"

// ManifestLocator locates backup paths based on manifest files that are
// written to a predetermined path:
//
//	manifests/<repo_storage_name>/<repo_relative_path>/<backup_id>.toml
//
// It relies on Fallback to determine paths of new backups.
type ManifestLocator struct {
	Loader   ManifestLoader
	Fallback Locator
}

// NewManifestLocator builds a new ManifestLocator.
func NewManifestLocator(sink Sink, fallback Locator) ManifestLocator {
	return ManifestLocator{
		Loader:   NewManifestLoader(sink),
		Fallback: fallback,
	}
}

// BeginFull returns a tentative first step needed to create a new full backup.
// The logic will be overridden by the fallback locator, if configured.
func (l ManifestLocator) BeginFull(ctx context.Context, repo storage.Repository, backupID string) *Backup {
	if l.Fallback != nil {
		return l.Fallback.BeginFull(ctx, repo, backupID)
	}

	storageName := repo.GetStorageName()
	relativePath := repo.GetRelativePath()

	return &Backup{
		ID:         backupID,
		Repository: repo,
		Steps: []Step{
			{
				BundlePath:      filepath.Join(storageName, relativePath, backupID, "001.bundle"),
				RefPath:         filepath.Join(storageName, relativePath, backupID, "001.refs"),
				CustomHooksPath: filepath.Join(storageName, relativePath, backupID, "001.custom_hooks.tar"),
			},
		},
	}
}

// BeginIncremental returns a tentative step needed to create a new incremental
// backup. The incremental backup is always based off of the latest backup. If
// there is no latest backup, a new full backup step is returned using
// backupID. The logic will be overridden by the fallback locator, if
// configured.
func (l ManifestLocator) BeginIncremental(ctx context.Context, repo storage.Repository, backupID string) (*Backup, error) {
	if l.Fallback != nil {
		return l.Fallback.BeginIncremental(ctx, repo, backupID)
	}

	backup, err := l.Loader.ReadManifest(ctx, repo, latestManifestName)
	switch {
	case errors.Is(err, ErrDoesntExist):
		return l.BeginFull(ctx, repo, backupID), nil
	case err != nil:
		return nil, fmt.Errorf("manifest: begin incremental: %w", err)
	}

	storageName := repo.GetStorageName()
	relativePath := repo.GetRelativePath()
	n := len(backup.Steps) + 1

	// This is a convenience that could be calculated but it is cheap enough to
	// generate here. It means that the increment generating code only needs to
	// refer to a single step.
	var previousRefPath string
	if len(backup.Steps) > 0 {
		previousRefPath = backup.Steps[len(backup.Steps)-1].RefPath
	}

	backup.ID = backupID
	backup.Steps = append(backup.Steps, Step{
		BundlePath:      filepath.Join(storageName, relativePath, backupID, fmt.Sprintf("%03d.bundle", n)),
		RefPath:         filepath.Join(storageName, relativePath, backupID, fmt.Sprintf("%03d.refs", n)),
		PreviousRefPath: previousRefPath,
		CustomHooksPath: filepath.Join(storageName, relativePath, backupID, fmt.Sprintf("%03d.custom_hooks.tar", n)),
	})

	return backup, nil
}

// Commit passes through to Fallback, then writes a manifest file for the backup.
func (l ManifestLocator) Commit(ctx context.Context, backup *Backup) error {
	if l.Fallback != nil {
		if err := l.Fallback.Commit(ctx, backup); err != nil {
			return err
		}
	}

	if err := l.Loader.WriteManifest(ctx, backup, backup.ID); err != nil {
		return fmt.Errorf("manifest: commit: %w", err)
	}
	if err := l.Loader.WriteManifest(ctx, backup, latestManifestName); err != nil {
		return fmt.Errorf("manifest: commit latest: %w", err)
	}

	return nil
}

// FindLatest loads the manifest called +latest. If this manifest does not
// exist, the Fallback is used.
func (l ManifestLocator) FindLatest(ctx context.Context, repo storage.Repository) (*Backup, error) {
	backup, err := l.Loader.ReadManifest(ctx, repo, latestManifestName)
	switch {
	case l.Fallback != nil && errors.Is(err, ErrDoesntExist):
		return l.Fallback.FindLatest(ctx, repo)
	case err != nil:
		return nil, fmt.Errorf("manifest: find latest: %w", err)
	}

	return backup, nil
}

// Find loads the manifest for the provided repo and backupID. If this manifest
// does not exist, the fallback is used.
func (l ManifestLocator) Find(ctx context.Context, repo storage.Repository, backupID string) (*Backup, error) {
	backup, err := l.Loader.ReadManifest(ctx, repo, backupID)
	switch {
	case l.Fallback != nil && errors.Is(err, ErrDoesntExist):
		return l.Fallback.Find(ctx, repo, backupID)
	case err != nil:
		return nil, fmt.Errorf("manifest: find: %w", err)
	}

	return backup, nil
}
