package tempdir

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper"

	log "github.com/sirupsen/logrus"
)

const (
	// We need to be careful that this path does not clash with any
	// directory name that could be provided by a user. The '+' character is
	// not allowed in GitLab namespaces or repositories.
	tmpRootPrefix = "+gitaly/tmp"
)

// New returns the path of a new temporary directory for use with the
// repository. The directory is removed with os.RemoveAll when ctx
// expires.
func New(ctx context.Context, repo *pb.Repository) (string, error) {
	storageDir, err := helper.GetStorageByName(repo.StorageName)
	if err != nil {
		return "", err
	}

	root := tmpRoot(storageDir)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}

	tempDir, err := ioutil.TempDir(root, "repo")
	if err != nil {
		return "", err
	}

	go func() {
		<-ctx.Done()
		os.RemoveAll(tempDir)
	}()

	return tempDir, nil
}

func tmpRoot(storageRoot string) string {
	return path.Join(storageRoot, tmpRootPrefix)
}

// StartCleaning starts tempdir cleanup goroutines.
func StartCleaning() {
	for _, st := range config.Config.Storages {
		go func(name string, dir string) {
			start := time.Now()
			err := clean(tmpRoot(dir))

			entry := log.WithFields(log.Fields{
				"time_ms": int(1000 * time.Since(start).Seconds()),
				"storage": name,
			})
			if err != nil {
				entry = entry.WithError(err)
			}
			entry.Info("finished tempdir cleaner walk")

			time.Sleep(1 * time.Hour)
		}(st.Name, st.Path)
	}
}

func clean(dir string) error {
	// If we start "cleaning up" the wrong directory we may delete user data
	// which is Really Bad.
	if !strings.HasSuffix(dir, tmpRootPrefix) {
		log.Print(dir)
		panic(invalidCleanRoot("invalid tempdir clean root: panicking to prevent data loss"))
	}

	return filepath.Walk(dir, cleanFunc)
}

const (
	maxAge = 7 * 24 * time.Hour
)

type invalidCleanRoot string

func cleanFunc(path string, info os.FileInfo, errIncoming error) error {
	if errIncoming != nil && !os.IsNotExist(errIncoming) {
		return fmt.Errorf("incoming %q: %v", path, errIncoming)
	}

	if info == nil {
		return nil
	}

	if perm := info.Mode().Perm(); info.IsDir() && perm&0700 < 0700 {
		// Fix directory read permissions
		if err := os.Chmod(path, perm|0700); err != nil {
			return err
		}
	}

	if time.Since(info.ModTime()) < maxAge {
		return nil
	}

	if err := os.Remove(path); err != nil && !info.IsDir() {
		return err
	}

	return nil
}
