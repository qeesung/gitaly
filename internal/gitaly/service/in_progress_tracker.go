package service

import (
	"sync"
)

// InProgressTracker can be used to keep track of processes that are in flight
type InProgressTracker struct {
	inProgress map[string]int
	l          sync.RWMutex
}

// NewInProgressTracker instantiates a new InProgressTracker.
func NewInProgressTracker() *InProgressTracker {
	return &InProgressTracker{
		inProgress: make(map[string]int),
	}
}

// GetInProgress gets the number of inflight processes for a given key.
func (p *InProgressTracker) GetInProgress(key string) int {
	p.l.RLock()
	defer p.l.RUnlock()

	return p.inProgress[key]
}

// IncrementInProgress increments the number of inflight processes for a given key.
func (p *InProgressTracker) IncrementInProgress(key string) {
	p.l.Lock()
	defer p.l.Unlock()

	p.inProgress[key]++
}

// DecrementInProgress decrements the number of inflight processes for a given key.
func (p *InProgressTracker) DecrementInProgress(key string) {
	p.l.Lock()
	defer p.l.Unlock()

	p.inProgress[key]--
}
