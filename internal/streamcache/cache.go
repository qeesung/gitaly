package streamcache

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Cache struct {
	m     sync.Mutex
	dir   string
	index map[string]*entry
}

func NewCache(dir string, expiry time.Duration) (*Cache, error) {
	return &Cache{
		dir:   dir,
		index: make(map[string]*entry),
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

type entry struct {
	key string
	c   *Cache
}

func (c *Cache) newEntry(key string, create func(io.Writer) error) (io.ReadCloser, *entry, error) {
	f, err := os.Create(filepath.Join(c.dir, key))
	if err != nil {
		return nil, nil, err
	}
	if err := create(f); err != nil {
		return nil, nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}

	return f, &entry{key: key, c: c}, nil
}

func (e *entry) Open() (io.ReadCloser, error) { return os.Open(filepath.Join(e.c.dir, e.key)) }
