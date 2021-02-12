package streamcache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/maphash"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/dontpanic"
	"gitlab.com/gitlab-org/gitaly/internal/git/housekeeping"
	"gitlab.com/gitlab-org/labkit/log"
)

type filestore struct {
	m        sync.Mutex
	dir      string
	expiry   time.Duration
	id       string
	entries  int64
	hasher   maphash.Hash
	stop     chan struct{}
	stopOnce sync.Once
}

func newFilestore(dir string, expiry time.Duration) (*filestore, error) {
	buf := make([]byte, 10)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, err
	}

	fs := &filestore{
		dir:    dir,
		expiry: expiry,
		id:     hex.EncodeToString(buf),
		stop:   make(chan struct{}),
	}

	dontpanic.GoForever(1*time.Minute, func() {
		sleepLoop(fs.stop, fs.expiry, func() {
			if err := fs.cleanWalk(time.Now().Add(-fs.expiry)); err != nil {
				log.WithError(err).Error("streamcache filestore cleanup")
			}
		})
	})

	return fs, nil
}

func (fs *filestore) Create() (*os.File, error) {
	fs.m.Lock()
	defer fs.m.Unlock()

	name := fmt.Sprintf("%s-%d",
		// fs.id ensures uniqueness among other *filestore instances
		fs.id,
		// entries ensures uniqueness (modulo roll-over) among other files
		// created by this *filestore instance
		fs.entries,
	)
	fs.entries++

	// Use a nested directory scheme to avoid one big directory with many
	// files: this makes directory walks faster. Use a uniform hash to ensure
	// files are distributed evenly across nested directories.
	fs.hasher.Reset()
	_, _ = fs.hasher.WriteString(name)
	sum := fs.hasher.Sum64()
	path := filepath.Join(
		fs.dir,
		fmt.Sprintf("%02x", byte(sum>>8)),
		fmt.Sprintf("%02x", byte(sum)),
		name,
	)

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}

	return os.Create(path)
}

func (fs *filestore) Stop() { fs.stopOnce.Do(func() { close(fs.stop) }) }

// cleanWalk removes files but not directories. This is to avoid races
// when a directory looks empty but another goroutine is about to create
// a new file in it with fs.Create(). Because the number of directories is
// bounded by the /aa/bb radix scheme (65536 possible values) there is no
// need to remove the directories anyway.
func (fs *filestore) cleanWalk(cutoff time.Time) error {
	if err := housekeeping.FixDirectoryPermissions(context.Background(), fs.dir); err != nil {
		return err
	}

	return filepath.Walk(fs.dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.ModTime().Before(cutoff) {
			err = os.Remove(path)
		}

		return err
	})
}
