package cleanup

import (
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"gitlab.com/gitlab-org/gitaly/internal/service/cleanup/internalrefs"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

func (s *server) ApplyBfgObjectMap(stream gitalypb.CleanupService_ApplyBfgObjectMapServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.Internal, "first request failed: %v", err)
	}

	repo := firstRequest.GetRepository()
	if repo == nil {
		return status.Errorf(codes.InvalidArgument, "empty repository")
	}

	firstRead := false
	reader := streamio.NewReader(func() ([]byte, error) {
		if !firstRead {
			firstRead = true
			return firstRequest.GetObjectMap(), nil
		}

		request, err := stream.Recv()
		return request.GetObjectMap(), err
	})

	ctx := stream.Context()

	// It doesn't matter if new internal references are added after this RPC
	// starts running - they shouldn't point to the objects removed by the BFG
	cleaner, err := internalrefs.NewCleaner(ctx, repo, nil)
	if err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}

	if err := cleaner.ApplyObjectMap(reader); err != nil {
		if invalidErr, ok := err.(internalrefs.ErrInvalidObjectMap); ok {
			return status.Errorf(codes.InvalidArgument, "%s", invalidErr)
		}

		return status.Errorf(codes.Internal, "%s", err)
	}

	return stream.SendAndClose(&gitalypb.ApplyBfgObjectMapResponse{})
}
