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
)

type Cache struct {
	m          sync.Mutex
	dir        string
	index      map[string]*entry
	numEntries int
	id         string
}

func NewCache(dir string, expiry time.Duration) (*Cache, error) {
	buf := make([]byte, 10)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, err
	}

	return &Cache{
		dir:   dir,
		index: make(map[string]*entry),
		id:    string(buf),
	}, nil
}

func (c *Cache) FindOrCreate(key string, create func(io.Writer) error) (io.ReadCloser, error) {
	c.m.Lock()
	defer c.m.Unlock()

	if e := c.index[key]; e != nil {
		if r, err := e.Open(); err == nil {
			// Cache hit
			return r, nil
		}

		// Entry is broken, will be replaced below
	}

	r, e, err := c.newEntry(key, create)
	if err != nil {
		return nil, err
	}

	c.index[key] = e

	return r, nil
}

func (c *Cache) entryPath(id int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s-%d", c.id, id)
	name := fmt.Sprintf("%x", h.Sum(nil))
	return filepath.Join(c.dir, name[0:2], name[2:4], name[4:])
}

type entry struct {
	key string
	c   *Cache
	id  int
}

func (c *Cache) newEntry(key string, create func(io.Writer) error) (io.ReadCloser, *entry, error) {
	e := &entry{key: key, c: c, id: c.numEntries}
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
