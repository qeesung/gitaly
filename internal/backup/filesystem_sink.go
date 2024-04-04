package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

// FilesystemSink is a sink for creating and restoring backups from the local filesystem.
type FilesystemSink struct {
	path string
}

// NewFilesystemSink returns a sink that uses a local filesystem to work with data.
func NewFilesystemSink(path string) *FilesystemSink {
	return &FilesystemSink{
		path: path,
	}
}

// GetWriter opens a io.WriteCloser that can be used to write data into a
// relativePath path on the filesystem. It is the callers responsibility to
// Close the writer after usage.
func (fs *FilesystemSink) GetWriter(ctx context.Context, relativePath string) (io.WriteCloser, error) {
	path := filepath.Join(fs.path, relativePath)

	if err := os.MkdirAll(filepath.Dir(path), perm.PrivateDir); err != nil {
		return nil, fmt.Errorf("filesystem sink: %w", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, perm.PrivateFile)
	if err != nil {
		return nil, fmt.Errorf("filesystem sink: %w", err)
	}
	return f, nil
}

// GetReader returns a reader of the requested file path.
// It's the caller's responsibility to Close returned reader once it is not needed anymore.
// If relativePath doesn't exist the ErrDoesntExist is returned.
func (fs *FilesystemSink) GetReader(ctx context.Context, relativePath string) (io.ReadCloser, error) {
	path := filepath.Join(fs.path, relativePath)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrDoesntExist
		}
		return nil, fmt.Errorf("filesystem sink: %w", err)
	}
	return f, nil
}

// Close will fsync the written file and its parent directory.
func (fs *FilesystemSink) Close() error {
	_, err := os.Stat(fs.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat sink path: %w", err)
	}

	syncer := safe.NewSyncer()
	if err := syncer.Sync(fs.path); err != nil {
		return fmt.Errorf("sync sink: %w", err)
	}
	if err := syncer.SyncParent(fs.path); err != nil {
		return fmt.Errorf("sync sink parent: %w", err)
	}
	return nil
}

// SignedURL is not supported by FilesystemSink.
func (fs *FilesystemSink) SignedURL(ctx context.Context, relativePath string, expiry time.Duration) (string, error) {
	return "", structerr.NewUnimplemented("SignedURL not implemented for FilesystemSink")
}
