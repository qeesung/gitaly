package praefect

import (
	"errors"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/commonerr"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc"
)

type renamePeeker struct {
	grpc.ServerStream
	peeked *gitalypb.RenameRepositoryRequest
}

func (peeker *renamePeeker) RecvMsg(msg interface{}) error {
	// On the first read, we'll return the peeked first message of the stream.
	if peeker.peeked != nil {
		peeked := peeker.peeked
		peeker.peeked = nil

		codec := proxy.NewCodec()
		payload, err := codec.Marshal(peeked)
		if err != nil {
			return fmt.Errorf("marshaling peeked rename request: %w", err)
		}

		return codec.Unmarshal(payload, msg)
	}

	return peeker.ServerStream.RecvMsg(msg)
}

func validateRenameRepositoryRequest(req *gitalypb.RenameRepositoryRequest, virtualStorages map[string]struct{}) error {
	// These checks are not strictly necessary but they exist to keep retain compatibility with
	// Gitaly's tested behavior.
	if req.GetRepository() == nil {
		return helper.ErrInvalidArgumentf("empty Repository")
	} else if req.GetRelativePath() == "" {
		return helper.ErrInvalidArgumentf("destination relative path is empty")
	} else if _, ok := virtualStorages[req.GetRepository().GetStorageName()]; !ok {
		return helper.ErrInvalidArgumentf("GetStorageByName: no such storage: %q", req.GetRepository().GetStorageName())
	} else if _, err := storage.ValidateRelativePath("/fake-root", req.GetRelativePath()); err != nil {
		// Gitaly uses ValidateRelativePath to verify there are no traversals, so we use the same function
		// here. Praefect is not susceptible to path traversals as it generates its own disk paths but we
		// do this to retain API compatibility with Gitaly. ValidateRelativePath checks for traversals by
		// seeing whether the relative path escapes the root directory. It's not possible to traverse up
		// from the /, so the traversals in the path wouldn't be caught. To allow for the check to work,
		// we use the /fake-root directory simply to notice if there were traversals in the path.
		return helper.ErrInvalidArgumentf("GetRepoPath: %s", err)
	}

	return nil
}

// RenameRepositoryFeatureFlagger decides whether Praefect should handle the rename request or whether it should
// be proxied to a Gitaly. Rolling out Praefect generated replica paths is difficult as the atomicity fixes depend on the
// unique replica paths. If the unique replica paths are disabled, the in-place rename handling makes no longer sense either.
// Since they don't work isolation, this method decides which handling is used based on whether the repository is using a Praefect
// generated replica path or not. Repositories with client set paths are handled non-atomically by proxying to Gitalys.
// The Praefect generated paths are always handled with the atomic handling, regardless whether the feature flag is disabled
// later.
//
// This function peeks the first request and forwards the call either to a Gitaly or handles it in Praefect. This requires
// peeking into the internals of the proxying so we can set restore the frame correctly.
func RenameRepositoryFeatureFlagger(virtualStorageNames []string, rs datastore.RepositoryStore, handleRenameRepository grpc.StreamHandler) grpc.StreamServerInterceptor {
	virtualStorages := make(map[string]struct{}, len(virtualStorageNames))
	for _, virtualStorage := range virtualStorageNames {
		virtualStorages[virtualStorage] = struct{}{}
	}

	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod != "/gitaly.RepositoryService/RenameRepository" {
			return handler(srv, stream)
		}

		// Peek the message
		var request gitalypb.RenameRepositoryRequest
		if err := stream.RecvMsg(&request); err != nil {
			return fmt.Errorf("peek rename repository request: %w", err)
		}

		// In order for the handlers to work after the message is peeked, the stream is restored
		// with an alternative implementation that returns the first message correctly.
		stream = &renamePeeker{ServerStream: stream, peeked: &request}

		if err := validateRenameRepositoryRequest(&request, virtualStorages); err != nil {
			return err
		}

		repo := request.GetRepository()
		repositoryID, err := rs.GetRepositoryID(stream.Context(), repo.GetStorageName(), repo.GetRelativePath())
		if err != nil {
			if errors.As(err, new(commonerr.RepositoryNotFoundError)) {
				return helper.ErrNotFoundf("GetRepoPath: not a git repository: \"%s/%s\"", repo.GetStorageName(), repo.GetRelativePath())
			}

			return fmt.Errorf("get repository id: %w", err)
		}

		replicaPath, err := rs.GetReplicaPath(stream.Context(), repositoryID)
		if err != nil {
			return fmt.Errorf("get replica path: %w", err)
		}

		// Repositories that do not have a Praefect generated replica path are always handled in the old manner.
		// Once the feature flag is removed, all of the repositories will be handled in the atomic manner.
		if !strings.HasPrefix(replicaPath, "@cluster") {
			return handler(srv, stream)
		}

		return handleRenameRepository(srv, stream)
	}
}

// RenameRepositoryHandler handles /gitaly.RepositoryService/RenameRepository calls by renaming
// the repository in the lookup table stored in the database.
func RenameRepositoryHandler(virtualStoragesNames []string, rs datastore.RepositoryStore) grpc.StreamHandler {
	virtualStorages := make(map[string]struct{}, len(virtualStoragesNames))
	for _, virtualStorage := range virtualStoragesNames {
		virtualStorages[virtualStorage] = struct{}{}
	}

	return func(srv interface{}, stream grpc.ServerStream) error {
		var req gitalypb.RenameRepositoryRequest
		if err := stream.RecvMsg(&req); err != nil {
			return fmt.Errorf("receive request: %w", err)
		}

		if err := validateRenameRepositoryRequest(&req, virtualStorages); err != nil {
			return err
		}

		if err := rs.RenameRepositoryInPlace(stream.Context(),
			req.GetRepository().GetStorageName(),
			req.GetRepository().GetRelativePath(),
			req.GetRelativePath(),
		); err != nil {
			if errors.Is(err, commonerr.ErrRepositoryNotFound) {
				return helper.ErrNotFoundf(
					`GetRepoPath: not a git repository: "%s/%s"`,
					req.GetRepository().GetStorageName(),
					req.GetRepository().GetRelativePath(),
				)
			} else if errors.Is(err, commonerr.ErrRepositoryAlreadyExists) {
				return helper.ErrAlreadyExistsf("target repo exists already")
			}

			return helper.ErrInternal(err)
		}

		return stream.SendMsg(&gitalypb.RenameRepositoryResponse{})
	}
}
