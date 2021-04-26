package helper

import "time"

// Ticker ticks on the channel returned by C to signal something.
type Ticker interface {
	C() <-chan time.Time
	Stop()
	Reset()
}

// NewTimerTicker returns a Ticker that ticks after the specified interval
// has passed since the previous Reset call.
func NewTimerTicker(interval time.Duration) Ticker {
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	return &timerTicker{timer: timer, interval: interval}
}

type timerTicker struct {
	timer    *time.Timer
	interval time.Duration
}

// C returns the ticker's timer channel. The current tick time will be sent on this channel
// whenever the ticker expires.
func (tt *timerTicker) C() <-chan time.Time { return tt.timer.C }

// Reset resets the ticker such that it will again fire after its specified interval.
func (tt *timerTicker) Reset() { tt.timer.Reset(tt.interval) }

// Stop will stop the ticker such that no new ticks will be generated.
func (tt *timerTicker) Stop() { tt.timer.Stop() }

// ManualTicker implements a ticker that ticks when Tick is called.
// Stop and Reset functions call the provided functions.
type ManualTicker struct {
	c         chan time.Time
	StopFunc  func()
	ResetFunc func()
}

// C returns the ticker's timer channel. The current tick time will be sent on this channel
// whenever the ticker expires.
func (mt *ManualTicker) C() <-chan time.Time { return mt.c }

// Stop will invoke the ticker's StopFunc.
func (mt *ManualTicker) Stop() { mt.StopFunc() }

// Reset will invoke the ticker's ResetFunc.
func (mt *ManualTicker) Reset() { mt.ResetFunc() }

// Tick will send a new tick on the ticker's channel.
func (mt *ManualTicker) Tick() { mt.c <- time.Now() }

// NewManualTicker returns a Ticker that can be manually controlled.
func NewManualTicker() *ManualTicker {
	return &ManualTicker{
		c:         make(chan time.Time, 1),
		StopFunc:  func() {},
		ResetFunc: func() {},
	}
}

// NewCountTicker returns a ManualTicker with a ResetFunc that
// calls the provided callback on Reset call after it has been
// called N times.
func NewCountTicker(n int, callback func()) *ManualTicker {
	ticker := NewManualTicker()
	ticker.ResetFunc = func() {
		n--
		if n < 0 {
			callback()
			return
		}

		ticker.Tick()
	}

	return ticker
}
