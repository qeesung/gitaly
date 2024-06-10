// Package unarycache allows you to cache responses for unary gRPC messages.
//
// Some gRPC unary message take a considerable amount of compute to build the
// response. This package will allow users to cache this response.
//
// For the time being it is implemented using a simple in-memory
// least-recently-used cache.
package unarycache

import (
	"context"
	"fmt"

	lru "github.com/hashicorp/golang-lru/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
)

// Generator is a function that computes a cacheable value for a repository based on the given key.
// The key must uniquely identify the result. A valid choice would for example be an object ID.
type Generator[Key comparable, Value any] func(context.Context, *localrepo.Repo, Key) (Value, error)

// cacheKey is a repository-scoped key for the cache.
type cacheKey[Key comparable] struct {
	// repoPath is the path of the repository for which a cache entry is active.
	repoPath string
	// key is the key that uniquely identifies a result.
	key Key
}

// Cache is a cache that stores values that can be uniquely computed for a specific key.
type Cache[Key comparable, Value any] struct {
	// generator is the generator function that computes the value for a given key if the key is
	// not yet cached.
	generator Generator[Key, Value]
	// lru is the least-recently-used cache that stores cached values. The cache keys are scoped
	// to the repository and the respective key that uniquely identifies the result.
	lru *lru.Cache[cacheKey[Key], Value]
}

// New creates a new Cache. The cache will hold at maximum `maxEntries`, if more than this many
// entries are added then the least-recently-used one will be evicted from the cache. If a
// repository-scoped key does not exist in the cache, then the generator function will be called to
// compute the value for the given repository and key.
func New[Key comparable, Value any](maxEntries int, generator Generator[Key, Value]) (*Cache[Key, Value], error) {
	lru, err := lru.New[cacheKey[Key], Value](maxEntries)
	if err != nil {
		return nil, fmt.Errorf("creating LRU cache: %w", err)
	}

	return &Cache[Key, Value]{
		generator: generator,
		lru:       lru,
	}, nil
}

// GetOrCompute either returns a cached value or computes the value for the given repository and
// key.
func (c *Cache[Key, Value]) GetOrCompute(ctx context.Context, repo *localrepo.Repo, key Key) (Value, error) {
	var defaultValue Value

	repoPath, err := repo.Path(ctx)
	if err != nil {
		return defaultValue, fmt.Errorf("getting repo path: %w", err)
	}

	repoScopedKey := cacheKey[Key]{
		repoPath: repoPath,
		key:      key,
	}

	if value, ok := c.lru.Get(repoScopedKey); ok {
		return value, nil
	}

	value, err := c.generator(ctx, repo, key)
	if err != nil {
		return defaultValue, err
	}

	c.lru.Add(repoScopedKey, value)

	return value, nil
}
