package catfile

import (
	"container/list"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
)

const (
	// DefaultBatchfileTTL is the default ttl for batch files to live in the cache
	DefaultBatchfileTTL = 10 * time.Second

	// CacheMaxItems is the default configuration for maximum entries in the batch cache
	CacheMaxItems = 100

	defaultEvictionInterval = 1 * time.Second
)

var catfileCacheMembers = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: "gitaly_catfile_cache_members",
		Help: "Gauge of catfile cache members",
	},
)

var cache *batchCache

func init() {
	prometheus.MustRegister(catfileCacheMembers)
	cache = newCache(DefaultBatchfileTTL, CacheMaxItems)
}

func newCacheKey(sessionID string, repo repository.GitRepo) key {
	return key{
		sessionID:   sessionID,
		repoStorage: repo.GetStorageName(),
		repoRelPath: repo.GetRelativePath(),
		repoObjDir:  repo.GetGitObjectDirectory(),
		repoAltDir:  strings.Join(repo.GetGitAlternateObjectDirectories(), ","),
	}
}

type key struct {
	sessionID   string
	repoStorage string
	repoRelPath string
	repoObjDir  string
	repoAltDir  string
}

type entry struct {
	key
	value  *Batch
	expiry time.Time
}

// batchCache is a doubly linked list with extras. Each entry has a
// unique key. We have a map to be able to look up entries by key
// directly, avoiding a full list traversal. Entries always get added to
// the back of the list. If the list gets too long, we evict entries from
// the front of the list. When an entry gets added it gets an expiry time
// based on a fixed TTL. A monitor goroutine periodically evicts expired
// entries.
type batchCache struct {
	// keyMap lets us look up entries by key
	keyMap map[key]*list.Element

	ll *list.List

	sync.Mutex

	// maxLen is the maximum number of keys in the cache
	maxLen int

	// ttl is the fixed ttl for cache entries
	ttl time.Duration

	// done is used to shut down the ttl eviction goroutine
	done chan struct{}
}

func newCache(ttl time.Duration, maxLen int) *batchCache {
	return newCacheRefresh(ttl, maxLen, defaultEvictionInterval)
}

func newCacheRefresh(ttl time.Duration, maxLen int, refresh time.Duration) *batchCache {
	bc := &batchCache{
		keyMap: make(map[key]*list.Element),
		ll:     list.New(),
		maxLen: maxLen,
		ttl:    ttl,
		done:   make(chan struct{}),
	}

	go bc.monitor(refresh)
	return bc
}

func (bc *batchCache) monitor(interval time.Duration) {
	ticker := time.NewTicker(interval)

	for {
		select {
		case <-ticker.C:
			bc.EnforceTTL(time.Now())
		case <-bc.done:
			ticker.Stop()
			return
		}
	}
}

// Add adds a key, value pair to bc. If there are too many keys in bc
// already Add will evict old keys until the length is OK again.
func (bc *batchCache) Add(k key, b *Batch) {
	bc.Lock()
	defer bc.Unlock()

	if _, ok := bc.keyMap[k]; ok {
		catfileCacheCounter.WithLabelValues("duplicate").Inc()
		bc.delete(k, true)
	}

	ent := &entry{key: k, value: b, expiry: time.Now().Add(bc.ttl)}
	bc.keyMap[k] = bc.ll.PushBack(ent)

	for bc.len() > bc.maxLen {
		bc.evictOldest()
	}

	catfileCacheMembers.Set(float64(bc.len()))
}

func (bc *batchCache) evictOldest() { bc.delete(bc.head().key, true) }
func (bc *batchCache) len() int     { return bc.ll.Len() }
func (bc *batchCache) head() *entry { return bc.ll.Front().Value.(*entry) }

// Checkout removes a value from bc. After use the caller can re-add the value with bc.Add.
func (bc *batchCache) Checkout(k key) (*Batch, bool) {
	bc.Lock()
	defer bc.Unlock()

	e, ok := bc.keyMap[k]
	if !ok {
		catfileCacheCounter.WithLabelValues("miss").Inc()
		return nil, false
	}

	catfileCacheCounter.WithLabelValues("hit").Inc()

	ent := e.Value.(*entry)
	bc.delete(ent.key, false)
	return ent.value, true
}

// EnforceTTL evicts all keys older than now.
func (bc *batchCache) EnforceTTL(now time.Time) {
	for {
		bc.Lock()

		if bc.len() == 0 {
			bc.Unlock()
			return
		}

		if now.Before(bc.head().expiry) {
			bc.Unlock()
			return
		}

		bc.evictOldest()
		bc.Unlock()
	}
}

func (bc *batchCache) EvictAll() {
	bc.Lock()
	defer bc.Unlock()

	close(bc.done)
	for bc.len() > 0 {
		bc.evictOldest()
	}
}

func (bc *batchCache) delete(k key, wantClose bool) {
	e, ok := bc.keyMap[k]
	if !ok {
		return
	}

	if wantClose {
		e.Value.(*entry).value.Close()
	}

	bc.ll.Remove(e)
	delete(bc.keyMap, k)
	catfileCacheMembers.Set(float64(bc.len()))
}

// ExpireAll is used to expire all of the batches in the cache
func ExpireAll() {
	cache.EvictAll()
}
