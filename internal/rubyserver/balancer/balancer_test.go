package balancer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRemovals(t *testing.T) {
	okActions := []action{
		{add: "foo"},
		{add: "bar"},
		{add: "qux"},
		{remove: "bar"},
		{add: "baz"},
		{remove: "foo"},
	}
	numAddr := 3
	removeDelay := 1 * time.Millisecond
	ConfigureBuilder(numAddr, removeDelay)

	testCases := []struct {
		desc      string
		actions   []action
		lastFails bool
		delay     time.Duration
	}{
		{
			desc:    "add then remove",
			actions: okActions,
			delay:   2 * removeDelay,
		},
		{
			desc:      "add then remove but too fast",
			actions:   okActions,
			lastFails: true,
			delay:     0,
		},
		{
			desc:      "remove one address too many",
			actions:   append(okActions, action{remove: "qux"}),
			lastFails: true,
			delay:     2 * removeDelay,
		},
		{
			desc: "remove unknown address",
			actions: []action{
				{add: "foo"},
				{add: "qux"},
				{add: "baz"},
				{remove: "bar"},
			},
			lastFails: true,
			delay:     2 * removeDelay,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			lbBuilder.testingRestart <- struct{}{}
			time.Sleep(2 * removeDelay) // wait for lastRemoval in monitor goroutine to be long enough ago

			for i, a := range tc.actions {
				if a.add != "" {
					AddAddress(a.add)
				} else {
					if tc.delay > 0 {
						time.Sleep(tc.delay)
					}

					expected := true
					if i+1 == len(tc.actions) && tc.lastFails {
						expected = false
					}

					require.Equal(t, expected, RemoveAddress(a.remove), "expected result from removing %q", a.remove)
				}
			}
		})
	}
}

type action struct {
	add    string
	remove string
}
