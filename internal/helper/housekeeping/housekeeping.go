package housekeeping

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"golang.org/x/net/context"
)

const deleteTempFilesOlderThanDuration = 7 * 24 * time.Hour

// PerformHousekeeping will perform housekeeping duties on a repository
func PerformHousekeeping(ctx context.Context, repoPath string) error {
	log := grpc_logrus.Extract(ctx)

	return filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if repoPath == path {
			// Never consider the root path
			return nil
		}

		if info == nil || !shouldUnlink(path, info.ModTime(), info.Mode(), err) {
			return nil
		}

		err = forceRemove(path)
		if err != nil {
			log.WithError(err).WithField("path", path).Warn("Unable to remove stray file")
		}

		if info.IsDir() {
			// Do not walk removed directories
			return filepath.SkipDir
		}

		return nil
	})
}

func forceOwnership(path string) {
	filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		os.Chown(path, os.Getuid(), os.Getgid())

		if info.IsDir() {
			os.Chmod(path, 0700)
		} else {
			os.Chmod(path, 0600)
		}

		return nil
	})
}

// Delete a directory structure while ensuring the current user has permission to delete the directory structure
func forceRemove(path string) error {
	err := os.RemoveAll(path)
	if err == nil {
		return nil
	}

	// Delete failed. Try again after chmod and chowning recursively
	forceOwnership(path)

	return os.RemoveAll(path)
}

func shouldUnlink(path string, modTime time.Time, mode os.FileMode, err error) bool {
	base := filepath.Base(path)

	// Only tmp_ prefixed files will every be deleted
	if !strings.HasPrefix(base, "tmp_") {
		return false
	}

	// Unable to access the file AND it starts with `tmp_`? Try delete it
	if err != nil {
		return true
	}

	// Delete anything older than...
	if time.Since(modTime) >= deleteTempFilesOlderThanDuration {
		return true
	}

	// Delete anything with zero permissions
	if mode.Perm() == 0 {
		return true
	}

	return false
}
