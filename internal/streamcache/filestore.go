package streamcache

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/dontpanic"
	"gitlab.com/gitlab-org/labkit/log"
)

type filestore struct {
	m       sync.Mutex
	dir     string
	expiry  time.Duration
	id      string
	entries int64
}

func newFilestore(dir string, expiry time.Duration) (*filestore, error) {
	buf := make([]byte, 10)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, err
	}

	fs := &filestore{
		dir:    dir,
		expiry: expiry,
		id:     string(buf),
	}

	dontpanic.GoForever(1*time.Minute, fs.clean)

	return fs, nil
}

func (fs *filestore) Create() (*os.File, error) {
	fs.m.Lock()
	defer fs.m.Unlock()

	h := sha256.New()
	fmt.Fprintf(h, "%s-%d", fs.id, fs.entries)
	fs.entries++

	name := fmt.Sprintf("%x", h.Sum(nil))
	path := filepath.Join(fs.dir, name[0:2], name[2:4], name[4:])

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}

	return os.Create(path)
}

func (fs *filestore) clean() {
	sleepLoop(fs.expiry, func() {
		cutoff := time.Now().Add(-fs.expiry)

		if err := filepath.Walk(fs.dir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && info.ModTime().Before(cutoff) {
				err = os.Remove(path)
			}

			return err
		}); err != nil {
			log.WithError(err).Error("streamcache filestore cleanup")
		}
	})
}
