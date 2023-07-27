package limiter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestResizableSemaphore_New(t *testing.T) {
	t.Parallel()

	semaphore := NewResizableSemaphore(5)
	require.Equal(t, int64(0), semaphore.Current())
}

func TestResizableSemaphore_Canceled(t *testing.T) {
	t.Parallel()

	t.Run("context is canceled when the semaphore is empty", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx, cancel := context.WithCancel(testhelper.Context(t))
		cancel()

		semaphore := NewResizableSemaphore(5)

		require.Equal(t, context.Canceled, semaphore.Acquire(ctx, ticker))
		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("context is canceled when the semaphore is acquirable", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx, cancel := context.WithCancel(testhelper.Context(t))
		semaphore := NewResizableSemaphore(5)

		// 3 goroutines acquired semaphore
		beforeCallRelease := make(chan struct{})
		var acquireWg1, releaseWg1 sync.WaitGroup
		for i := 0; i < 3; i++ {
			acquireWg1.Add(1)
			releaseWg1.Add(1)
			go func() {
				require.Nil(t, semaphore.Acquire(ctx, ticker))
				acquireWg1.Done()

				<-beforeCallRelease

				semaphore.Release()
				releaseWg1.Done()
			}()
		}
		acquireWg1.Wait()

		// Now cancel the context
		cancel()
		require.Equal(t, context.Canceled, semaphore.Acquire(ctx, ticker))

		// The first 3 goroutines can call Release() even if the context is cancelled
		close(beforeCallRelease)
		releaseWg1.Wait()

		require.Equal(t, context.Canceled, semaphore.Acquire(ctx, ticker))
		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("context is canceled when there are waiters", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx, cancel := context.WithCancel(testhelper.Context(t))
		semaphore := NewResizableSemaphore(5)

		// Try to acquire a token of the empty sempahore
		require.Nil(t, semaphore.TryAcquire())
		semaphore.Release()

		// 5 goroutines acquired semaphore
		beforeCallRelease := make(chan struct{})
		var acquireWg1, releaseWg1 sync.WaitGroup
		for i := 0; i < 5; i++ {
			acquireWg1.Add(1)
			releaseWg1.Add(1)
			go func() {
				require.Nil(t, semaphore.Acquire(ctx, ticker))
				acquireWg1.Done()

				<-beforeCallRelease

				semaphore.Release()
				releaseWg1.Done()
			}()
		}
		acquireWg1.Wait()

		//  Another 5 waits for sempahore
		var acquireWg2 sync.WaitGroup
		for i := 0; i < 5; i++ {
			acquireWg2.Add(1)
			go func() {
				// This goroutine is block until the context is cancel, which returns canceled error
				require.Equal(t, context.Canceled, semaphore.Acquire(ctx, ticker))
				acquireWg2.Done()
			}()
		}

		// Try to acquire a token of the full semaphore
		require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())

		// Cancel the context
		cancel()
		acquireWg2.Wait()

		// The first 5 goroutines can call Release() even if the context is cancelled
		close(beforeCallRelease)
		releaseWg1.Wait()

		// The last 5 goroutines exits immediately, Acquire() returns error
		acquireWg2.Wait()

		require.Equal(t, int64(0), semaphore.Current())

		// Now the context is cancelled
		require.Equal(t, context.Canceled, semaphore.Acquire(ctx, ticker))
	})

	t.Run("ticker ticks when the semaphore is full", func(t *testing.T) {
		ctx := testhelper.Context(t)
		semaphore := NewResizableSemaphore(5)

		// Try to acquire a token of the empty sempahore
		require.Nil(t, semaphore.TryAcquire())
		semaphore.Release()

		// 5 goroutines acquired semaphore
		beforeCallRelease := make(chan struct{})
		var acquireWg1, releaseWg1 sync.WaitGroup
		for i := 0; i < 5; i++ {
			acquireWg1.Add(1)
			releaseWg1.Add(1)
			go func() {
				require.Nil(t, semaphore.Acquire(ctx, helper.NewManualTicker()))
				acquireWg1.Done()

				<-beforeCallRelease

				semaphore.Release()
				releaseWg1.Done()
			}()
		}
		acquireWg1.Wait()

		//  Another 5 waits for sempahore
		var tickers []*helper.ManualTicker
		var acquireWg2 sync.WaitGroup
		for i := 0; i < 5; i++ {
			acquireWg2.Add(1)
			ticker := helper.NewManualTicker()
			tickers = append(tickers, ticker)
			go func(ticker helper.Ticker) {
				// This goroutine is unlocked with an error when its ticker ticks
				require.Equal(t, ErrMaxQueueTime, semaphore.Acquire(ctx, ticker))
				acquireWg2.Done()
			}(ticker)
		}

		// Try to acquire a token of the full semaphore
		require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())

		// Ticks all tickers
		for _, ticker := range tickers {
			ticker.Tick()
		}
		acquireWg2.Wait()

		// The first 5 goroutines can call Release() even if tickers tick
		close(beforeCallRelease)
		releaseWg1.Wait()

		// The last 5 goroutines exits immediately, Acquire() returns an error
		acquireWg2.Wait()

		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("context's deadline exceeded", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx, cancel := context.WithDeadline(testhelper.Context(t), time.Now().Add(-1*time.Hour))
		defer cancel()

		semaphore := NewResizableSemaphore(5)

		require.Equal(t, context.DeadlineExceeded, semaphore.Acquire(ctx, ticker))
		require.Equal(t, int64(0), semaphore.Current())
	})
}

func TestResizableSemaphore_Acquire(t *testing.T) {
	t.Parallel()

	t.Run("acquire less than the capacity", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx := testhelper.Context(t)
		semaphore := NewResizableSemaphore(5)

		waitBeforeRelease, waitRelease := acquireSemaphore(t, ctx, ticker, semaphore, 3)
		require.Equal(t, int64(3), semaphore.Current())

		require.Nil(t, semaphore.Acquire(ctx, ticker))
		require.Equal(t, int64(4), semaphore.Current())

		require.Nil(t, semaphore.TryAcquire())
		require.Equal(t, int64(5), semaphore.Current())

		close(waitBeforeRelease)
		waitRelease()

		// Still 2 left
		require.Equal(t, int64(2), semaphore.Current())
		semaphore.Release()
		semaphore.Release()

		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("acquire more than the capacity", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx := testhelper.Context(t)
		semaphore := NewResizableSemaphore(5)

		waitBeforeRelease, waitRelease := acquireSemaphore(t, ctx, ticker, semaphore, 5)
		require.Equal(t, int64(5), semaphore.Current())

		require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
		require.Equal(t, int64(5), semaphore.Current())

		close(waitBeforeRelease)
		waitRelease()

		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("semaphore is full then available again", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx := testhelper.Context(t)
		semaphore := NewResizableSemaphore(5)

		waitChan := make(chan error)
		waitBeforeRelease, waitRelease := acquireSemaphore(t, ctx, ticker, semaphore, 5)

		go func() {
			waitChan <- semaphore.Acquire(ctx, ticker)
		}()

		// The semaphore is full now
		require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
		require.Equal(t, int64(5), semaphore.Current())

		// Release one token
		semaphore.Release()
		// The waiting channel is unlocked
		require.Nil(t, <-waitChan)

		// Release another token
		semaphore.Release()
		require.Equal(t, int64(4), semaphore.Current())

		// Now TryAcquire can pull out a token
		require.Nil(t, semaphore.TryAcquire())
		require.Equal(t, int64(5), semaphore.Current())

		close(waitBeforeRelease)
		waitRelease()

		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("the semaphore is resized up when empty", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx := testhelper.Context(t)

		semaphore := NewResizableSemaphore(5)
		semaphore.Resize(10)

		waitBeforeRelease, waitRelease := acquireSemaphore(t, ctx, ticker, semaphore, 9)
		require.Equal(t, int64(9), semaphore.Current())

		require.Nil(t, semaphore.Acquire(ctx, ticker))
		require.Equal(t, int64(10), semaphore.Current())

		close(waitBeforeRelease)
		waitRelease()

		// Still 1 left
		semaphore.Release()

		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("the semaphore is resized up when not empty", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx := testhelper.Context(t)
		semaphore := NewResizableSemaphore(7)

		waitBeforeRelease1, waitRelease1 := acquireSemaphore(t, ctx, ticker, semaphore, 5)
		require.Equal(t, int64(5), semaphore.Current())

		semaphore.Resize(15)
		require.Equal(t, int64(5), semaphore.Current())

		waitBeforeRelease2, waitRelease2 := acquireSemaphore(t, ctx, ticker, semaphore, 5)
		require.Equal(t, int64(10), semaphore.Current())

		require.Nil(t, semaphore.Acquire(ctx, ticker))
		require.Equal(t, int64(11), semaphore.Current())

		close(waitBeforeRelease1)
		close(waitBeforeRelease2)
		waitRelease1()
		waitRelease2()

		// Still 1 left
		semaphore.Release()

		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("the semaphore is resized up when full", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx := testhelper.Context(t)
		semaphore := NewResizableSemaphore(5)

		waitBeforeRelease1, waitRelease1 := acquireSemaphore(t, ctx, ticker, semaphore, 5)

		require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
		require.Equal(t, int64(5), semaphore.Current())

		semaphore.Resize(10)

		waitBeforeRelease2, waitRelease2 := acquireSemaphore(t, ctx, ticker, semaphore, 5)
		require.Equal(t, int64(10), semaphore.Current())

		require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
		require.Equal(t, int64(10), semaphore.Current())

		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				require.Nil(t, semaphore.Acquire(ctx, ticker))
				wg.Done()
				semaphore.Release()
			}()
		}

		semaphore.Resize(15)
		wg.Wait()

		close(waitBeforeRelease1)
		close(waitBeforeRelease2)
		waitRelease1()
		waitRelease2()

		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("the semaphore is resized down when empty", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx := testhelper.Context(t)
		semaphore := NewResizableSemaphore(10)
		semaphore.Resize(5)

		waitBeforeRelease, waitRelease := acquireSemaphore(t, ctx, ticker, semaphore, 4)
		require.Equal(t, int64(4), semaphore.Current())

		require.Nil(t, semaphore.Acquire(ctx, ticker))
		require.Equal(t, int64(5), semaphore.Current())

		require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
		require.Equal(t, int64(5), semaphore.Current())

		close(waitBeforeRelease)
		waitRelease()

		// Still 1 left
		semaphore.Release()

		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("the semaphore is resized down when not empty", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx := testhelper.Context(t)
		semaphore := NewResizableSemaphore(20)

		waitBeforeRelease1, waitRelease1 := acquireSemaphore(t, ctx, ticker, semaphore, 5)
		require.Equal(t, int64(5), semaphore.Current())

		semaphore.Resize(15)
		waitBeforeRelease2, waitRelease2 := acquireSemaphore(t, ctx, ticker, semaphore, 5)
		require.Equal(t, int64(10), semaphore.Current())

		require.Nil(t, semaphore.Acquire(ctx, ticker))
		require.Equal(t, int64(11), semaphore.Current())

		close(waitBeforeRelease1)
		close(waitBeforeRelease2)
		waitRelease1()
		waitRelease2()

		// Still 1 left
		semaphore.Release()

		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("the semaphore is resized down lower than the current length", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx := testhelper.Context(t)
		semaphore := NewResizableSemaphore(10)

		waitBeforeRelease1, waitRelease1 := acquireSemaphore(t, ctx, ticker, semaphore, 5)
		require.Equal(t, int64(5), semaphore.Current())

		semaphore.Resize(3)
		require.Equal(t, int64(5), semaphore.Current())

		require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())

		close(waitBeforeRelease1)
		waitRelease1()
		require.Equal(t, int64(0), semaphore.Current())

		waitBeforeRelease2, waitRelease2 := acquireSemaphore(t, ctx, ticker, semaphore, 3)
		require.Equal(t, int64(3), semaphore.Current())

		require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
		require.Equal(t, int64(3), semaphore.Current())

		close(waitBeforeRelease2)
		waitRelease2()

		require.Equal(t, int64(0), semaphore.Current())
	})

	t.Run("the semaphore is resized down when full", func(t *testing.T) {
		ticker := helper.NewManualTicker()
		ctx := testhelper.Context(t)
		semaphore := NewResizableSemaphore(10)

		waitBeforeRelease1, waitRelease1 := acquireSemaphore(t, ctx, ticker, semaphore, 10)
		require.Equal(t, int64(10), semaphore.Current())

		semaphore.Resize(5)
		require.Equal(t, int64(10), semaphore.Current())
		require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())

		close(waitBeforeRelease1)
		waitRelease1()

		require.Equal(t, int64(0), semaphore.Current())

		waitBeforeRelease2, waitRelease2 := acquireSemaphore(t, ctx, ticker, semaphore, 5)

		require.Equal(t, int64(5), semaphore.Current())
		require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())

		close(waitBeforeRelease2)
		waitRelease2()

		require.Equal(t, int64(0), semaphore.Current())
	})
}

func acquireSemaphore(t *testing.T, ctx context.Context, ticker helper.Ticker, semaphore *resizableSemaphore, n int) (chan struct{}, func()) {
	var acquireWg, releaseWg sync.WaitGroup
	waitBeforeRelease := make(chan struct{})

	for i := 0; i < n; i++ {
		acquireWg.Add(1)
		releaseWg.Add(1)
		go func() {
			require.Nil(t, semaphore.Acquire(ctx, ticker))
			acquireWg.Done()

			<-waitBeforeRelease
			semaphore.Release()
			releaseWg.Done()
		}()
	}
	acquireWg.Wait()

	return waitBeforeRelease, releaseWg.Wait
}
