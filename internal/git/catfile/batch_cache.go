package catfile

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/metadata"
	"gitlab.com/gitlab-org/labkit/correlation"
)

const (
	// defaultBatchfileTTL is the default ttl for batch files to live in the cache
	defaultBatchfileTTL = 10 * time.Second

	defaultEvictionInterval = 1 * time.Second

	// The default maximum number of cache entries
	defaultMaxLen = 100
)

// Cache is a cache for git-cat-file(1) processes.
type Cache interface {
	// BatchProcess either creates a new git-cat-file(1) process or returns a cached one for
	// the given repository.
	BatchProcess(context.Context, git.RepositoryExecutor) (Batch, error)
	// Evict evicts all cached processes from the cache.
	Evict()
}

func newCacheKey(sessionID string, repo repository.GitRepo) (key, bool) {
	if sessionID == "" {
		return key{}, false
	}

	return key{
		sessionID:   sessionID,
		repoStorage: repo.GetStorageName(),
		repoRelPath: repo.GetRelativePath(),
		repoObjDir:  repo.GetGitObjectDirectory(),
		repoAltDir:  strings.Join(repo.GetGitAlternateObjectDirectories(), ","),
	}, true
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
	value  *batch
	expiry time.Time
}

// BatchCache entries always get added to the back of the list. If the
// list gets too long, we evict entries from the front of the list. When
// an entry gets added it gets an expiry time based on a fixed TTL. A
// monitor goroutine periodically evicts expired entries.
type BatchCache struct {
	// maxLen is the maximum number of keys in the cache
	maxLen int
	// ttl is the fixed ttl for cache entries
	ttl time.Duration
	// monitorTicker is the ticker used for the monitoring Goroutine.
	monitorTicker *time.Ticker
	monitorDone   chan interface{}

	catfileCacheCounter     *prometheus.CounterVec
	currentCatfileProcesses prometheus.Gauge
	totalCatfileProcesses   prometheus.Counter
	catfileLookupCounter    *prometheus.CounterVec
	catfileCacheMembers     prometheus.Gauge

	entriesMutex sync.Mutex
	entries      []*entry

	// cachedProcessDone is a condition that gets signalled whenever a process is being
	// considered to be returned to the cache. This field is optional and must only be used in
	// tests.
	cachedProcessDone *sync.Cond
}

// NewCache creates a new catfile process cache.
func NewCache(cfg config.Cfg) *BatchCache {
	return newCache(defaultBatchfileTTL, cfg.Git.CatfileCacheSize, defaultEvictionInterval)
}

func newCache(ttl time.Duration, maxLen int, refreshInterval time.Duration) *BatchCache {
	if maxLen <= 0 {
		maxLen = defaultMaxLen
	}

	bc := &BatchCache{
		maxLen: maxLen,
		ttl:    ttl,
		catfileCacheCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_catfile_cache_total",
				Help: "Counter of catfile cache hit/miss",
			},
			[]string{"type"},
		),
		currentCatfileProcesses: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "gitaly_catfile_processes",
				Help: "Gauge of active catfile processes",
			},
		),
		totalCatfileProcesses: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "gitaly_catfile_processes_total",
				Help: "Counter of catfile processes",
			},
		),
		catfileLookupCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_catfile_lookups_total",
				Help: "Git catfile lookups by object type",
			},
			[]string{"type"},
		),
		catfileCacheMembers: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "gitaly_catfile_cache_members",
				Help: "Gauge of catfile cache members",
			},
		),
		monitorTicker: time.NewTicker(refreshInterval),
		monitorDone:   make(chan interface{}),
	}

	go bc.monitor()
	return bc
}

// Describe describes all metrics exposed by BatchCache.
func (bc *BatchCache) Describe(descs chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(bc, descs)
}

// Collect collects all metrics exposed by BatchCache.
func (bc *BatchCache) Collect(metrics chan<- prometheus.Metric) {
	bc.catfileCacheCounter.Collect(metrics)
	bc.currentCatfileProcesses.Collect(metrics)
	bc.totalCatfileProcesses.Collect(metrics)
	bc.catfileLookupCounter.Collect(metrics)
	bc.catfileCacheMembers.Collect(metrics)
}

func (bc *BatchCache) monitor() {
	for {
		select {
		case <-bc.monitorTicker.C:
			bc.enforceTTL(time.Now())
		case <-bc.monitorDone:
			close(bc.monitorDone)
			return
		}
	}
}

// Stop stops the monitoring Goroutine and evicts all cached processes. This must only be called
// once.
func (bc *BatchCache) Stop() {
	bc.monitorTicker.Stop()
	bc.monitorDone <- struct{}{}
	<-bc.monitorDone
	bc.Evict()
}

// BatchProcess creates a new Batch process for the given repository.
func (bc *BatchCache) BatchProcess(ctx context.Context, repo git.RepositoryExecutor) (_ Batch, returnedErr error) {
	requestDone := ctx.Done()
	if requestDone == nil {
		panic("empty ctx.Done() in catfile.Batch.New()")
	}

	cacheKey, isCacheable := newCacheKey(metadata.GetValue(ctx, SessionIDField), repo)
	if isCacheable {
		// We only try to look up cached batch processes in case it is cacheable, which
		// requires a session ID. This is mostly done such that git-cat-file(1) processes
		// from one user cannot interfer with those from another user. The main intent is to
		// disallow trivial denial of service attacks against other users in case it is
		// possible to poison the cache with broken git-cat-file(1) processes.

		if c, ok := bc.checkout(cacheKey); ok {
			go bc.returnWhenDone(requestDone, cacheKey, c)
			return c, nil
		}

		// We have not found any cached process, so we need to create a new one. In this
		// case, we need to detach the process from the current context such that it does
		// not get killed when the current context is done. Note that while we explicitly
		// `Close()` processes in case this function fails, we must have a cancellable
		// context or otherwise our `command` package will panic.
		var cancel func()
		ctx, cancel = context.WithCancel(context.Background())
		defer func() {
			if returnedErr != nil {
				cancel()
			}
		}()

		// We have to decorrelate the process from the current context given that it
		// may potentially be reused across different RPC calls.
		ctx = correlation.ContextWithCorrelation(ctx, "")
		ctx = opentracing.ContextWithSpan(ctx, nil)
	}

	c, err := newBatch(ctx, repo, bc.catfileLookupCounter)
	if err != nil {
		return nil, err
	}
	defer func() {
		// If we somehow fail after creating a new Batch process, then we want to kill
		// spawned processes right away.
		if returnedErr != nil {
			c.Close()
		}
	}()

	bc.totalCatfileProcesses.Inc()
	bc.currentCatfileProcesses.Inc()
	go func() {
		<-ctx.Done()
		bc.currentCatfileProcesses.Dec()
	}()

	if isCacheable {
		// If the process is cacheable, then we want to put the process into the cache when
		// the current outer context is done.
		go bc.returnWhenDone(requestDone, cacheKey, c)
	}

	return c, nil
}

func (bc *BatchCache) returnWhenDone(done <-chan struct{}, cacheKey key, c *batch) {
	<-done

	if bc.cachedProcessDone != nil {
		defer func() {
			bc.cachedProcessDone.Broadcast()
		}()
	}

	if c == nil || c.isClosed() {
		return
	}

	if c.hasUnreadData() {
		bc.catfileCacheCounter.WithLabelValues("dirty").Inc()
		c.Close()
		return
	}

	bc.add(cacheKey, c)
}

// add adds a key, value pair to bc. If there are too many keys in bc
// already add will evict old keys until the length is OK again.
func (bc *BatchCache) add(k key, b *batch) {
	bc.entriesMutex.Lock()
	defer bc.entriesMutex.Unlock()

	if i, ok := bc.lookup(k); ok {
		bc.catfileCacheCounter.WithLabelValues("duplicate").Inc()
		bc.delete(i, true)
	}

	ent := &entry{key: k, value: b, expiry: time.Now().Add(bc.ttl)}
	bc.entries = append(bc.entries, ent)

	for bc.len() > bc.maxLen {
		bc.evictHead()
	}

	bc.catfileCacheMembers.Set(float64(bc.len()))
}

func (bc *BatchCache) head() *entry { return bc.entries[0] }
func (bc *BatchCache) evictHead()   { bc.delete(0, true) }
func (bc *BatchCache) len() int     { return len(bc.entries) }

// checkout removes a value from bc. After use the caller can re-add the value with bc.Add.
func (bc *BatchCache) checkout(k key) (*batch, bool) {
	bc.entriesMutex.Lock()
	defer bc.entriesMutex.Unlock()

	i, ok := bc.lookup(k)
	if !ok {
		bc.catfileCacheCounter.WithLabelValues("miss").Inc()
		return nil, false
	}

	bc.catfileCacheCounter.WithLabelValues("hit").Inc()

	ent := bc.entries[i]
	bc.delete(i, false)
	return ent.value, true
}

// enforceTTL evicts all entries older than now, assuming the entry
// expiry times are increasing.
func (bc *BatchCache) enforceTTL(now time.Time) {
	bc.entriesMutex.Lock()
	defer bc.entriesMutex.Unlock()

	for bc.len() > 0 && now.After(bc.head().expiry) {
		bc.evictHead()
	}
}

// Evict evicts all cached processes from the cache.
func (bc *BatchCache) Evict() {
	bc.entriesMutex.Lock()
	defer bc.entriesMutex.Unlock()

	for bc.len() > 0 {
		bc.evictHead()
	}
}

func (bc *BatchCache) lookup(k key) (int, bool) {
	for i, ent := range bc.entries {
		if ent.key == k {
			return i, true
		}
	}

	return -1, false
}

func (bc *BatchCache) delete(i int, wantClose bool) {
	ent := bc.entries[i]

	if wantClose {
		ent.value.Close()
	}

	bc.entries = append(bc.entries[:i], bc.entries[i+1:]...)
	bc.catfileCacheMembers.Set(float64(bc.len()))
}
