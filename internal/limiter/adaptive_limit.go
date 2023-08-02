package limiter

import (
	"sync"
)

// AdaptiveSetting is a struct that holds the configuration parameters for an adaptive limiter.
type AdaptiveSetting struct {
	Initial       int
	Max           int
	Min           int
	BackoffFactor float64
}

// AfterUpdateHook is a function hook that is triggered when the current value changes. The callers need to register a hook to
// the AdaptiveLimiter implementation beforehand. They are required to handle errors inside the hook function.
type AfterUpdateHook func(newVal int)

// AdaptiveLimiter is an interface for managing and updating adaptive limits.
// It exposes methods to get the name, current limit value, update the limit value, and access its settings.
type AdaptiveLimiter interface {
	Name() string
	Current() int
	Update(val int)
	AfterUpdate(AfterUpdateHook)
	Setting() AdaptiveSetting
}

// AdaptiveLimit is an implementation of the AdaptiveLimiter interface. It uses an atomic Int32 to represent the current
// limit value, ensuring thread-safe updates.
type AdaptiveLimit struct {
	sync.RWMutex

	name        string
	current     int
	setting     AdaptiveSetting
	updateHooks []AfterUpdateHook
}

// NewAdaptiveLimit initializes a new AdaptiveLimit object
func NewAdaptiveLimit(name string, setting AdaptiveSetting) *AdaptiveLimit {
	return &AdaptiveLimit{
		name:    name,
		current: setting.Initial,
		setting: setting,
	}
}

// Name returns the name of the adaptive limit
func (l *AdaptiveLimit) Name() string {
	return l.name
}

// Current returns the current limit. This function can be called without the need for synchronization.
func (l *AdaptiveLimit) Current() int {
	l.RLock()
	defer l.RUnlock()

	return l.current
}

// Update adjusts current limit value.
func (l *AdaptiveLimit) Update(val int) {
	l.Lock()
	defer l.Unlock()

	if val != l.current {
		l.current = val
		for _, hook := range l.updateHooks {
			hook(val)
		}
	}
}

// AfterUpdate registers a callback when the current limit is updated. Because all updates and hooks are synchronized,
// calling Current() inside the update hook in the same goroutine will cause deadlock. Hence, the update hook must
// use the new value passed as the argument instead.
func (l *AdaptiveLimit) AfterUpdate(hook AfterUpdateHook) {
	l.updateHooks = append(l.updateHooks, hook)
}

// Setting returns the configuration parameters for an adaptive limiter.
func (l *AdaptiveLimit) Setting() AdaptiveSetting {
	return l.setting
}
