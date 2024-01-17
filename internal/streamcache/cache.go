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
// readers). A cache entry consists of a key, an maximum age, a
// pipe and the error result of the thing writing to the pipe.
//
// # Eviction
//
// There are two eviction goroutines: one for Cache and one for filestore.
// The Cache eviction goroutine evicts entries after a set amount of time,
// and deletes their underlying files too. This is safe because Unix file
// semantics guarantee that readers/writers that are still using those
// files can keep using them. In addition to evicting known cache
// entries, we also have a goroutine at the filestore level which
// performs a directory walk. This will clean up cache files left behind
// by other processes.
package streamcache

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gitlab.com/gitlab-org/gitaly/v16/internal/dontpanic"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

var (
	cacheIndexSize = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gitaly_streamcache_index_entries",
			Help: "Number of index entries in streamcache",
		},
		[]string{"dir"},
	)

	packObjectsCacheEnabled = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gitaly_pack_objects_cache_enabled",
			Help: "If set to 1, indicates that the cache for PackObjectsHook has been enabled in this process",
		},
		[]string{"dir", "max_age"},
	)
)

// Cache is a cache for large byte streams.
type Cache interface {
	// Fetch finds or creates a cache entry and writes its contents into dst.
	// If the create callback is called the created return value is true. In
	// case of a non-nil error return, the create callback may still be
	// running in a goroutine for the benefit of another caller of Fetch with
	// the same key.
	Fetch(ctx context.Context, key string, dst io.Writer, create func(io.Writer) error) (written int64, created bool, err error)
	// Stop stops the cleanup goroutines of the cache.
	Stop()
}

var _ = Cache(&TestLoggingCache{})

// TestLogEntry records the result of a cache lookup for testing purposes.
type TestLogEntry struct {
	Key     string
	Created bool
	Err     error
}

// TestLoggingCache wraps a real Cache and logs all its lookups. This is
// not suitable for production because the log will grow indefinitely.
// Use only for testing.
type TestLoggingCache struct {
	Cache
	entries []*TestLogEntry
	m       sync.Mutex
}

// Fetch calls the underlying Fetch method and logs the
// result.
func (tlc *TestLoggingCache) Fetch(ctx context.Context, key string, dst io.Writer, create func(io.Writer) error) (written int64, created bool, err error) {
	written, created, err = tlc.Cache.Fetch(ctx, key, dst, create)

	tlc.m.Lock()
	defer tlc.m.Unlock()
	tlc.entries = append(tlc.entries, &TestLogEntry{Key: key, Created: created, Err: err})
	return written, created, err
}

// Entries returns a reference to the log of entries observed so far.
// This is a reference so the caller should not modify the underlying
// array or its elements.
func (tlc *TestLoggingCache) Entries() []*TestLogEntry {
	tlc.m.Lock()
	defer tlc.m.Unlock()
	return tlc.entries
}

var _ = Cache(NullCache{})

// NullCache is a null implementation of Cache. Every lookup is a miss,
// and it uses no storage.
type NullCache struct{}

// Fetch runs create(dst). The created flag is always true.
func (NullCache) Fetch(ctx context.Context, key string, dst io.Writer, create func(io.Writer) error) (written int64, created bool, err error) {
	w := &helper.CountingWriter{W: dst}
	err = create(w)
	return w.N, true, err
}

// Stop is a no-op.
func (NullCache) Stop() {}

type cache struct {
	m          sync.Mutex
	maxAge     time.Duration
	index      map[string]*entry
	createFile func() (namedWriteCloser, error)
	stop       chan struct{}
	stopOnce   sync.Once
	logger     log.Logger
	dir        string
	sleepLoop  *dontpanic.Forever

	// removalCond is a condition that gets signalled after files have been removed from disk.
	// This field is optional and should only be used for tests.
	removalCond *sync.Cond
}

// New returns a new cache instance.
func New(cfg config.StreamCacheConfig, logger log.Logger) Cache {
	if cfg.Enabled {
		packObjectsCacheEnabled.WithLabelValues(
			cfg.Dir,
			strconv.Itoa(int(cfg.MaxAge.Duration().Seconds())),
		).Set(1)

		maxAge := cfg.MaxAge.Duration()
		return &minOccurrences{
			N:      cfg.MinOccurrences,
			MinAge: maxAge,
			Cache:  newCacheWithSleep(cfg.Dir, maxAge, time.After, time.After, logger),
		}
	}

	return NullCache{}
}

func newCacheWithSleep(
	dir string,
	maxAge time.Duration,
	filestoreSleep func(time.Duration) <-chan time.Time,
	cleanSleep func(time.Duration) <-chan time.Time,
	logger log.Logger,
) *cache {
	fs := newFilestore(dir, maxAge, filestoreSleep, logger)

	c := &cache{
		maxAge:     maxAge,
		index:      make(map[string]*entry),
		createFile: fs.Create,
		stop:       make(chan struct{}),
		logger:     logger,
		dir:        dir,
		sleepLoop:  dontpanic.NewForever(logger, time.Minute),
	}

	c.sleepLoop.Go(func() {
		sleepLoop(c.stop, c.maxAge, cleanSleep, c.clean)
	})
	go func() {
		<-c.stop
		c.sleepLoop.Cancel()
		fs.Stop()
	}()

	return c
}

func (c *cache) Stop() {
	c.stopOnce.Do(func() { close(c.stop) })
}

func (c *cache) clean() {
	c.m.Lock()
	defer c.m.Unlock()

	var removed []*entry
	cutoff := time.Now().Add(-c.maxAge)
	for k, e := range c.index {
		if e.created.Before(cutoff) {
			c.delete(k)
			removed = append(removed, e)
		}
	}

	// Batch together file removals in a goroutine, without holding the mutex
	go func() {
		for _, e := range removed {
			if err := e.pipe.RemoveFile(); err != nil && !os.IsNotExist(err) {
				c.logger.WithError(err).Error("streamcache: remove file evicted from index")
			}
		}

		if c.removalCond != nil {
			c.removalCond.L.Lock()
			defer c.removalCond.L.Unlock()
			c.removalCond.Broadcast()
		}
	}()
}

func (c *cache) delete(key string) {
	delete(c.index, key)
	c.setIndexSize()
}

func (c *cache) setIndexSize() {
	cacheIndexSize.WithLabelValues(c.dir).Set(float64(len(c.index)))
}

func (c *cache) Fetch(ctx context.Context, key string, dst io.Writer, create func(io.Writer) error) (written int64, created bool, err error) {
	var (
		rc io.ReadCloser
		wt *waiter
	)
	rc, wt, created, err = c.getStream(key, create)
	if err != nil {
		return
	}
	defer rc.Close()

	written, err = io.Copy(dst, rc)
	if err != nil {
		return
	}

	err = wt.Wait(ctx)
	return
}

func (c *cache) getStream(key string, create func(io.Writer) error) (_ io.ReadCloser, _ *waiter, created bool, err error) {
	c.m.Lock()
	defer c.m.Unlock()

	if e := c.index[key]; e != nil {
		if r, err := e.pipe.OpenReader(); err == nil {
			return r, e.waiter, false, nil
		}

		// In this case err != nil. That is allowed to happen, for instance if
		// the *filestore cleanup goroutine deleted the file already. But let's
		// remove the key from the cache to save the next caller the effort of
		// trying to open this entry.
		c.delete(key)
	}

	r, e, err := c.newEntry(key, create)
	if err != nil {
		return nil, nil, false, err
	}

	c.index[key] = e
	c.setIndexSize()

	return r, e.waiter, true, nil
}

type entry struct {
	key     string
	cache   *cache
	pipe    *pipe
	created time.Time
	waiter  *waiter
}

func (c *cache) newEntry(key string, create func(io.Writer) error) (_ io.ReadCloser, _ *entry, err error) {
	e := &entry{
		key:     key,
		cache:   c,
		created: time.Now(),
		waiter:  newWaiter(),
	}

	// Every entry gets a unique underlying file. We do not want to reuse
	// existing cache files because we do not know whether they are the
	// result of a successful call to create.
	//
	// This may sound like we should be using an anonymous tempfile, but that
	// would be at odds with the requirement to be able to open and close
	// multiple instances of the file independently: one for the writer, and
	// one for each reader.
	//
	// So the name of the file is irrelevant, but the file must have _a_
	// name.
	f, err := c.createFile()
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			f.Close()
		}
	}()

	var pr io.ReadCloser
	pr, e.pipe, err = newPipe(f)
	if err != nil {
		return nil, nil, err
	}

	go func() {
		err := runCreate(e.pipe, create)

		// We defer this until after we have removed the cache entry so that the waiter is
		// only unblocked when the cache key has already been pruned from the cache.
		defer e.waiter.SetError(err)

		if err != nil {
			c.logger.WithError(err).Error("create cache entry")
			c.m.Lock()
			defer c.m.Unlock()
			c.delete(key)
		}
	}()

	return pr, e, nil
}

func runCreate(w io.WriteCloser, create func(io.Writer) error) (err error) {
	// Catch panics because this function runs in a goroutine. That means that
	// unlike RPC handlers, which are guarded by a panic catching middleware,
	// an uncaught panic can crash the whole process.
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()

	defer w.Close()

	if err := create(w); err != nil {
		return err
	}

	if err := w.Close(); err != nil {
		return err
	}

	return nil
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

func sleepLoop(done chan struct{}, period time.Duration, sleep func(time.Duration) <-chan time.Time, callback func()) {
	const maxPeriod = time.Minute
	if period <= 0 || period >= maxPeriod {
		period = maxPeriod
	}

	for {
		select {
		case <-done:
			return
		case <-sleep(period):
		}

		callback()
	}
}
