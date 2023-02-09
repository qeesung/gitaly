package tick

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimerTicker(t *testing.T) {
	const interval = 10 * time.Millisecond
	const wait = 2 * interval

	ticker := NewTimerTicker(interval)

	select {
	case <-ticker.C():
		t.Fatalf("ticker should be inactive before first reset call")
	case <-time.After(wait):
	}

	ticker.Reset()

	select {
	case <-ticker.C():
	case <-time.After(wait):
		t.Fatalf("timed out waiting for a tick")
	}

	ticker.Reset()
	ticker.Stop()

	select {
	case <-ticker.C():
		t.Fatalf("should not receive a tick if the ticker was stopped")
	case <-time.After(wait):
	}
}

func TestManualTicker(t *testing.T) {
	ticker := NewManualTicker()

	require.NotPanics(t, ticker.Reset)
	require.NotPanics(t, ticker.Stop)

	reset := false
	ticker.ResetFunc = func() { reset = true }
	ticker.Reset()
	require.True(t, reset)

	stopped := false
	ticker.StopFunc = func() { stopped = true }
	ticker.Stop()
	require.True(t, stopped)

	select {
	case <-ticker.C():
		t.Fatalf("ManualTicker ticked before calling Tick")
	default:
	}

	ticker.Tick()

	select {
	case <-ticker.C():
	default:
		t.Fatalf("did not receive expected tick")
	}
}

func TestCountTicker(t *testing.T) {
	callbackCalled := make(chan struct{}, 1)
	callback := func() { callbackCalled <- struct{}{} }
	numCalls := 2

	ticker := NewCountTicker(numCalls, callback)

	for i := 0; i < numCalls; i++ {
		ticker.Reset()
		select {
		case <-ticker.C():
		case <-callbackCalled:
			t.Fatalf("Callback should not be called before the number of is reached")
		default:
			t.Fatalf("did not receive expected tick")
		}
	}

	for i := 0; i < numCalls; i++ {
		ticker.Reset()
		select {
		case <-ticker.C():
			t.Fatalf("Should not tick after the maximum number of reset calls is reached")
		case <-callbackCalled:
		default:
			t.Fatalf("callback was not called as expected")
		}
	}
}
