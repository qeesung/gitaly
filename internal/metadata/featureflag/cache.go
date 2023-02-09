package featureflag

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/tick"
)

// Cache is an abstraction over feature flag management.
type Cache interface {
	// Get returns true/false as a first return parameter is the feature flag is
	// enabled/disabled. The second return parameter is true only if the feature
	// flag was evaluated and false if it is not know (not in the Cache).
	Get(context.Context, string) (bool, bool)
}

// NoopCache is a dummy implementation of the FlagCache that is used as a stub.
type NoopCache struct{}

// Get always returns the same result (false, false) to the caller.
func (NoopCache) Get(context.Context, string) (bool, bool) {
	return false, false
}

var (
	// flagCache is a storage of the feature flags evaluated somewhere and
	// used to prevent potential performance troubles with triggering
	// feature flag fetching too much often from the Provider.
	flagCache    Cache = NoopCache{}
	flagCacheMtx sync.Mutex
)

// SetCache sets a Cache for the feature flags.
func SetCache(new Cache) {
	flagCacheMtx.Lock()
	defer flagCacheMtx.Unlock()
	flagCache = new
}

// GetCache returns current Cache of the feature flags.
func GetCache() Cache {
	flagCacheMtx.Lock()
	defer flagCacheMtx.Unlock()
	return flagCache
}

// Provider is an abstraction that is able to return a set of feature flags
// in their current state.
type Provider interface {
	// GetAll returns all known feature flags and their state.
	GetAll(ctx context.Context) (map[string]bool, error)
}

// RefreshableCache is a periodically refreshable cache for storing feature flags.
// To start auto-refresh the RefreshLoop method needs to be called.
type RefreshableCache struct {
	mtx      sync.RWMutex
	logger   logrus.FieldLogger
	flags    map[string]bool
	provider Provider
}

// NewRefreshableCache returns a new instance of the RefreshableCache that is already initialized.
func NewRefreshableCache(
	ctx context.Context,
	logger logrus.FieldLogger,
	provider Provider,
) *RefreshableCache {
	c := &RefreshableCache{logger: logger, provider: provider}
	c.refresh(ctx)
	return c
}

// RefreshLoop is a blocking call that returns once passed in context is cancelled.
// It continuously reloads cache data. If data retrieval from the Provider returns
// an error the cache data remains the same without any changes.
func (rc *RefreshableCache) RefreshLoop(ctx context.Context, ticker tick.Ticker) {
	ticker.Reset()
	for {
		select {
		case <-ticker.C():
			rc.refresh(ctx)
			ticker.Reset()
		case <-ctx.Done():
			return
		}
	}
}

// Get returns current cached value for the feature flag and true as a second return parameter.
// If flag is not found the both return values are false.
func (rc *RefreshableCache) Get(_ context.Context, name string) (bool, bool) {
	rc.mtx.RLock()
	defer rc.mtx.RUnlock()
	val, found := rc.flags[name]
	return val, found
}

func (rc *RefreshableCache) refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	newFlags, err := rc.provider.GetAll(ctx)
	if err != nil {
		rc.logger.Errorf("failure on fetching the state of the feature flags: %v", err)
		// In case of an issue with flags retrieval, proceed without updating cached data.
		return
	}

	flagsClone := make(map[string]bool, len(newFlags))
	for k, v := range newFlags {
		flagsClone[k] = v
	}

	rc.mtx.Lock()
	defer rc.mtx.Unlock()
	rc.flags = flagsClone
}
