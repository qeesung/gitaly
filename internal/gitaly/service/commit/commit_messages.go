package commit

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

func (s *server) GetCommitMessages(request *gitalypb.GetCommitMessagesRequest, stream gitalypb.CommitService_GetCommitMessagesServer) error {
	if err := validateGetCommitMessagesRequest(stream.Context(), s.locator, request); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}
	if err := s.getAndStreamCommitMessages(request, stream); err != nil {
		return structerr.NewInternal("%w", err)
	}

	return nil
}

func (s *server) getAndStreamCommitMessages(request *gitalypb.GetCommitMessagesRequest, stream gitalypb.CommitService_GetCommitMessagesServer) error {
	ctx := stream.Context()
	repo := s.localrepo(request.GetRepository())

	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return err
	}
	defer cancel()

	for _, commitID := range request.GetCommitIds() {
		msg, err := catfile.GetCommitMessage(ctx, objectReader, repo, git.Revision(commitID))
		if err != nil {
			return fmt.Errorf("failed to get commit message: %w", err)
		}
		msgReader := bytes.NewReader(msg)

		if err := stream.Send(&gitalypb.GetCommitMessagesResponse{CommitId: commitID}); err != nil {
			return err
		}
		sw := streamio.NewWriter(func(p []byte) error {
			return stream.Send(&gitalypb.GetCommitMessagesResponse{Message: p})
		})
		_, err = io.Copy(sw, msgReader)
		if err != nil {
			return fmt.Errorf("failed to send response: %w", err)
		}
	}
	return nil
}

func validateGetCommitMessagesRequest(ctx context.Context, locator storage.Locator, request *gitalypb.GetCommitMessagesRequest) error {
	return locator.ValidateRepository(ctx, request.GetRepository())
}
