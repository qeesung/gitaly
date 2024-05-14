package backup

import (
	"context"
	"fmt"
	"path"

	"github.com/pelletier/go-toml/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
)

// ManifestLoader reads and writes manifest files from a Sink. Manifest files
// are used to persist all details about a repository needed to properly
// restore it to a known state.
type ManifestLoader struct {
	sink Sink
}

// NewManifestLoader builds a new ManifestLoader
func NewManifestLoader(sink Sink) ManifestLoader {
	return ManifestLoader{
		sink: sink,
	}
}

// ReadManifest reads a manifest from the sink for the specified backup ID.
func (l ManifestLoader) ReadManifest(ctx context.Context, repo storage.Repository, backupID string) (*Backup, error) {
	f, err := l.sink.GetReader(ctx, manifestPath(repo, backupID))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	defer f.Close()

	var backup Backup

	if err := toml.NewDecoder(f).Decode(&backup); err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	backup.ID = backupID
	backup.Repository = repo

	return &backup, nil
}

// WriteManifest writes a manifest to the sink for the specified backup ID.
func (l ManifestLoader) WriteManifest(ctx context.Context, backup *Backup, backupID string) (returnErr error) {
	f, err := l.sink.GetWriter(ctx, manifestPath(backup.Repository, backupID))
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
