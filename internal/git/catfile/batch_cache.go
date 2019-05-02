package catfile

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/golang/groupcache/lru"
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
)

const (
	// CacheFeatureFlagKey is the feature flag key for catfile batch caching. This should match
	// what is in gitlab-ce
	CacheFeatureFlagKey = "catfile-cache"
	// CacheMaxItems is the default configuration for maximum entries in the batch cache
	CacheMaxItems = 100
)

var catfileCacheMembers = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: "gitaly_catfile_cache_members",
		Help: "Gauge of catfile cache members",
	},
)

var cache *BatchCache

func init() {
	prometheus.MustRegister(catfileCacheMembers)
	cache = NewCache(CacheMaxItems)
}

// CacheKey is a key for the catfile cache
type CacheKey struct {
	sessionID   string
	repoStorage string
	repoRelPath string
	repoObjDir  string
	repoAltDir  string
}

// CacheItem is a wrapper around Batch that provides a channel
// through which the ttl goroutine can be stopped
type CacheItem struct {
	batch                *Batch
	stopTTL              chan struct{}
	preserveBatchOnEvict bool
}

// ExpireAll is used to expire all of the batches in the cache
func ExpireAll() {
	cache.Lock()
	defer cache.Unlock()
	cache.lru.Clear()
}

// BatchCache is a cache containing batch objects based on session id and repository path
type BatchCache struct {
	sync.Mutex
	lru *lru.Cache
}

func closeBatchAndStopTTL(key lru.Key, value interface{}) {
	cacheItem := value.(*CacheItem)
	close(cacheItem.stopTTL)

	if !cacheItem.preserveBatchOnEvict {
		cacheItem.batch.Close()
	}
}

// NewCache creates a new BatchCache
func NewCache(maxEntries int) *BatchCache {
	lruCache := lru.New(maxEntries)
	lruCache.OnEvicted = closeBatchAndStopTTL

	return &BatchCache{
		lru: lruCache,
	}
}

// Get retrieves a batch based on a CacheKey. We remove it from the lru so that other processes can't Get it.
// however, since OnEvicted is called every time something is evicted from the cache, we need to signal to the OnEvicted
// function that we don't want this batch to be closed. Therefore, we will update the cache entry with preserveBatchOnEvict set
// to true.
func (bc *BatchCache) Get(key CacheKey) *Batch {
	bc.Lock()
	defer bc.Unlock()

	catfileCacheMembers.Set(float64(bc.lru.Len()))

	v, ok := bc.lru.Get(key)
	if !ok {
		return nil
	}

	cacheItem := v.(*CacheItem)

	// set preserveBatchOnEvict=true so that OnEvict doesn't close the batch
	cacheItem.preserveBatchOnEvict = true
	bc.lru.Add(key, cacheItem)

	bc.lru.Remove(key)

	return cacheItem.batch
}

// Add Adds a batch based on a CacheKey. If there is already a batch for the given
// key, it will remove and close the existing one, and Add the new one.
func (bc *BatchCache) Add(key CacheKey, b *Batch, ttl time.Duration) {
	bc.Lock()
	defer bc.Unlock()

	if v, ok := bc.lru.Get(key); ok {
		existing := v.(*CacheItem)
		existing.batch.Close()
		bc.lru.Remove(key)
	}

	stopTTL := make(chan struct{})

	bc.lru.Add(key, &CacheItem{
		batch:   b,
		stopTTL: stopTTL,
	})

	go func() {
		timer := time.NewTimer(ttl)

		select {
		case <-timer.C:
			bc.Del(key)
		// stopTTL channel is closed when the item is taken out of the cache so we don't have leaked goroutines
		case <-stopTTL:
			timer.Stop()
		}
	}()
}

// Del Deletes a batch based on a CacheKey
func (bc *BatchCache) Del(key CacheKey) {
	bc.Lock()
	defer bc.Unlock()

	bc.lru.Remove(key)
}

// returnToCache returns the batch to the cache
func (bc *BatchCache) returnToCache(ctx context.Context, cacheKey CacheKey, b *Batch) {
	<-ctx.Done()

	if b == nil || b.isClosed() {
		return
	}

	if b.HasUnreadData() {
		b.Close()
		return
	}
	bc.Add(cacheKey, b, DefaultBatchfileTTL)

}

// NewCacheKey return a cache key based on a session id and a git repository
func NewCacheKey(sessionID string, repo repository.GitRepo) CacheKey {
	return CacheKey{
		sessionID:   sessionID,
		repoStorage: repo.GetStorageName(),
		repoRelPath: repo.GetRelativePath(),
		repoObjDir:  repo.GetGitObjectDirectory(),
		repoAltDir:  strings.Join(repo.GetGitAlternateObjectDirectories(), ","),
	}
}
