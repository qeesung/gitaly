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
	"runtime"
	"time"

	"github.com/sirupsen/logrus"
)

// Try will wrap the provided function with a panic recovery and return any
// recovered value
func Try(fn func()) (recovered interface{}) {
	defer func() {
		recovered = recover()
	}()

	fn()
	return nil
}

// Go will run the provided function in a gorourtine and recover from any
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

// DebugTrace is a chainable panic handler. It prints a stack trace if the
// logrus log level is set to debug or higher.
func DebugTrace(ph PanicHandler) PanicHandler {
	return func(recovered interface{}) {
		if ph != nil {
			ph(recovered)
		}
		if logrus.IsLevelEnabled(logrus.DebugLevel) {
			b := make([]byte, 1024)
			n := runtime.Stack(b, false)
			logrus.Debugf("dontpanic: stack trace: %s", string(b[0:n]))
		}
	}
}

// ErrorLog is a chainable panic handler. It will log the recovered value at
// log level error
func ErrorLog(ph PanicHandler) PanicHandler {
	return func(recovered interface{}) {
		if ph != nil {
			ph(recovered)
		}
		logrus.Errorf("dontpanic: panic handled: %+v", recovered)
	}
}

// BackOff is a chainable panic handler. It will sleep for specified duration
// effectively delaying retry.
func BackOff(backoff time.Duration, ph PanicHandler) PanicHandler {
	return func(recovered interface{}) {
		if ph != nil {
			ph(recovered)
		}
		logrus.Infof("dontpanic: backing off %s until next retry", backoff)
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
