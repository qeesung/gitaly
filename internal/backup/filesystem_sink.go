package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// Write creates required file structure and stored data from r into relativePath location.
// If created file is empty it will be removed.
func (fs *FilesystemSink) Write(ctx context.Context, relativePath string, r io.Reader) (returnErr error) {
	path := filepath.Join(fs.path, relativePath)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory structure %q: %w", dir, err)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write file %q: %w", path, err)
	}
	defer func() {
		if err := f.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("write file %q: %w", path, err)
		}
	}()
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("write file %q: %w", path, err)
	}
	return nil
}

// GetReader returns a reader of the requested file path.
// It's the caller's responsibility to Close returned reader once it is not needed anymore.
// If relativePath doesn't exist the ErrDoesntExist is returned.
func (fs *FilesystemSink) GetReader(ctx context.Context, relativePath string) (io.ReadCloser, error) {
	path := filepath.Join(fs.path, relativePath)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = ErrDoesntExist
		}
		return nil, fmt.Errorf("filesystem sink: get reader for %q: %w", relativePath, err)
	}
	return f, nil
}
