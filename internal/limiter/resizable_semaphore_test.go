package limiter

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestResizableSemaphore_New(t *testing.T) {
	t.Parallel()

	semaphore := NewResizableSemaphore(5)
	require.Equal(t, 0, semaphore.Count())
}

// TestResizableSemaphore_ContextCanceled ensures that when consumers successfully acquire the semaphore with a given
// context, and that context is subsequently canceled, the consumers are still able to release the semaphore. It also
// tests that consumers waiting to acquire the semaphore with the same canceled context are not able to do so once slots
// become available.
func TestResizableSemaphore_ContextCanceled(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, func(t *testing.T, ctx context.Context) {
		t.Run("context is canceled when the semaphore is empty", func(t *testing.T) {
			ctx, cancel := context.WithCancel(ctx)
			cancel()

			semaphore := NewResizableSemaphore(5)

			require.Equal(t, context.Canceled, semaphore.Acquire(ctx))
			require.Equal(t, 0, semaphore.Count())
		})

		t.Run("context is canceled when the semaphore is not full", func(t *testing.T) {
			ctx, cancel := context.WithCancel(ctx)
			testResizableSemaphoreCanceledWhenNotFull(t, ctx, cancel, context.Canceled)
		})

		t.Run("context is canceled when the semaphore is full", func(t *testing.T) {
			ctx, cancel := context.WithCancel(ctx)
			testResizableSemaphoreCanceledWhenFull(t, ctx, cancel, context.Canceled)
		})

		t.Run("context's deadline exceeded when the semaphore is empty", func(t *testing.T) {
			ctx, cancel := context.WithDeadline(ctx, time.Now().Add(-1*time.Hour))
			defer cancel()

			semaphore := NewResizableSemaphore(5)

			require.Equal(t, ErrMaxQueueTime, semaphore.Acquire(ctx))
			require.Equal(t, 0, semaphore.Count())
		})

		t.Run("context's deadline exceeded when the semaphore is not full", func(t *testing.T) {
			ctx, cancel, simulateTimeout := testhelper.ContextWithSimulatedTimeout(ctx)
			defer cancel()

			testResizableSemaphoreCanceledWhenNotFull(t, ctx, simulateTimeout, ErrMaxQueueTime)
		})

		t.Run("context's deadline exceeded when the semaphore is full", func(t *testing.T) {
			ctx, cancel, simulateTimeout := testhelper.ContextWithSimulatedTimeout(ctx)
			defer cancel()

			testResizableSemaphoreCanceledWhenFull(t, ctx, simulateTimeout, ErrMaxQueueTime)
		})
	})
}

func testResizableSemaphoreCanceledWhenNotFull(t *testing.T, ctx context.Context, stopContext context.CancelFunc, expectedErr error) {
	semaphore := NewResizableSemaphore(5)

	// 3 goroutines acquired semaphore
	beforeCallRelease := make(chan struct{})
	var acquireWg, releaseWg sync.WaitGroup
	for i := 0; i < 3; i++ {
		acquireWg.Add(1)
		releaseWg.Add(1)
		go func() {
			require.Nil(t, semaphore.Acquire(ctx))
			acquireWg.Done()

			<-beforeCallRelease

			semaphore.Release()
			releaseWg.Done()
		}()
	}
	acquireWg.Wait()

	// Now cancel the context
	stopContext()
	require.Equal(t, expectedErr, semaphore.Acquire(ctx))

	// The first 3 goroutines can call Release() even if the context is cancelled
	close(beforeCallRelease)
	releaseWg.Wait()

	require.Equal(t, expectedErr, semaphore.Acquire(ctx))
	require.Equal(t, 0, semaphore.Count())
}

func testResizableSemaphoreCanceledWhenFull(t *testing.T, ctx context.Context, stopContext context.CancelFunc, expectedErr error) {
	semaphore := NewResizableSemaphore(5)

	// Try to acquire a token of the empty semaphore
	require.Nil(t, semaphore.TryAcquire())
	semaphore.Release()

	// 5 goroutines acquired semaphore
	beforeCallRelease := make(chan struct{})
	var acquireWg1, releaseWg1 sync.WaitGroup
	for i := 0; i < 5; i++ {
		acquireWg1.Add(1)
		releaseWg1.Add(1)
		go func() {
			require.Nil(t, semaphore.Acquire(ctx))
			acquireWg1.Done()

			<-beforeCallRelease

			semaphore.Release()
			releaseWg1.Done()
		}()
	}
	acquireWg1.Wait()

	//  Another 5 waits for semaphore
	var acquireWg2 sync.WaitGroup
	for i := 0; i < 5; i++ {
		acquireWg2.Add(1)
		go func() {
			// This goroutine is block until the context is cancel, which returns canceled error
			require.Equal(t, expectedErr, semaphore.Acquire(ctx))
			acquireWg2.Done()
		}()
	}

	// Try to acquire a token of the full semaphore
	require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())

	// Cancel the context
	stopContext()
	acquireWg2.Wait()

	// The first 5 goroutines can call Release() even if the context is cancelled
	close(beforeCallRelease)
	releaseWg1.Wait()

	// The last 5 goroutines exits immediately, Acquire() returns error
	acquireWg2.Wait()

	require.Equal(t, 0, semaphore.Count())

	// Now the context is cancelled
	require.Equal(t, expectedErr, semaphore.Acquire(ctx))
}

func TestResizableSemaphore_Acquire(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, func(t *testing.T, ctx context.Context) {
		t.Run("acquire less than the capacity", func(t *testing.T) {
			semaphore := NewResizableSemaphore(5)

			waitBeforeRelease, waitRelease := acquireSemaphore(t, ctx, semaphore, 3)
			require.Equal(t, 3, semaphore.Count())

			require.Nil(t, semaphore.Acquire(ctx))
			require.Equal(t, 4, semaphore.Count())

			require.Nil(t, semaphore.TryAcquire())
			require.Equal(t, 5, semaphore.Count())

			close(waitBeforeRelease)
			waitRelease()

			// Still 2 left
			require.Equal(t, 2, semaphore.Count())
			semaphore.Release()
			semaphore.Release()

			require.Equal(t, 0, semaphore.Count())
		})

		t.Run("acquire more than the capacity", func(t *testing.T) {
			semaphore := NewResizableSemaphore(5)

			waitBeforeRelease, waitRelease := acquireSemaphore(t, ctx, semaphore, 5)
			require.Equal(t, 5, semaphore.Count())

			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
			require.Equal(t, 5, semaphore.Count())

			close(waitBeforeRelease)
			waitRelease()

			require.Equal(t, 0, semaphore.Count())
		})

		t.Run("semaphore is full then available again", func(t *testing.T) {
			semaphore := NewResizableSemaphore(5)
			for i := 0; i < 5; i++ {
				require.NoError(t, semaphore.Acquire(ctx))
			}

			waitChan := make(chan error)
			go func() {
				for i := 0; i < 5; i++ {
					waitChan <- semaphore.Acquire(ctx)
				}
			}()

			// The semaphore is full now
			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
			require.Equal(t, 5, semaphore.Count())

			for i := 0; i < 5; i++ {
				// Release one token
				semaphore.Release()
				// The waiting channel is unlocked
				require.Nil(t, <-waitChan)
			}

			// Release another token
			semaphore.Release()
			require.Equal(t, 4, semaphore.Count())

			// Now TryAcquire can pull out a token
			require.Nil(t, semaphore.TryAcquire())
			require.Equal(t, 5, semaphore.Count())
		})

		t.Run("the semaphore is resized up when empty", func(t *testing.T) {
			semaphore := NewResizableSemaphore(5)
			semaphore.Resize(10)

			waitBeforeRelease, waitRelease := acquireSemaphore(t, ctx, semaphore, 9)
			require.Equal(t, 9, semaphore.Count())

			require.Nil(t, semaphore.Acquire(ctx))
			require.Equal(t, 10, semaphore.Count())

			close(waitBeforeRelease)
			waitRelease()

			// Still 1 left
			semaphore.Release()

			require.Equal(t, 0, semaphore.Count())
		})

		t.Run("the semaphore is resized up when not empty", func(t *testing.T) {
			semaphore := NewResizableSemaphore(7)

			waitBeforeRelease1, waitRelease1 := acquireSemaphore(t, ctx, semaphore, 5)
			require.Equal(t, 5, semaphore.Count())

			semaphore.Resize(15)
			require.Equal(t, 5, semaphore.Count())

			waitBeforeRelease2, waitRelease2 := acquireSemaphore(t, ctx, semaphore, 5)
			require.Equal(t, 10, semaphore.Count())

			require.Nil(t, semaphore.Acquire(ctx))
			require.Equal(t, 11, semaphore.Count())

			close(waitBeforeRelease1)
			close(waitBeforeRelease2)
			waitRelease1()
			waitRelease2()

			// Still 1 left
			semaphore.Release()

			require.Equal(t, 0, semaphore.Count())
		})

		t.Run("the semaphore is resized up when full", func(t *testing.T) {
			semaphore := NewResizableSemaphore(5)

			waitBeforeRelease1, waitRelease1 := acquireSemaphore(t, ctx, semaphore, 5)

			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
			require.Equal(t, 5, semaphore.Count())

			semaphore.Resize(10)

			waitBeforeRelease2, waitRelease2 := acquireSemaphore(t, ctx, semaphore, 5)
			require.Equal(t, 10, semaphore.Count())

			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
			require.Equal(t, 10, semaphore.Count())

			var count atomic.Int32
			for i := 0; i < 10; i++ {
				go func() {
					require.Nil(t, semaphore.Acquire(ctx))
					count.Add(1)
				}()
			}

			semaphore.Resize(15)
			// Poll until 5 acquires
			for count.Load() != 5 {
				time.Sleep(1 * time.Millisecond)
			}
			// Resize to 20 to fit the rest 5
			semaphore.Resize(20)
			// Wait for the rest to finish
			for count.Load() != 10 {
				time.Sleep(1 * time.Millisecond)
			}

			close(waitBeforeRelease1)
			close(waitBeforeRelease2)
			waitRelease1()
			waitRelease2()

			require.Equal(t, 10, semaphore.Count())
		})

		t.Run("the semaphore is resized down when empty", func(t *testing.T) {
			semaphore := NewResizableSemaphore(10)
			semaphore.Resize(5)

			waitBeforeRelease, waitRelease := acquireSemaphore(t, ctx, semaphore, 4)
			require.Equal(t, 4, semaphore.Count())

			require.Nil(t, semaphore.Acquire(ctx))
			require.Equal(t, 5, semaphore.Count())

			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
			require.Equal(t, 5, semaphore.Count())

			close(waitBeforeRelease)
			waitRelease()

			// Still 1 left
			semaphore.Release()

			require.Equal(t, 0, semaphore.Count())
		})

		t.Run("the semaphore is resized down when not empty", func(t *testing.T) {
			semaphore := NewResizableSemaphore(20)

			waitBeforeRelease1, waitRelease1 := acquireSemaphore(t, ctx, semaphore, 5)
			require.Equal(t, 5, semaphore.Count())

			semaphore.Resize(15)
			waitBeforeRelease2, waitRelease2 := acquireSemaphore(t, ctx, semaphore, 5)
			require.Equal(t, 10, semaphore.Count())

			require.Nil(t, semaphore.Acquire(ctx))
			require.Equal(t, 11, semaphore.Count())

			close(waitBeforeRelease1)
			close(waitBeforeRelease2)
			waitRelease1()
			waitRelease2()

			// Still 1 left
			semaphore.Release()

			require.Equal(t, 0, semaphore.Count())
		})

		t.Run("the semaphore is resized down lower than the current length", func(t *testing.T) {
			semaphore := NewResizableSemaphore(10)

			waitBeforeRelease1, waitRelease1 := acquireSemaphore(t, ctx, semaphore, 5)
			require.Equal(t, 5, semaphore.Count())

			semaphore.Resize(3)
			require.Equal(t, 5, semaphore.Count())

			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())

			close(waitBeforeRelease1)
			waitRelease1()
			require.Equal(t, 0, semaphore.Count())

			waitBeforeRelease2, waitRelease2 := acquireSemaphore(t, ctx, semaphore, 3)
			require.Equal(t, 3, semaphore.Count())

			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
			require.Equal(t, 3, semaphore.Count())

			close(waitBeforeRelease2)
			waitRelease2()

			require.Equal(t, 0, semaphore.Count())
		})

		t.Run("the semaphore is resized down when full", func(t *testing.T) {
			semaphore := NewResizableSemaphore(10)

			waitBeforeRelease1, waitRelease1 := acquireSemaphore(t, ctx, semaphore, 10)
			require.Equal(t, 10, semaphore.Count())

			semaphore.Resize(5)
			require.Equal(t, 10, semaphore.Count())
			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())

			close(waitBeforeRelease1)
			waitRelease1()

			require.Equal(t, 0, semaphore.Count())

			waitBeforeRelease2, waitRelease2 := acquireSemaphore(t, ctx, semaphore, 5)

			require.Equal(t, 5, semaphore.Count())
			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())

			close(waitBeforeRelease2)
			waitRelease2()

			require.Equal(t, 0, semaphore.Count())
		})

		t.Run("the semaphore is resized up and down consecutively", func(t *testing.T) {
			semaphore := NewResizableSemaphore(10)

			for i := 0; i < 5; i++ {
				require.NoError(t, semaphore.Acquire(ctx))
			}
			require.Equal(t, 5, semaphore.Count())

			semaphore.Resize(7)
			require.Equal(t, 5, semaphore.Count())

			require.NoError(t, semaphore.Acquire(ctx))
			require.Equal(t, 6, semaphore.Count())

			// Resize down to 3, current = 3, leftover = 3
			semaphore.Resize(3)
			require.Equal(t, 6, semaphore.Count())

			// Cannot acquire
			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
			semaphore.Release()
			require.Equal(t, 5, semaphore.Count())
			semaphore.Release()
			require.Equal(t, 4, semaphore.Count())

			// Resize down again. Current = 2, leftover = 2
			semaphore.Resize(2)
			require.Equal(t, 4, semaphore.Count())

			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())
			semaphore.Release()
			require.Equal(t, 3, semaphore.Count())
			semaphore.Release()
			require.Equal(t, 2, semaphore.Count())

			// Leftover is used up, but still cannot acquire
			require.Equal(t, ErrMaxQueueSize, semaphore.TryAcquire())

			// Acquireable now
			semaphore.Release()
			require.Equal(t, 1, semaphore.Count())
			require.NoError(t, semaphore.Acquire(ctx))
			require.Equal(t, 2, semaphore.Count())
		})
	})
}

func BenchmarkResizableSemaphore(b *testing.B) {
	for _, numIterations := range []uint{100, 1000, 10_000} {
		n := numIterations
		b.Run(fmt.Sprintf("%d", n), func(b *testing.B) {
			b.Run("acquire then release immediately", func(b *testing.B) {
				b.ResetTimer()
				ctx := testhelper.Context(b)
				semaphore := NewResizableSemaphore(2)

				for i := uint(0); i < n; i++ {
					b.StartTimer()
					err := semaphore.Acquire(ctx)
					semaphore.Release()
					b.StopTimer()
					require.NoError(b, err)
				}

				require.Equal(b, 0, semaphore.Count())
			})

			b.Run("acquire then release after done", func(b *testing.B) {
				b.ResetTimer()
				ctx := testhelper.Context(b)
				semaphore := NewResizableSemaphore(n)

				for i := uint(0); i < n; i++ {
					b.StartTimer()
					err := semaphore.Acquire(ctx)
					b.StopTimer()
					require.NoError(b, err)
				}
				for i := uint(0); i < n; i++ {
					b.StartTimer()
					semaphore.Release()
					b.StopTimer()
				}
				require.Equal(b, 0, semaphore.Count())
			})

			b.Run("acquire after waiting", func(b *testing.B) {
				b.ResetTimer()
				ctx := testhelper.Context(b)
				semaphore := NewResizableSemaphore(n)

				for i := uint(0); i < n; i++ {
					require.NoError(b, semaphore.Acquire(ctx))
				}
				// All of the following acquisitions are blocked
				waitChan := make(chan error)
				go func() {
					for i := uint(0); i < n; i++ {
						waitChan <- semaphore.Acquire(ctx)
					}
				}()
				for i := uint(0); i < n; i++ {
					// Measure the time since the last release and to the waiter acquires the
					// semaphore.
					b.StartTimer()
					semaphore.Release()
					<-waitChan
					b.StopTimer()
				}
				require.Equal(b, n, semaphore.Count())
			})
		})
	}
}

// acquireSemaphore attempts to acquire semaphore n times using ctx. It returns a channel which can be closed as a
// signal to the consumer to release the semaphore, and a WaitGroup which blocks until all consumers are finished.
func acquireSemaphore(t *testing.T, ctx context.Context, semaphore *resizableSemaphore, n int) (chan struct{}, func()) {
	var acquireWg, releaseWg sync.WaitGroup
	waitBeforeRelease := make(chan struct{})

	for i := 0; i < n; i++ {
		acquireWg.Add(1)
		releaseWg.Add(1)
		go func() {
			require.Nil(t, semaphore.Acquire(ctx))
			acquireWg.Done()

			<-waitBeforeRelease
			semaphore.Release()
			releaseWg.Done()
		}()
	}
	acquireWg.Wait()

	return waitBeforeRelease, releaseWg.Wait
}
