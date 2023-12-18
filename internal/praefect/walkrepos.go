package praefect

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
)

// WalkReposHandler implements an interceptor for the WalkRepos RPC, invoked when calling
// through Praefect. Instead of walking the storage directory in the filesystem, this Praefect
// implementation queries the database for all known repositories in the given virtual storage.
// As a consequence, the modification_time parameter can't be populated in the response.
func WalkReposHandler(rs datastore.RepositoryStore) grpc.StreamHandler {
	return func(srv interface{}, stream grpc.ServerStream) error {
		sendRepo := func(relPath string) error {
			return stream.SendMsg(&gitalypb.WalkReposResponse{
				RelativePath: relPath,
			})
		}

		var req gitalypb.WalkReposRequest
		if err := stream.RecvMsg(&req); err != nil {
			return fmt.Errorf("receive request: %w", err)
		}

		if req.StorageName == "" {
			return structerr.NewInvalidArgument("%w", storage.ErrStorageNotSet)
		}

		repos, err := rs.ListRepositoryPaths(stream.Context(), req.StorageName)
		if err != nil {
			return structerr.NewInternal("list repository paths: %w", err)
		}

		for _, repo := range repos {
			if err := sendRepo(repo); err != nil {
				return structerr.NewInternal("send repository path: %w", err)
			}
		}

		return nil
	}
}
