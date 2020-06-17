package info

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestDatalossCheck(t *testing.T) {
	for _, tc := range []struct {
		desc     string
		shard    nodes.Shard
		outdated map[string][]string
		response *gitalypb.DatalossCheckResponse
		error    error
	}{
		{
			desc: "no previous writable primary",
			shard: nodes.Shard{
				Primary: &nodes.MockNode{StorageName: "primary-storage"},
			},
			response: &gitalypb.DatalossCheckResponse{
				CurrentPrimary: "primary-storage",
			},
		},
		{
			desc: "multiple out of date",
			shard: nodes.Shard{
				PreviousWritablePrimary: &nodes.MockNode{StorageName: "previous-primary"},
				Primary:                 &nodes.MockNode{StorageName: "primary-storage"},
				IsReadOnly:              true,
			},
			outdated: map[string][]string{
				"repo-2": {"node-3"},
				"repo-1": {"node-1", "node-2"},
			},
			response: &gitalypb.DatalossCheckResponse{
				PreviousWritablePrimary: "previous-primary",
				CurrentPrimary:          "primary-storage",
				IsReadOnly:              true,
				OutdatedNodes: []*gitalypb.DatalossCheckResponse_Nodes{
					{RelativePath: "repo-1", Nodes: []string{"node-1", "node-2"}},
					{RelativePath: "repo-2", Nodes: []string{"node-3"}},
				},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			const virtualStorage = "test-virtual-storage"
			mgr := &nodes.MockManager{
				GetShardFunc: func(vs string) (nodes.Shard, error) {
					require.Equal(t, virtualStorage, vs)
					return tc.shard, nil
				},
			}

			rq := &datastore.MockReplicationEventQueue{
				GetOutdatedRepositoriesFunc: func(ctx context.Context, virtualStorage string, referenceStorage string) (map[string][]string, error) {
					return tc.outdated, nil
				},
			}

			srv := NewServer(mgr, config.Config{}, rq)
			resp, err := srv.DatalossCheck(context.Background(), &gitalypb.DatalossCheckRequest{
				VirtualStorage: virtualStorage,
			})
			require.Equal(t, tc.error, err)
			require.Equal(t, tc.response, resp)
		})
	}
}
