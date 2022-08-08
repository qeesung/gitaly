//go:build !gitaly_test_sha256

package praefect

import (
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/nodes"
)

// generates a praefect configuration with the specified number of backend
// nodes
func testConfig(backends int) config.Config {
	var nodes []*config.Node

	for i := 0; i < backends; i++ {
		n := &config.Node{
			Storage: fmt.Sprintf("praefect-internal-%d", i),
			Token:   fmt.Sprintf("%d", i),
		}

		nodes = append(nodes, n)
	}
	cfg := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name:  "praefect",
				Nodes: nodes,
			},
		},
	}

	return cfg
}

type nullNodeMgr struct{}

func (nullNodeMgr) GetShard(ctx context.Context, virtualStorageName string) (nodes.Shard, error) {
	return nodes.Shard{Primary: &nodes.MockNode{}}, nil
}

func (nullNodeMgr) GetSyncedNode(ctx context.Context, virtualStorageName, repoPath string) (nodes.Node, error) {
	return nil, nil
}

func (nullNodeMgr) HealthyNodes() map[string][]string {
	return nil
}

func (nullNodeMgr) Nodes() map[string][]nodes.Node {
	return nil
}
