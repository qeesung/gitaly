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

func (s *server) PreReceiveHook(stream gitalypb.HookService_PreReceiveHookServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return structerr.NewInternal("receiving first request: %w", err)
	}

	if err := validatePreReceiveHookRequest(stream.Context(), s.locator, firstRequest); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}
	repository := firstRequest.GetRepository()

	stdin := streamio.NewReader(func() ([]byte, error) {
		req, err := stream.Recv()
		return req.GetStdin(), err
	})

	var m sync.Mutex
	stdout := streamio.NewSyncWriter(&m, func(p []byte) error {
		return stream.Send(&gitalypb.PreReceiveHookResponse{Stdout: p})
	})
	stderr := streamio.NewSyncWriter(&m, func(p []byte) error {
		return stream.Send(&gitalypb.PreReceiveHookResponse{Stderr: p})
	})

	if err := s.manager.PreReceiveHook(
		stream.Context(),
		repository,
		firstRequest.GetGitPushOptions(),
		firstRequest.GetEnvironmentVariables(),
		stdin,
		stdout,
		stderr,
	); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return preReceiveHookResponse(stream, int32(exitError.ExitCode()), "")
		}

		return preReceiveHookResponse(stream, 1, fmt.Sprintf("%s", err))
	}

	return preReceiveHookResponse(stream, 0, "")
}

func validatePreReceiveHookRequest(ctx context.Context, locator storage.Locator, in *gitalypb.PreReceiveHookRequest) error {
	return locator.ValidateRepository(ctx, in.GetRepository())
}

func preReceiveHookResponse(stream gitalypb.HookService_PreReceiveHookServer, code int32, stderr string) error {
	if err := stream.Send(&gitalypb.PreReceiveHookResponse{
		ExitStatus: &gitalypb.ExitStatus{Value: code},
		Stderr:     []byte(stderr),
	}); err != nil {
		return structerr.NewInternal("sending response: %w", err)
	}

	return nil
}
