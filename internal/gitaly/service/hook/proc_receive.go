package hook

import (
	"fmt"

	gitalyhook "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func sendProcReceiveHookResponse(stream gitalypb.HookService_ProcReceiveHookServer, code int32, stderr string) error {
	if err := stream.Send(&gitalypb.ProcReceiveHookResponse{
		ExitStatus: &gitalypb.ExitStatus{Value: code},
		Stderr:     []byte(stderr),
	}); err != nil {
		return structerr.NewInternal("sending response: %w", err)
	}

	return nil
}

func (s *server) ProcReceiveHook(stream gitalypb.HookService_ProcReceiveHookServer) error {
	ctx := stream.Context()

	firstRequest, err := stream.Recv()
	if err != nil {
		return structerr.NewInternal("receiving first request: %w", err)
	}

	stdin := streamio.NewReader(func() ([]byte, error) {
		req, err := stream.Recv()
		return req.GetStdin(), err
	})

	stdout := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.ProcReceiveHookResponse{Stdout: p})
	})

	handler, doneCh, err := gitalyhook.NewProcReceiveHandler(
		firstRequest.GetEnvironmentVariables(), stdin, stdout, nil,
	)
	if err != nil {
		return structerr.NewInternal("creating handler: %w", err)
	}

	registry := s.manager.ProcReceiveRegistry()
	if err := registry.Transmit(ctx, handler); err != nil {
		return sendProcReceiveHookResponse(stream, 1, fmt.Sprintf("transmitting handler: %s", err))
	}

	if err := <-doneCh; err != nil {
		return sendProcReceiveHookResponse(stream, 1, fmt.Sprintf("handler finished: %s", err.Error()))
	}

	return sendProcReceiveHookResponse(stream, 0, "")
}
