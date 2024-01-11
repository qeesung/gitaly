package praefect

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

// RemoveAllHandler intercepts RemoveAll calls, deletes the database records
// before calling each gitaly.
func RemoveAllHandler(rs datastore.RepositoryStore, conns Connections) grpc.StreamHandler {
	return func(_ interface{}, stream grpc.ServerStream) error {
		var req gitalypb.RemoveAllRequest
		if err := stream.RecvMsg(&req); err != nil {
			return fmt.Errorf("receive request: %w", err)
		}

		ctx := stream.Context()
		virtualStorage := req.StorageName

		if err := rs.DeleteAllRepositories(ctx, virtualStorage); err != nil {
			return fmt.Errorf("delete all db repositories: %w", err)
		}

		group, ctx := errgroup.WithContext(ctx)

		for storage, conn := range conns[virtualStorage] {
			rewrittenStorage := storage
			conn := conn

			group.Go(func() error {
				_, err := gitalypb.NewRepositoryServiceClient(conn).RemoveAll(ctx, &gitalypb.RemoveAllRequest{
					StorageName: rewrittenStorage,
				})
				return err
			})
		}

		if err := group.Wait(); err != nil {
			return err
		}

		return stream.SendMsg(&gitalypb.RemoveAllResponse{})
	}
}
