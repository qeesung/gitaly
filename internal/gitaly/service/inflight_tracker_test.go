package service_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
)

func TestInflightTracker(t *testing.T) {
	key := "key1"

	testCases := []struct {
		desc             string
		expectedInflight int
		actions          func(*service.InflightTracker)
	}{
		{
			desc:             "one in flight",
			expectedInflight: 1,
			actions: func(t *service.InflightTracker) {
				t.IncrementInProgress(key)
				t.IncrementInProgress(key)
				t.DecrementInProgress(key)
			},
		},
		{
			desc:             "two in flight with concurrent writes",
			expectedInflight: 2,
			actions: func(t *service.InflightTracker) {
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
			tracker := service.NewInflightTracker()

			tc.actions(tracker)
			require.Equal(t, tc.expectedInflight, tracker.GetInflight(key))
		})
	}
}
