package hook

import (
	"context"
	"errors"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func validateReferenceTransactionHookRequest(ctx context.Context, locator storage.Locator, in *gitalypb.ReferenceTransactionHookRequest) error {
	return locator.ValidateRepository(ctx, in.GetRepository())
}

func (s *server) ReferenceTransactionHook(stream gitalypb.HookService_ReferenceTransactionHookServer) error {
	request, err := stream.Recv()
	if err != nil {
		return structerr.NewInternal("receiving first request: %w", err)
	}

	if err := validateReferenceTransactionHookRequest(stream.Context(), s.locator, request); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	var state hook.ReferenceTransactionState
	switch request.State {
	case gitalypb.ReferenceTransactionHookRequest_PREPARED:
		state = hook.ReferenceTransactionPrepared
	case gitalypb.ReferenceTransactionHookRequest_COMMITTED:
		state = hook.ReferenceTransactionCommitted
	case gitalypb.ReferenceTransactionHookRequest_ABORTED:
		state = hook.ReferenceTransactionAborted
	default:
		return structerr.NewInvalidArgument("invalid hook state")
	}

	stdin := streamio.NewReader(func() ([]byte, error) {
		req, err := stream.Recv()
		return req.GetStdin(), err
	})

	if err := s.manager.ReferenceTransactionHook(
		stream.Context(),
		state,
		request.GetEnvironmentVariables(),
		stdin,
	); err != nil {
		switch {
		case errors.Is(err, transaction.ErrTransactionAborted):
			return structerr.NewAborted("reference-transaction hook: %w", err)
		case errors.Is(err, transaction.ErrTransactionStopped):
			return structerr.NewFailedPrecondition("reference-transaction hook: %w", err)
		default:
			return structerr.NewInternal("reference-transaction hook: %w", err)
		}
	}

	if err := stream.Send(&gitalypb.ReferenceTransactionHookResponse{
		ExitStatus: &gitalypb.ExitStatus{Value: 0},
	}); err != nil {
		return structerr.NewInternal("sending response: %w", err)
	}

	return nil
}
