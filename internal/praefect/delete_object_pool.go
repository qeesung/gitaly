package praefect

import (
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/internal/git/objectpool"
	objectpoolsvc "gitlab.com/gitlab-org/gitaly/internal/gitaly/service/objectpool"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// DeleteObjectPoolHandler intercepts DeleteObjectPool calls, deletes the database records and
// deletes the object pool from every backing Gitaly node.
func DeleteObjectPoolHandler(rs datastore.RepositoryStore, conns Connections) grpc.StreamHandler {
	return removeRepositoryHandler(rs, conns,
		func(stream grpc.ServerStream) (*gitalypb.Repository, error) {
			var req gitalypb.DeleteObjectPoolRequest
			if err := stream.RecvMsg(&req); err != nil {
				return nil, fmt.Errorf("receive request: %w", err)
			}

			repo, err := objectpoolsvc.ExtractPool(&req)
			if err != nil {
				return nil, err
			}

			if !housekeeping.IsRailsPoolPath(repo.GetRelativePath()) {
				return nil, helper.ErrInvalidArgument(objectpool.ErrInvalidPoolDir)
			}

			return repo, nil
		},
		func(ctx context.Context, conn *grpc.ClientConn, rewritten *gitalypb.Repository) error {
			_, err := gitalypb.NewObjectPoolServiceClient(conn).DeleteObjectPool(ctx, &gitalypb.DeleteObjectPoolRequest{
				ObjectPool: &gitalypb.ObjectPool{
					Repository: rewritten,
				},
			})
			return err
		},
		func() proto.Message { return &gitalypb.DeleteObjectPoolResponse{} },
		false,
	)
}
