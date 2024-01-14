package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
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

// ManifestLocator locates backup paths based on manifest files that are
// written to a predetermined path:
//
//	manifests/<repo_storage_name>/<repo_relative_path>/<backup_id>.toml
//
// It relies on Fallback to determine paths of new backups.
type ManifestLocator struct {
	Sink     Sink
	Fallback Locator
}

// BeginFull passes through to Fallback
func (l ManifestLocator) BeginFull(ctx context.Context, repo storage.Repository, backupID string) *Backup {
	return l.Fallback.BeginFull(ctx, repo, backupID)
}

// BeginIncremental passes through to Fallback
func (l ManifestLocator) BeginIncremental(ctx context.Context, repo storage.Repository, backupID string) (*Backup, error) {
	return l.Fallback.BeginIncremental(ctx, repo, backupID)
}

// Commit passes through to Fallback, then writes a manifest file for the backup.
func (l ManifestLocator) Commit(ctx context.Context, backup *Backup) (returnErr error) {
	if err := l.Fallback.Commit(ctx, backup); err != nil {
		return err
	}

	if err := l.writeManifest(ctx, backup, backup.ID); err != nil {
		return fmt.Errorf("manifest: commit: %w", err)
	}
	if err := l.writeManifest(ctx, backup, "+latest"); err != nil {
		return fmt.Errorf("manifest: commit: %w", err)
	}

	return nil
}

// FindLatest passes through to Fallback
func (l ManifestLocator) FindLatest(ctx context.Context, repo storage.Repository) (*Backup, error) {
	return l.Fallback.FindLatest(ctx, repo)
}

// Find loads the manifest for the provided repo and backupID. If this manifest
// does not exist, the fallback is used.
func (l ManifestLocator) Find(ctx context.Context, repo storage.Repository, backupID string) (*Backup, error) {
	f, err := l.Sink.GetReader(ctx, manifestPath(repo, backupID))
	switch {
	case errors.Is(err, ErrDoesntExist):
		return l.Fallback.Find(ctx, repo, backupID)
	case err != nil:
		return nil, fmt.Errorf("manifest: find: %w", err)
	}
	defer f.Close()

	var backup Backup

	if err := toml.NewDecoder(f).Decode(&backup); err != nil {
		return nil, fmt.Errorf("manifest: find: %w", err)
	}

	backup.ID = backupID
	backup.Repository = repo

	return &backup, nil
}

func (l ManifestLocator) writeManifest(ctx context.Context, backup *Backup, backupID string) (returnErr error) {
	f, err := l.Sink.GetWriter(ctx, manifestPath(backup.Repository, backupID))
	if err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	defer func() {
		if err := f.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("write manifest: %w", err)
		}
	}()

	if err := toml.NewEncoder(f).Encode(backup); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	return nil
}

func manifestPath(repo storage.Repository, backupID string) string {
	storageName := repo.GetStorageName()
	// Other locators strip the .git suffix off of relative paths. This suffix
	// is determined by gitlab-rails not gitaly. So here we leave the relative
	// path as-is so that new backups can be more independent.
	relativePath := repo.GetRelativePath()
	return path.Join("manifests", storageName, relativePath, backupID+".toml")
}
