package service_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
)

func TestInProgressTracker(t *testing.T) {
	key := "key1"

	testCases := []struct {
		desc               string
		expectedInProgress int
		actions            func(*service.InProgressTracker)
	}{
		{
			desc:               "one in flight",
			expectedInProgress: 1,
			actions: func(t *service.InProgressTracker) {
				t.IncrementInProgress(key)
				t.IncrementInProgress(key)
				t.DecrementInProgress(key)
			},
		},
		{
			desc:               "two in flight with concurrent writes",
			expectedInProgress: 2,
			actions: func(t *service.InProgressTracker) {
				var wg sync.WaitGroup

				wg.Add(4)
				go func() {
					t.IncrementInProgress(key)
					wg.Done()
				}()
				go func() {
					t.IncrementInProgress(key)
					wg.Done()
				}()

				go func() {
					t.IncrementInProgress(key)
					wg.Done()
				}()

				go func() {
					t.DecrementInProgress(key)
					wg.Done()
				}()

				wg.Wait()
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			tracker := service.NewInProgressTracker()

			tc.actions(tracker)
			require.Equal(t, tc.expectedInProgress, tracker.GetInProgress(key))
		})
	}
}
