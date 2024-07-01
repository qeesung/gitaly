package hook

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func postReceiveHookResponse(stream gitalypb.HookService_PostReceiveHookServer, code int32, stderr string) error {
	if err := stream.Send(&gitalypb.PostReceiveHookResponse{
		ExitStatus: &gitalypb.ExitStatus{Value: code},
		Stderr:     []byte(stderr),
	}); err != nil {
		return structerr.NewInternal("sending response: %w", err)
	}

	return nil
}

func (s *server) PostReceiveHook(stream gitalypb.HookService_PostReceiveHookServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	if err := validatePostReceiveHookRequest(stream.Context(), s.locator, firstRequest); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	stdin := streamio.NewReader(func() ([]byte, error) {
		req, err := stream.Recv()
		return req.GetStdin(), err
	})

	var m sync.Mutex
	stdout := streamio.NewSyncWriter(&m, func(p []byte) error {
		return stream.Send(&gitalypb.PostReceiveHookResponse{Stdout: p})
	})
	stderr := streamio.NewSyncWriter(&m, func(p []byte) error {
		return stream.Send(&gitalypb.PostReceiveHookResponse{Stderr: p})
	})

	if err := s.manager.PostReceiveHook(
		stream.Context(),
		firstRequest.Repository,
		firstRequest.GetGitPushOptions(),
		firstRequest.GetEnvironmentVariables(),
		stdin,
		stdout,
		stderr,
	); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return postReceiveHookResponse(stream, int32(exitError.ExitCode()), "")
		}

		return postReceiveHookResponse(stream, 1, fmt.Sprintf("%s", err))
	}

	return postReceiveHookResponse(stream, 0, "")
}

func validatePostReceiveHookRequest(ctx context.Context, locator storage.Locator, in *gitalypb.PostReceiveHookRequest) error {
	return locator.ValidateRepository(ctx, in.GetRepository())
}
