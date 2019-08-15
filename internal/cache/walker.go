// Package cache supplies background workers for periodically cleaning the
// cache folder on all storages listed in the config file. Upon configuration
// validation, one worker will be started for each storage. The worker will
// walk the cache directory tree and remove any files older than one hour. The
// worker will walk the cache directory every ten minutes.
package cache

import (
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/tempdir"
)

func cleanWalk(storagePath string) error {
	cachePath := filepath.Join(storagePath, tempdir.CachePrefix)

	err := filepath.Walk(cachePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		countWalkCheck()

		threshold := time.Now().Add(-1 * staleAge)
		if info.ModTime().After(threshold) {
			return nil
		}

		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				// race condition: another file walker on the same storage may
				// have deleted the file already
				return nil
			}
			return err
		}

		countWalkRemoval()

		return nil
	})

	if os.IsNotExist(err) {
		return nil
	}

	return err
}

const cleanWalkFrequency = 10 * time.Minute

func startCleanWalker(storage config.Storage) {
	logrus.WithField("storage", storage.Name).Info("Starting disk cache object walker")
	walkTick := time.NewTicker(cleanWalkFrequency)
	go func() {
		for {
			if err := cleanWalk(storage.Path); err != nil {
				logrus.WithField("storage", storage.Name).Error(err)
			}
			<-walkTick.C
		}
	}()
}

func init() {
	config.RegisterHook(func() error {
		for _, storage := range config.Config.Storages {
			startCleanWalker(storage)
		}
		return nil
	})
}
