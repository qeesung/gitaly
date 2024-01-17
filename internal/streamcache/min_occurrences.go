package streamcache

import (
	"context"
	"io"
	"sync"
	"time"
)

// minOccurrences is a streamcache middleware. Its intended use is to
// prevent storing unique keys in the cache. If a key is retrieved only
// once, the cache is not helping, and it costs us disk IO to store an
// entry in the cache. By keeping a counter per key minOccurrences can
// prevent keys from reaching the "real" cache until their count is high
// enough.
type minOccurrences struct {
	Cache
	N      int           // Minimum occurrences before a key passes to real Cache
	MinAge time.Duration // Minimum time to remember a key

	// We must garbage-collect our counters periodically to prevent unbounded
	// memory growth. To efficiently drop many counters we drop an entire
	// map. To prevent resetting all counters and creating a wave of cache
	// misses, use an "old" and "new" generation of counters. Frequently used
	// keys will migrate from "old" to "new" and thereby will not get reset.
	m         sync.Mutex
	oldCount  map[string]int
	newCount  map[string]int
	rotatedAt time.Time

	null NullCache
}

func (mo *minOccurrences) Fetch(ctx context.Context, key string, dst io.Writer, create func(io.Writer) error) (written int64, created bool, err error) {
	if mo.incr(key) > mo.N {
		return mo.Cache.Fetch(ctx, key, dst, create)
	}
	return mo.null.Fetch(ctx, key, dst, create)
}

func (mo *minOccurrences) incr(key string) int {
	mo.m.Lock()
	defer mo.m.Unlock()

	// Keys live longer than MinAge and that is OK. We just need to make sure
	// they get garbage-collected eventually, and that they live at least as
	// long as the underlying cache expiry time.
	if now := time.Now(); now.Sub(mo.rotatedAt) > mo.MinAge {
		mo.rotatedAt = now
		mo.oldCount = mo.newCount // This causes mo.oldCount to get garbage-collected
		mo.newCount = nil
	}

	if mo.newCount == nil {
		mo.newCount = make(map[string]int, len(mo.oldCount))
	}

	mo.newCount[key]++
	if old := mo.oldCount[key]; old > 0 {
		mo.newCount[key] += old
		delete(mo.oldCount, key)
	}

	return mo.newCount[key]
}
