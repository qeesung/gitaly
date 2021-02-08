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
)

type Cache struct {
	m          sync.Mutex
	dir        string
	expiry     time.Duration
	index      map[string]*entry
	queue      []*entry
	numEntries int
	id         string
}

func NewCache(dir string, expiry time.Duration) (*Cache, error) {
	buf := make([]byte, 10)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, err
	}

	c := &Cache{
		dir:    dir,
		expiry: expiry,
		index:  make(map[string]*entry),
		id:     string(buf),
	}

	dontpanic.GoForever(1*time.Minute, c.clean)

	return c, nil
}

func (c *Cache) clean() {
	for {
		sleep := time.Minute
		if c.expiry < sleep {
			sleep = c.expiry
		}
		time.Sleep(sleep)

		cutoff := time.Now().Add(-c.expiry)
		c.pruneIndex(cutoff)
		filepath.Walk(c.dir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && info.ModTime().Before(cutoff) {
				err = os.Remove(path)
			}

			return err
		})
	}
}

func (c *Cache) pruneIndex(cutoff time.Time) {
	c.m.Lock()
	defer c.m.Unlock()

	for len(c.queue) > 0 && c.queue[0].created.Before(cutoff) {
		delete(c.index, c.queue[0].key)
		c.queue = c.queue[1:]
	}
}

func (c *Cache) FindOrCreate(key string, create func(io.Writer) error) (io.ReadCloser, error) {
	c.m.Lock()
	defer c.m.Unlock()

	if e := c.index[key]; e != nil {
		if r, err := e.Open(); err == nil {
			return r, nil
		}
	}

	r, e, err := c.newEntry(key, create)
	if err != nil {
		return nil, err
	}

	c.index[key] = e
	c.queue = append(c.queue, e)

	return r, nil
}

func (c *Cache) entryPath(id int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s-%d", c.id, id)
	name := fmt.Sprintf("%x", h.Sum(nil))
	return filepath.Join(c.dir, name[0:2], name[2:4], name[4:])
}

type entry struct {
	key     string
	c       *Cache
	id      int
	created time.Time
}

func (c *Cache) newEntry(key string, create func(io.Writer) error) (io.ReadCloser, *entry, error) {
	e := &entry{
		key:     key,
		c:       c,
		id:      c.numEntries,
		created: time.Now(),
	}
	c.numEntries++

	path := c.entryPath(e.id)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, nil, err
	}

	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	if err := create(f); err != nil {
		return nil, nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}

	return f, e, nil
}

func (e *entry) Open() (io.ReadCloser, error) { return os.Open(e.c.entryPath(e.id)) }
