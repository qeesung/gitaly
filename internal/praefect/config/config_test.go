package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigValidation(t *testing.T) {
	gitalySrvs := []*GitalyServer{&GitalyServer{"test", "localhost:23456"}}

	testCases := []struct {
		desc   string
		config Config
		err    error
	}{
		{
			desc:   "No ListenAddr",
			config: Config{ListenAddr: "", GitalyServers: gitalySrvs},
			err:    errNoListenAddr,
		},
		{
			desc:   "No servers",
			config: Config{ListenAddr: "localhost:1234", GitalyServers: nil},
			err:    errNoGitalyServers,
		},
		{
			desc:   "duplicate address",
			config: Config{ListenAddr: "localhost:1234", GitalyServers: []*GitalyServer{gitalySrvs[0], gitalySrvs[0]}},
			err:    errDuplicateGitalyAddr,
		},
		{
			desc:   "Valid config",
			config: Config{ListenAddr: "localhost:1234", GitalyServers: gitalySrvs},
			err:    nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t1 *testing.T) {
			err := tc.config.Validate()
			assert.Equal(t, err, tc.err)
		})
	}
}
