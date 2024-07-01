package ref

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func (s *server) GetTagMessages(request *gitalypb.GetTagMessagesRequest, stream gitalypb.RefService_GetTagMessagesServer) error {
	if err := validateGetTagMessagesRequest(stream.Context(), s.locator, request); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}
	if err := s.getAndStreamTagMessages(request, stream); err != nil {
		return structerr.NewInternal("%w", err)
	}

	return nil
}

func validateGetTagMessagesRequest(ctx context.Context, locator storage.Locator, request *gitalypb.GetTagMessagesRequest) error {
	return locator.ValidateRepository(ctx, request.GetRepository())
}

func (s *server) getAndStreamTagMessages(request *gitalypb.GetTagMessagesRequest, stream gitalypb.RefService_GetTagMessagesServer) error {
	ctx := stream.Context()
	repo := s.localrepo(request.GetRepository())

	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return fmt.Errorf("creating object reader: %w", err)
	}
	defer cancel()

	for _, tagID := range request.GetTagIds() {
		tag, err := catfile.GetTag(ctx, objectReader, git.Revision(tagID), "")
		if err != nil {
			return fmt.Errorf("getting tag: %w", err)
		}

		if err := stream.Send(&gitalypb.GetTagMessagesResponse{TagId: tagID}); err != nil {
			return err
		}
		sw := streamio.NewWriter(func(p []byte) error {
			return stream.Send(&gitalypb.GetTagMessagesResponse{Message: p})
		})

		msgReader := bytes.NewReader(tag.Message)

		if _, err = io.Copy(sw, msgReader); err != nil {
			return fmt.Errorf("failed to send response: %w", err)
		}
	}
	return nil
}
