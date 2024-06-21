package service

import (
	"sync"
)

// InflightTracker can be used to keep track of processes that are in flight
type InflightTracker struct {
	inProgress map[string]int
	l          sync.RWMutex
}

// NewInflightTracker instantiates a new InflightTracker.
func NewInflightTracker() *InflightTracker {
	return &InflightTracker{
		inProgress: make(map[string]int),
	}
}

// GetInflight gets the number of inflight processes for a given key.
func (p *InflightTracker) GetInflight(key string) int {
	p.l.RLock()
	defer p.l.RUnlock()

	return p.inProgress[key]
}

// IncrementInProgress increments the number of inflight processes for a given key.
func (p *InflightTracker) IncrementInProgress(key string) {
	p.l.Lock()
	defer p.l.Unlock()

	p.inProgress[key]++
}

// DecrementInProgress decrements the number of inflight processes for a given key.
func (p *InflightTracker) DecrementInProgress(key string) {
	p.l.Lock()
	defer p.l.Unlock()

	p.inProgress[key]--
}
