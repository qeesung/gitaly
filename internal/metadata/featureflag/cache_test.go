//go:build !gitaly_test_sha256

package featureflag

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/tick"
)

func TestGetCache(t *testing.T) {
	defaultCache := GetCache()
	require.IsType(t, NoopCache{}, defaultCache)
}

func TestSetCache(t *testing.T) {
	oldCache := GetCache()
	t.Cleanup(func() {
		SetCache(oldCache)
	})
	SetCache(NewRefreshableCache(createContext(), logrus.New(), stubProvider{}))
	require.NotEqual(t, oldCache, GetCache())
}

func TestRefreshableCache_Get(t *testing.T) {
	t.Parallel()
	ctx := createContext()
	const flagName = "f1"
	for _, tc := range []struct {
		desc     string
		provider Provider
		expValue bool
		expFound bool
	}{
		{
			desc:     "not found",
			provider: stubProvider{},
			expValue: false,
			expFound: false,
		},
		{
			desc: "found, disabled",
			provider: stubProvider{getAll: func(context.Context) (map[string]bool, error) {
				return map[string]bool{flagName: false}, nil
			}},
			expValue: false,
			expFound: true,
		},
		{
			desc: "found, enabled",
			provider: stubProvider{getAll: func(context.Context) (map[string]bool, error) {
				return map[string]bool{flagName: true}, nil
			}},
			expValue: true,
			expFound: true,
		},
		{
			desc: "error from provider on initialisation",
			provider: stubProvider{getAll: func(context.Context) (map[string]bool, error) {
				return nil, assert.AnError
			}},
			expValue: false,
			expFound: false,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			rcache := NewRefreshableCache(ctx, logrus.New(), tc.provider)
			res, ok := rcache.Get(ctx, flagName)
			require.Equal(t, tc.expFound, ok)
			require.Equal(t, tc.expValue, res)
		})
	}

	t.Run("error from provider on refresh", func(t *testing.T) {
		var called int
		provider := stubProvider{getAll: func(context.Context) (map[string]bool, error) {
			t.Helper()
			called++
			switch called {
			case 1:
				return map[string]bool{flagName: true}, nil
			case 2:
				return nil, assert.AnError
			case 3:
				require.FailNow(t, "unexpected GetAll call on the Provider")
			}
			return nil, nil
		}}
		rcache := NewRefreshableCache(ctx, logrus.New(), provider)
		res1, _ := rcache.Get(ctx, flagName)
		require.True(t, res1)
		rcache.refresh(ctx) // we don't start background refresh that is why it is done manually
		res2, _ := rcache.Get(ctx, flagName)
		require.Equal(t, res1, res2, "the old value shouldn't change")
	})
}

func TestRefreshableCache_RefreshLoop(t *testing.T) {
	t.Parallel()
	const flagName = "f1"
	var called int
	provider := stubProvider{getAll: func(context.Context) (map[string]bool, error) {
		t.Helper()
		called++
		switch called {
		case 1: // initialisation fetch
			return map[string]bool{flagName: false}, nil
		case 2: // first refresh
			return nil, assert.AnError
		case 3:
			return map[string]bool{flagName: true}, nil
		case 4:
			require.FailNow(t, "unexpected GetAll call on the Provider")
		}
		return nil, nil
	}}

	refreshDone := make(chan any)
	ticker := tick.NewManualTicker()
	ticker.ResetFunc = func() {
		refreshDone <- struct{}{}
	}

	ctx := createContext()
	refreshLoopCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	rcache := NewRefreshableCache(ctx, logrus.New(), provider)
	// No refresh done, value after initialization.
	res, ok := rcache.Get(ctx, flagName)
	require.True(t, ok)
	require.False(t, res)

	refreshLoopDone := make(chan any)
	go func() {
		defer close(refreshLoopDone)
		rcache.RefreshLoop(refreshLoopCtx, ticker)
	}()
	// Consumption from the channel unblocks ticker.Reset() done before loop starts.
	<-refreshDone

	// Refresh done, but because Provider returned an error, no data changes.
	ticker.Tick()
	<-refreshDone
	res, ok = rcache.Get(ctx, flagName)
	require.True(t, ok)
	require.False(t, res)

	// Refresh done, Provider returned a new data.
	ticker.Tick()
	<-refreshDone
	res, ok = rcache.Get(ctx, flagName)
	require.True(t, ok)
	require.True(t, res)

	cancel()
	require.Eventually(t, func() bool {
		select {
		case <-refreshLoopDone:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond*10)
}

type stubProvider struct {
	getAll func(ctx context.Context) (map[string]bool, error)
}

func (sp stubProvider) GetAll(ctx context.Context) (map[string]bool, error) {
	if sp.getAll == nil {
		return nil, nil
	}
	return sp.getAll(ctx)
}
