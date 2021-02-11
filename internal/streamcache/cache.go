package streamcache

import (
	"fmt"
	"io"
	"sync"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/dontpanic"
	"gitlab.com/gitlab-org/labkit/log"
)

// Cache is a cache for large blobs (in the order of gigabytes). Because
// storing gigabytes of data is slow, cache entries can be streamed on
// the read end before they have finished on the write end. Because
// storing gigabytes of data is expensive, cache entries have a back
// pressure mechanism: if the readers don't make progress reading the
// data, the writers will block. That way our disk can fill up no faster
// than our readers can read from the cache.
//
// The cache has 3 main parts: Cache (in-memory index), filestore (files
// to store the cached data in because it does not fit in memory), and
// pipe (coordinated access to one file between one writer and multiple
// readers). A cache entry consists of a key, an expiration time and a
// pipe.
//
// Pipes
//
//           +------+
//           |      | -> reader
// writer -> | pipe | -> reader
//           |      | -> reader
//           +------+
//             |  ^
//             v  |
//           +------+
//           | file |
//           +------+
//
// Pipes are called so because their interface and behavior somewhat
// resembles Unix pipes, except there are multiple readers as opposed to
// just one. Just like with Unix pipes, pipe readers exert backpressure
// on the writer. When the write end is closed, the readers see EOF, just
// like they would when reading a file. When the read end is closed
// before the writer is done, the writer receives an error. This is all
// like it is with Unix pipes.
//
// The differences are as follows. When you create a pipe you get a write
// end and a read end, just like with Unix pipes. But now you can open an
// additional reader by calling OpenReader on the write end (the *pipe
// instance). Under the covers, a Unix pipe is just a buffer, and you
// cannot "rewind" it. With our pipe, there is an underlying file, so a
// new reader starts at offset 0 in the file. It can then catch up with
// the other readers.
//
// The way backpressure works is that the writer looks at the fastest
// reader. More specifically, it looks at the maximum of the file offsets
// of all the readers. This is useful because imagine what happens when a
// pipe is created by a reader on a slow network connection, and later a
// fast reader joins. The fast reader should not have to wait for the
// slow reader. What will happen is that the fast reader will catch up
// with the slow reader and overtake it. From that point on, the writer
// moves faster too, because it feels the backpressure of the fastest
// reader.

type Cache struct {
	m        sync.Mutex
	expiry   time.Duration
	index    map[string]*entry
	queue    []*entry
	fs       *filestore
	stop     chan struct{}
	stopOnce sync.Once
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
		stop:   make(chan struct{}),
	}

	dontpanic.GoForever(1*time.Minute, c.clean)

	return c, nil
}

// Stop stops the cleanup goroutines.
func (c *Cache) Stop() {
	c.stopOnce.Do(func() {
		c.fs.Stop()
		close(c.stop)
	})
}

func (c *Cache) clean() {
	sleepLoop(c.stop, c.expiry, func() {
		c.m.Lock()
		defer c.m.Unlock()

		cutoff := time.Now().Add(-c.expiry)
		for len(c.queue) > 0 && c.queue[0].created.Before(cutoff) {
			c.delete(c.queue[0])
			c.queue = c.queue[1:]
		}
	})
}

func sleepLoop(done chan struct{}, period time.Duration, fn func()) {
	sleep := time.Minute
	if 0 < period && period < sleep {
		sleep = period
	}

	for {
		time.Sleep(sleep)

		select {
		case <-done:
			return
		default:
		}

		fn()
	}
}

// FindOrCreate finds or creates a cache entry. If the create callback
// runs, it will be asynchronous and created is set to true.
func (c *Cache) FindOrCreate(key string, create func(io.Writer) error) (r io.ReadCloser, created bool, err error) {
	c.m.Lock()
	defer c.m.Unlock()

	if e := c.index[key]; e != nil {
		if r, err := e.Open(); err == nil {
			return r, false, nil
		}
		c.delete(e) // e is bad
	}

	r, e, err := c.newEntry(key, create)
	if err != nil {
		return nil, false, err
	}

	c.index[key] = e
	c.queue = append(c.queue, e)

	return r, true, nil
}

func (c *Cache) delete(e *entry) {
	if c.index[e.key] == e {
		delete(c.index, e.key)
	}
}

type entry struct {
	key     string
	c       *Cache
	p       *pipe
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
	pr, p, err := newPipe(f.Name(), f)
	if err != nil {
		return nil, nil, err
	}

	e.p = p

	go panicToErr(func() (err error) {
		defer p.Close()
		defer func() {
			if err != nil {
				log.WithError(err).Error("create streamcache entry")
				c.m.Lock()
				defer c.m.Unlock()
				c.delete(e)
			}
		}()

		if err := create(p); err != nil {
			return err
		}
		return p.Close()
	})

	return pr, e, nil
}

func (e *entry) Open() (io.ReadCloser, error) { return e.p.OpenReader() }

func panicToErr(f func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()

	return f()
}
