package info

import (
	"context"
	"sort"

	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func (s *Server) DatalossCheck(ctx context.Context, req *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error) {
	shard, err := s.nodeMgr.GetShard(req.VirtualStorage)
	if err != nil {
		return nil, err
	}

	if shard.PreviousWritablePrimary == nil {
		return &gitalypb.DatalossCheckResponse{
			CurrentPrimary: shard.Primary.GetStorage(),
			IsReadOnly:     shard.IsReadOnly,
		}, nil
	}

	repos, err := s.queue.GetOutdatedRepositories(ctx, req.GetVirtualStorage(), shard.PreviousWritablePrimary.GetStorage())
	if err != nil {
		return nil, err
	}

	outdatedNodes := make([]*gitalypb.DatalossCheckResponse_Nodes, 0, len(repos))
	for repo, nodes := range repos {
		outdatedNodes = append(outdatedNodes, &gitalypb.DatalossCheckResponse_Nodes{
			RelativePath: repo,
			Nodes:        nodes,
		})
	}

	sort.Slice(outdatedNodes, func(i, j int) bool { return outdatedNodes[i].RelativePath < outdatedNodes[j].RelativePath })

	return &gitalypb.DatalossCheckResponse{
		PreviousWritablePrimary: shard.PreviousWritablePrimary.GetStorage(),
		CurrentPrimary:          shard.Primary.GetStorage(),
		IsReadOnly:              shard.IsReadOnly,
		OutdatedNodes:           outdatedNodes,
	}, nil
}
