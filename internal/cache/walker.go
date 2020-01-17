// Package cache supplies background workers for periodically cleaning the
// cache folder on all storages listed in the config file. Upon configuration
// validation, one worker will be started for each storage. The worker will
// walk the cache directory tree and remove any files older than one hour. The
// worker will walk the cache directory every ten minutes.
package cache

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/dontpanic"
	"gitlab.com/gitlab-org/gitaly/internal/log"
	"gitlab.com/gitlab-org/gitaly/internal/tempdir"
)

func logWalkErr(err error, path, msg string) {
	countWalkError()
	log.Default().
		WithField("path", path).
		WithError(err).
		Warn(msg)
}

func cleanWalk(s config.Storage, path string) error {
	defer time.Sleep(100 * time.Microsecond) // relieve pressure

	countWalkCheck()
	entries, err := ioutil.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		logWalkErr(err, path, "unable to stat directory")
		return err
	}

	for _, e := range entries {
		ePath := filepath.Join(path, e.Name())

		if e.IsDir() {
			if err := cleanWalk(s, ePath); err != nil {
				return err
			}
			continue
		}

		countWalkCheck()
		if time.Since(e.ModTime()) < staleAge {
			continue // still fresh
		}

		// file is stale
		if err := os.Remove(ePath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			logWalkErr(err, ePath, "unable to remove file")
			return err
		}
		countWalkRemoval()
	}

	files, err := ioutil.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		logWalkErr(err, path, "unable to stat directory after walk")
		return err
	}

	if len(files) == 0 {
		countEmptyDir(s)
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			logWalkErr(err, path, "unable to remove empty directory")
			return err
		}
		countEmptyDirRemoval(s)
		countWalkRemoval()
	}

	return nil
}

const cleanWalkFrequency = 10 * time.Minute

func walkLoop(s config.Storage, walkPath string) {
	logrus.WithField("storage", s.Name).Infof("Starting file walker for %s", walkPath)
	walkTick := time.NewTicker(cleanWalkFrequency)
	dontpanic.GoForever(time.Minute, func() {
		for {
			if err := cleanWalk(s, walkPath); err != nil {
				logrus.WithField("storage", s.Name).Error(err)
			}

			<-walkTick.C
		}
	})
}

func startCleanWalker(storage config.Storage) {
	if disableWalker {
		return
	}

	walkLoop(storage, tempdir.CacheDir(storage))
	walkLoop(storage, tempdir.StateDir(storage))
}

var (
	disableMoveAndClear bool // only used to disable move and clear in tests
	disableWalker       bool // only used to disable object walker in tests
)

// moveAndClear will move the cache to the storage location's
// temporary folder, and then remove its contents asynchronously
func moveAndClear(storage config.Storage) error {
	if disableMoveAndClear {
		return nil
	}

	logger := logrus.WithField("storage", storage.Name)
	logger.Info("clearing disk cache object folder")

	tempPath := tempdir.TempDir(storage)
	if err := os.MkdirAll(tempPath, 0755); err != nil {
		return err
	}

	tmpDir, err := ioutil.TempDir(tempPath, "diskcache")
	if err != nil {
		return err
	}

	logger.Infof("moving disk cache object folder to %s", tmpDir)
	cachePath := tempdir.CacheDir(storage)
	if err := os.Rename(cachePath, filepath.Join(tmpDir, "moved")); err != nil {
		if os.IsNotExist(err) {
			logger.Info("disk cache object folder doesn't exist, no need to remove")
			return nil
		}

		return err
	}

	dontpanic.Go(func() {
		start := time.Now()
		if err := os.RemoveAll(tmpDir); err != nil {
			logger.Errorf("unable to remove disk cache objects: %q", err)
		}

		logger.Infof("cleared all cache object files in %s after %s", tmpDir, time.Since(start))
	})

	return nil
}

func init() {
	oncePerStorage := map[string]*sync.Once{}
	var err error

	config.RegisterHook(func(cfg config.Cfg) error {
		for _, storage := range cfg.Storages {
			if _, ok := oncePerStorage[storage.Name]; !ok {
				oncePerStorage[storage.Name] = new(sync.Once)
			}
			oncePerStorage[storage.Name].Do(func() {
				if err = moveAndClear(storage); err != nil {
					return
				}
				startCleanWalker(storage)
			})
		}
		return err
	})
}
