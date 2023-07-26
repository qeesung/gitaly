package limiter

import "sync/atomic"

// AdaptiveSetting is a struct that holds the configuration parameters for an adaptive limiter.
type AdaptiveSetting struct {
	Initial       int
	Max           int
	Min           int
	BackoffFactor float64
}

// AdaptiveLimiter is an interface for managing and updating adaptive limits.
// It exposes methods to get the name, current limit value, update the limit value, and access its settings.
type AdaptiveLimiter interface {
	Name() string
	Current() int
	Update(val int)
	Setting() AdaptiveSetting
}

// AdaptiveLimit is an implementation of the AdaptiveLimiter interface. It uses an atomic Int32 to represent the current
// limit value, ensuring thread-safe updates.
type AdaptiveLimit struct {
	name    string
	current atomic.Int32
	setting AdaptiveSetting
}

// Name returns the name of the adaptive limit
func (l *AdaptiveLimit) Name() string {
	return l.name
}

// Current returns the current limit. This function can be called without the need for synchronization.
func (l *AdaptiveLimit) Current() int {
	return int(l.current.Load())
}

// Update adjusts current limit value.
func (l *AdaptiveLimit) Update(val int) {
	l.current.Store(int32(val))
}

// Setting returns the configuration parameters for an adaptive limiter.
func (l *AdaptiveLimit) Setting() AdaptiveSetting {
	return l.setting
}
