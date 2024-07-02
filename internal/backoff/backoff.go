// Package backoff implements exponential backoff mechanism based on gRPC's backoff algorithm
// https://github.com/grpc/grpc/blob/master/doc/connection-backoff.md
package backoff

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// Strategy implements a backoff strategy. This strategy has a single Backoff method that returns
// a time duration corresponding to the input retries.
type Strategy interface {
	Backoff(retries uint) time.Duration
}

// Exponential defines an exponential backoff strategy. It multiplicatively decreases the rate of
// retrial by increasing the wait time. The wait time is calculated to following:
// Backoff(retries) = BaseDelay * (Multiplier ^ retries) + rand(0, Jitter)
// The backoff time can never exceed the MaxDelay.
type Exponential struct {
	sync.Mutex

	// BaseDelay is the minimum delay for the first attempt.
	BaseDelay time.Duration
	// MaxDelay is the upper limit for exponential backoff.
	MaxDelay time.Duration
	// Multiplier is the factor determining "how fast" the delay increases after each retry.
	Multiplier float64
	// Jitter defines the maximum of randomized duration added to the delay of each step. This
	// randomization prevents all actors retry at the same time.
	Jitter time.Duration
	// Random source for randomizing delay of each step
	rand *rand.Rand
}

// NewDefaultExponential returns an exponential backoff strategy using a set of configurations good
// enough for network connection retry.
func NewDefaultExponential(r *rand.Rand) *Exponential {
	//
	// | Retries | Delay before jitter |
	// | ------- | ------------------- |
	// | 0       | 1 second            |
	// | 1       | 1.5 seconds         |
	// | 2       | 2.3 seconds         |
	// | 3       | 3.4 seconds         |
	// | 4       | 5.0 seconds         |
	// | 5       | 7.6 seconds         |
	// | 6       | 11.0 seconds        |
	// | 7       | 17.1 seconds        |
	// | 8       | 25.1 seconds        |
	// | 9       | 38.4 seconds        |
	// | 10      | 57.6 seconds        |
	// | 11      | 60 seconds          |
	// | 12      | 60 seconds          |
	// | ...     | 60 seconds          |
	return &Exponential{
		BaseDelay:  1 * time.Second,
		MaxDelay:   60 * time.Second,
		Multiplier: 1.5,
		Jitter:     200 * time.Millisecond,
		rand:       r,
	}
}

// Backoff returns the duration to wait before retry again. The caller is fully responsible for
// retry controlling retry attempts and timers. Typically, the caller increases the retry attempt
// and creates a timer from the result of this method. Typically, this method is called before
// increasing the attempt of the caller, which is Backoff(0). It means the backoff time after the
// first failure.
func (e *Exponential) Backoff(retries uint) time.Duration {
	// Rand's source is not thread-safe. We could create per-goroutine rand source or use a mutex.
	e.Lock()
	defer e.Unlock()

	backoff := math.Min(
		float64(e.MaxDelay),
		float64(e.BaseDelay)*math.Pow(e.Multiplier, float64(retries)),
	) + float64(e.Jitter)*e.rand.Float64()
	return time.Duration(backoff).Round(time.Millisecond)
}
