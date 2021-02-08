package streamcache

import (
	"io"
	"os"
	"sync"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/dontpanic"
)

// Cache is a cache for large blobs (in the order of gigabytes). Because
// storing gigabytes of data is slow, cache entries can be streamed on
// the read end before they have finished on the write end. Because
// storing gigabytes of data is expensive, cache entries have a back
// pressure mechanism: if the readers don't make progress reading the
// data, the writers will block. That way our disk can fill up no faster
// than our readers can read from the cache.
type Cache struct {
	m      sync.Mutex
	expiry time.Duration
	index  map[string]*entry
	queue  []*entry
	fs     *filestore
}

// NewCache returns a new cache instance.
func NewCache(dir string, expiry time.Duration) (*Cache, error) {
	fs, err := newFilestore(dir, expiry)
	if err != nil {
		return nil, err
	}

	c := &Cache{
		expiry: expiry,
		index:  make(map[string]*entry),
		fs:     fs,
	}

	dontpanic.GoForever(1*time.Minute, c.clean)

	return c, nil
}

func (c *Cache) clean() {
	sleepLoop(c.expiry, func() {
		c.m.Lock()
		defer c.m.Unlock()

		cutoff := time.Now().Add(-c.expiry)
		for len(c.queue) > 0 && c.queue[0].created.Before(cutoff) {
			if e := c.queue[0]; c.index[e.key] == e {
				delete(c.index, e.key)
			}

			c.queue = c.queue[1:]
		}
	})
}

func sleepLoop(period time.Duration, fn func()) {
	sleep := time.Minute
	if 0 < period && period < sleep {
		sleep = period
	}

	for {
		time.Sleep(sleep)
		fn()
	}
}

// FindOrCreate finds or creates a cache entry.
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

type entry struct {
	key     string
	c       *Cache
	path    string
	created time.Time
}

func (c *Cache) newEntry(key string, create func(io.Writer) error) (io.ReadCloser, *entry, error) {
	e := &entry{
		key:     key,
		c:       c,
		created: time.Now(),
	}

	f, err := c.fs.Create()
	if err != nil {
		return nil, nil, err
	}
	e.path = f.Name()

	if err := create(f); err != nil {
		return nil, nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}

	return f, e, nil
}

func (e *entry) Open() (io.ReadCloser, error) { return os.Open(e.path) }
