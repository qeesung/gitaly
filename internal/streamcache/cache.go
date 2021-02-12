// Package streamcache provides a cache for large blobs (in the order of
// gigabytes). Because storing gigabytes of data is slow, cache entries
// can be streamed on the read end before they have finished on the write
// end. Because storing gigabytes of data is expensive, cache entries
// have a back pressure mechanism: if the readers don't make progress
// reading the data, the writers will block. That way our disk can fill
// up no faster than our readers can read from the cache.
//
// The cache has 3 main parts: Cache (in-memory index), filestore (files
// to store the cached data in because it does not fit in memory), and
// pipe (coordinated IO to one file between one writer and multiple
// readers). A cache entry consists of a key, an expiration time, a
// pipe and the error result of the thing writing to the pipe.
//
// Expiry
//
// There are two expiry goroutines: one for Cache and one for filestore.
// The Cache expiry goroutine expires entry after a set amount of time,
// and deletes their underlying files too. This is safe because Unix file
// semantics guarantee that readers/writers that are still using those
// files can keep using them. In addition to expiring known cache
// entries, we also have a goroutine at the filestore level which
// performs a directory walk. This will clean up cache files left behind
// by other processes.
//
package streamcache

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/dontpanic"
	"gitlab.com/gitlab-org/labkit/log"
)

// Cache is a cache for large byte streams.
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

		var removed []*entry
		cutoff := time.Now().Add(-c.expiry)
		for len(c.queue) > 0 && c.queue[0].created.Before(cutoff) {
			e := c.queue[0]
			c.delete(e)
			removed = append(removed, e)
			c.queue = c.queue[1:]
		}

		// Batch together file removals in a goroutine, without holding the mutex
		go func() {
			for _, e := range removed {
				_ = e.p.Remove()
			}
		}()
	})
}

// FindOrCreate finds or creates a cache entry. If the create callback
// runs, it will be asynchronous and created is set to true. Callers must
// Close() the returned stream to free underlying resources.
func (c *Cache) FindOrCreate(key string, create func(io.Writer) error) (s *Stream, created bool, err error) {
	c.m.Lock()
	defer c.m.Unlock()

	if e := c.index[key]; e != nil {
		if s, err := e.Open(); err == nil {
			return s, false, nil
		}
		c.delete(e) // e is bad
	}

	s, e, err := c.newEntry(key, create)
	if err != nil {
		return nil, false, err
	}

	c.index[key] = e
	c.queue = append(c.queue, e)

	return s, true, nil
}

// delete must be called while holding the mutex.
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
	w       *waiter
}

// Stream abstracts a stream of bytes (via Read()) plus an error (via
// Wait()). Callers must always call Close() to prevent resource leaks.
type Stream struct {
	w *waiter
	r io.ReadCloser
}

// Wait returns the error value of the Stream. If ctx is canceled,
// Wait unblocks and returns early.
func (s *Stream) Wait(ctx context.Context) error { return s.w.Wait(ctx) }

// Read reads from the underlying stream of the stream.
func (s *Stream) Read(p []byte) (int, error) { return s.r.Read(p) }

// Close releases the underlying resources of the stream.
func (s *Stream) Close() error { return s.r.Close() }

func (c *Cache) newEntry(key string, create func(io.Writer) error) (*Stream, *entry, error) {
	e := &entry{
		key:     key,
		c:       c,
		created: time.Now(),
		w:       newWaiter(),
	}

	f, err := c.fs.Create()
	if err != nil {
		return nil, nil, err
	}

	var pr io.ReadCloser
	pr, e.p, err = newPipe(f)
	if err != nil {
		return nil, nil, err
	}

	go func() {
		err := runCreate(e.p, create)
		e.w.SetError(err)
		if err != nil {
			log.WithError(err).Error("create cache entry")
			c.m.Lock()
			defer c.m.Unlock()
			c.delete(e)
		}
	}()

	return e.wrapReadCloser(pr), e, nil
}

func (e *entry) wrapReadCloser(r io.ReadCloser) *Stream {
	return &Stream{r: r, w: e.w}
}

func runCreate(w io.WriteCloser, create func(io.Writer) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()

	defer w.Close()
	if err := create(w); err != nil {
		return err
	}
	return w.Close()
}

func (e *entry) Open() (*Stream, error) {
	r, err := e.p.OpenReader()
	return e.wrapReadCloser(r), err
}

type waiter struct {
	done chan struct{}
	err  error
	once sync.Once
}

func newWaiter() *waiter { return &waiter{done: make(chan struct{})} }

func (w *waiter) SetError(err error) {
	w.once.Do(func() {
		w.err = err
		close(w.done)
	})
}

func (w *waiter) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.done:
		return w.err
	}
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
