package command

import (
	"context"
	"sync"

	"github.com/sirupsen/logrus"
)

type requestStatsKey struct{}

// Stats hosts a map of statistics.
type Stats struct {
	registry map[string]int
	sync.Mutex
}

// RecordSum adds the provided value to the value identified by key.
func (stats *Stats) RecordSum(key string, value int) {
	stats.Lock()
	defer stats.Unlock()

	if prevValue, ok := stats.registry[key]; ok {
		value += prevValue
	}

	stats.registry[key] = value
}

// RecordMax updates the given key with the new value if it's greater than the current value.
func (stats *Stats) RecordMax(key string, value int) {
	stats.Lock()
	defer stats.Unlock()

	if prevValue, ok := stats.registry[key]; ok {
		if prevValue > value {
			return
		}
	}

	stats.registry[key] = value
}

// Fields converts the given Stats structure into a set of logrus Fields for use in log messages.
func (stats *Stats) Fields() logrus.Fields {
	stats.Lock()
	defer stats.Unlock()

	f := logrus.Fields{}
	for k, v := range stats.registry {
		f[k] = v
	}
	return f
}

// StatsFromContext tries to extract stats from the provided context. It returns `nil` if tho
// context does not have any stats.
func StatsFromContext(ctx context.Context) *Stats {
	stats, _ := ctx.Value(requestStatsKey{}).(*Stats)
	return stats
}

// InitContextStats creates a new context with a Stats structure.
func InitContextStats(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestStatsKey{}, &Stats{
		registry: make(map[string]int),
	})
}
