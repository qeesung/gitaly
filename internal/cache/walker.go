package cache

import (
	"os"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/config"
)

var (
	walkerEventTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_diskcache_walker",
			Help: "Total number of events during diskcache filesystem walks",
		},
		[]string{"event"},
	)
)

func init() { prometheus.MustRegister(walkerEventTotal) }

func countWalkRemoval() { walkerEventTotal.WithLabelValues("removal").Inc() }
func countWalkCheck()   { walkerEventTotal.WithLabelValues("check").Inc() }

// TODO: replace constant with constant defined in !1305
const fileStaleness = time.Hour

func cleanWalk(storagePath string) error {
	return filepath.Walk(storagePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		countWalkCheck()

		threshold := time.Now().Add(-1 * fileStaleness)
		if info.ModTime().After(threshold) {
			return nil
		}

		if err := os.Remove(path); err != nil {
			return err
		}

		countWalkRemoval()

		return nil
	})
}

const cleanWalkFrequency = 10 * time.Minute

func startCleanWalker(storage config.Storage) {
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
