package housekeeping

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

const deleteTempFilesOlderThanDuration = 7 * 24 * time.Hour

// Perform will perform housekeeping duties on a repository
func Perform(ctx context.Context, repoPath string) error {
	logger := grpc_logrus.Extract(ctx).WithField("system", "housekeeping")

	filesMarkedForRemoval := 0
	unremovableFiles := 0

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if repoPath == path {
			// Never consider the root path
			return nil
		}

		if info == nil || !shouldRemove(path, info.ModTime(), info.Mode()) {
			return nil
		}

		filesMarkedForRemoval++

		err = forceRemove(path)
		if err != nil {
			unremovableFiles++
			logger.WithError(err).WithField("path", path).Warn("unable to remove stray file")
		}

		if info.IsDir() {
			// Do not walk removed directories
			return filepath.SkipDir
		}

		return nil
	})

	if filesMarkedForRemoval > 0 {
		logger.WithFields(log.Fields{
			"files":    filesMarkedForRemoval,
			"failures": unremovableFiles,
		}).Info("removed files")
	}

	return err
}

func FixDirectoryPermissions(path string, retriedPaths map[string]struct{}) error {
	if retriedPaths == nil {
		retriedPaths = make(map[string]struct{})
	}

	return filepath.Walk(path, func(path string, info os.FileInfo, errIncoming error) error {
		if !info.IsDir() || info.Mode()&0700 >= 0700 {
			return nil
		}

		if err := os.Chmod(path, info.Mode()|0700); err != nil {
			return err
		}

		if _, retried := retriedPaths[path]; !retried && os.IsPermission(errIncoming) {
			retriedPaths[path] = struct{}{}
			return FixDirectoryPermissions(path, retriedPaths)
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

	// Delete failed. Try again after chmod'ing directories recursively
	if err := FixDirectoryPermissions(path, nil); err != nil {
		return err
	}

	return os.RemoveAll(path)
}

func shouldRemove(path string, modTime time.Time, mode os.FileMode) bool {
	base := filepath.Base(path)

	// Only delete entries starting with `tmp_` and older than a week
	return strings.HasPrefix(base, "tmp_") && time.Since(modTime) >= deleteTempFilesOlderThanDuration
}
