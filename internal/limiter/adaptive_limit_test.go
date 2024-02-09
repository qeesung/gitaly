package limiter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestAdaptiveLimit_New(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, func(t *testing.T, ctx context.Context) {
		setting := AdaptiveSetting{
			Initial:       5,
			Max:           10,
			Min:           1,
			BackoffFactor: 0.5,
		}

		limit := NewAdaptiveLimit("testLimit", setting)
		require.Equal(t, limit.Name(), "testLimit")
		require.Equal(t, limit.Current(), 5)
		require.Equal(t, limit.Initial(), 5)
		require.Equal(t, limit.Setting(), setting)

		limit.Update(10)
		require.Equal(t, limit.Name(), "testLimit")
		require.Equal(t, limit.Current(), 10)
		require.Equal(t, limit.Initial(), 5)
		require.Equal(t, limit.Setting(), setting)
	})
}

func TestAdaptiveLimit_Update(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, func(t *testing.T, ctx context.Context) {
		newLimit := func() *AdaptiveLimit {
			return NewAdaptiveLimit("testLimit", AdaptiveSetting{
				Initial:       5,
				Max:           10,
				Min:           1,
				BackoffFactor: 0.5,
			})
		}

		t.Run("without update hooks", func(t *testing.T) {
			limit := newLimit()

			limit.Update(1)
			require.Equal(t, 1, limit.Current())

			limit.Update(2)
			require.Equal(t, 2, limit.Current())

			limit.Update(3)
			require.Equal(t, 3, limit.Current())
		})

		t.Run("new values are different from old values", func(t *testing.T) {
			limit := newLimit()

			vals := []int{}
			limit.AfterUpdate(func(val int) {
				vals = append(vals, val)
			})

			limit.Update(1)
			require.Equal(t, 1, limit.Current())
			require.Equal(t, vals, []int{1})

			limit.Update(2)
			require.Equal(t, 2, limit.Current())
			require.Equal(t, vals, []int{1, 2})

			limit.Update(3)
			require.Equal(t, 3, limit.Current())
			require.Equal(t, vals, []int{1, 2, 3})
		})

		t.Run("new values are the same as old values", func(t *testing.T) {
			limit := newLimit()

			vals := []int{}
			limit.AfterUpdate(func(val int) {
				vals = append(vals, val)
			})

			limit.Update(1)
			require.Equal(t, 1, limit.Current())
			require.Equal(t, vals, []int{1})

			limit.Update(1)
			require.Equal(t, 1, limit.Current())
			require.Equal(t, vals, []int{1})

			limit.Update(2)
			require.Equal(t, 2, limit.Current())
			require.Equal(t, vals, []int{1, 2})

			limit.Update(2)
			require.Equal(t, 2, limit.Current())
			require.Equal(t, vals, []int{1, 2})
		})

		t.Run("multiple update hooks", func(t *testing.T) {
			limit := newLimit()

			vals1 := []int{}
			limit.AfterUpdate(func(val int) {
				vals1 = append(vals1, val)
			})

			vals2 := []int{}
			limit.AfterUpdate(func(val int) {
				vals2 = append(vals2, val*2)
			})

			limit.Update(1)
			require.Equal(t, 1, limit.Current())
			require.Equal(t, vals1, []int{1})
			require.Equal(t, vals2, []int{2})

			limit.Update(2)
			require.Equal(t, 2, limit.Current())
			require.Equal(t, vals1, []int{1, 2})
			require.Equal(t, vals2, []int{2, 4})

			limit.Update(3)
			require.Equal(t, 3, limit.Current())
			require.Equal(t, vals1, []int{1, 2, 3})
			require.Equal(t, vals2, []int{2, 4, 6})
		})
	})
}
