// Package dontpanic provides function wrappers and supervisors to ensure
// that wrapped code does not panic and cause program crashes. It additionally
// provides convenience handlers for logging panics and backing off before
// subsequent attempts.
//
// When should you use this package? Anytime you are running a function or
// goroutine where it isn't obvious whether it can or can't panic. This may
// be a higher risk in long running goroutines and functions or ones that are
// difficult to test completely.
package dontpanic

import (
	"time"

	raven "github.com/getsentry/raven-go"
	"gitlab.com/gitlab-org/gitaly/internal/log"
)

// Try will wrap the provided function with a panic recovery and return any
// recovered value
func Try(fn func()) (recovered interface{}) {
	defer func() { recovered = recover() }()
	fn()
	return
}

// Go will run the provided function in a goroutine and recover from any
// panics. Any recovered value will be emitted via returned channel.
// If no panic occurred, nil will be emitted. The channel is then
// closed.
func Go(fn func()) <-chan interface{} {
	recoverQ := make(chan interface{}, 1)

	go func() {
		defer close(recoverQ)
		recoverQ <- Try(fn)
	}()

	return recoverQ
}

// PanicHandler is called after a panic occurs and before the next retry attempt
type PanicHandler func(recovered interface{})

var logger = log.Default()

// SentryCapture is a chainable panic handler. It re-panics the recovered
// value inside Sentry's own recovery handler and then error logs the sentry
// ID for later correlation.
func SentryCapture(ph PanicHandler) PanicHandler {
	return func(recovered interface{}) {
		if ph != nil {
			ph(recovered)
		}
		_, id := raven.CapturePanic(func() { panic(recovered) }, nil)
		logger.WithField("sentry_id", id).Errorf(
			"dontpanic: recovered value %v sent to Sentry", recovered)
	}
}

// ErrorLog is a chainable panic handler. It will log the recovered value at
// log level error
func ErrorLog(ph PanicHandler) PanicHandler {
	return func(recovered interface{}) {
		if ph != nil {
			ph(recovered)
		}
		logger.Errorf("dontpanic: panic handled: %+v", recovered)
	}
}

// BackOff is a chainable panic handler. It will sleep for specified duration
// effectively delaying retry.
func BackOff(backoff time.Duration, ph PanicHandler) PanicHandler {
	return func(recovered interface{}) {
		if ph != nil {
			ph(recovered)
		}
		logger.Infof("dontpanic: backing off %s until next retry", backoff)
		time.Sleep(backoff)
	}
}

// GoRetry will keep retrying a function fn in a goroutine while recovering
// from panics. The closure can stop the retry loop by calling the provided
// param stopRetrying. Each time a closure panics, the recovered value can be
// handled by an optionally provided parameter handler. A channel is returned
// to signal when the retry loop has returned.
func GoRetry(fn func(stopRetrying func()), handler PanicHandler) <-chan struct{} {
	unrecoverable := &struct{}{}
	panicUnrecoverable := func() { panic(unrecoverable) }

	recoverFn := func() interface{} {
		return Try(func() { fn(panicUnrecoverable) })
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		for {
			r := recoverFn()
			if r == unrecoverable {
				return
			}
			Try(func() { handler(r) })
		}
	}()

	return done
}
