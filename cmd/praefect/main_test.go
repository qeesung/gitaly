package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
)

func TestNoConfigFlag(t *testing.T) {
	_, err := initConfig()

	assert.Equal(t, err, errNoConfigFile)
}

func TestFlattenNodes(t *testing.T) {
	for _, tt := range []struct {
		desc   string
		conf   config.Config
		expect map[string]*nodePing
	}{
		{
			desc: "Flatten common address between storages",
			conf: config.Config{
				VirtualStorages: []*config.VirtualStorage{
					{
						Name: "meow",
						Nodes: []*config.Node{
							{
								Storage:        "foo",
								Address:        "tcp://example.com",
								Token:          "abc",
								DefaultPrimary: true,
							},
						},
					},
					{
						Name: "woof",
						Nodes: []*config.Node{
							{
								Storage:        "bar",
								Address:        "tcp://example.com",
								Token:          "abc",
								DefaultPrimary: true,
							},
						},
					},
				},
			},
			expect: map[string]*nodePing{
				"tcp://example.com": &nodePing{
					address: "tcp://example.com",
					storages: map[gitalyStorage][]virtualStorage{
						"foo": []virtualStorage{"meow"},
						"bar": []virtualStorage{"woof"},
					},
					vStorages: map[virtualStorage]struct{}{
						"meow": struct{}{},
						"woof": struct{}{},
					},
					token: "abc",
				},
			},
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			actual := flattenNodes(tt.conf)
			require.Equal(t, tt.expect, actual)
		})
	}
}
