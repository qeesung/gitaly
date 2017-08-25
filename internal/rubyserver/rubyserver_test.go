package rubyserver

import (
	"testing"
)

func TestStopSafe(t *testing.T) {
	badServers := []*Server{
		nil,
		&Server{},
	}

	for _, bs := range badServers {
		bs.Stop()
	}
}
