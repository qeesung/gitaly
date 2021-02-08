package streamcache

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGrower_notify(t *testing.T) {
	g := &grower{}
	n := g.Subscribe()

	expectNotify(t, n, false)
	require.Equal(t, int64(0), g.Value())

	// Expect no notifications after values <= g.Value()
	g.Grow(-1)
	expectNotify(t, n, false)
	g.Grow(0)
	expectNotify(t, n, false)

	// Expect notification after g.Value() went up
	g.Grow(1)
	expectNotify(t, n, true)
	require.Equal(t, int64(1), g.Value())
}

func expectNotify(t *testing.T, n *notifier, expected bool) {
	t.Helper()
	select {
	case <-n.C:
		require.True(t, expected, "unexpected notification")
	default:
		require.False(t, expected, "expected notification, got none")
	}
}

func TestGrowerUnsubscribe(t *testing.T) {
	g := &grower{}
	n1 := g.Subscribe()
	n2 := g.Subscribe()

	require.True(t, g.HasSubscribers())

	g.Grow(1)
	expectNotify(t, n1, true)
	expectNotify(t, n2, true)

	g.Unsubscribe(n2)
	require.True(t, g.HasSubscribers())

	g.Grow(2)
	expectNotify(t, n1, true)
	expectNotify(t, n2, false)

	g.Unsubscribe(n1)
	require.False(t, g.HasSubscribers())

	expectNotify(t, n1, false)
	expectNotify(t, n2, false)
}
