package info

import (
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

// MarkUnverified marks replicas as unverified. See the protobuf declarations for details.
func (s *Server) MarkUnverified(ctx context.Context, req *gitalypb.MarkUnverifiedRequest) (*gitalypb.MarkUnverifiedResponse, error) {
	var markUnverified func() (int64, error)
	switch selector := req.GetSelector().(type) {
	case *gitalypb.MarkUnverifiedRequest_RepositoryId:
		markUnverified = func() (int64, error) {
			return s.rs.MarkUnverified(ctx, selector.RepositoryId)
		}
	case *gitalypb.MarkUnverifiedRequest_VirtualStorage:
		markUnverified = func() (int64, error) {
			return s.rs.MarkVirtualStorageUnverified(ctx, selector.VirtualStorage)
		}
	case *gitalypb.MarkUnverifiedRequest_Storage_:
		markUnverified = func() (int64, error) {
			return s.rs.MarkStorageUnverified(ctx, selector.Storage.VirtualStorage, selector.Storage.Storage)
		}
	default:
		return nil, fmt.Errorf("unknown selector: %T", selector)
	}

	replicasMarked, err := markUnverified()
	if err != nil {
		return nil, fmt.Errorf("schedule verification: %w", err)
	}

	return &gitalypb.MarkUnverifiedResponse{ReplicasMarked: replicasMarked}, nil
}
