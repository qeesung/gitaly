package ssh

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/tick"
	"gitlab.com/gitlab-org/gitaly/v15/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
)

func (s *server) SSHUploadArchive(stream gitalypb.SSHService_SSHUploadArchiveServer) error {
	req, err := stream.Recv() // First request contains Repository only
	if err != nil {
		return structerr.NewInternal("%w", err)
	}
	if err = validateFirstUploadArchiveRequest(req); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	if err = s.sshUploadArchive(stream, req); err != nil {
		return structerr.NewInternal("%w", err)
	}

	return nil
}

func (s *server) sshUploadArchive(stream gitalypb.SSHService_SSHUploadArchiveServer, req *gitalypb.SSHUploadArchiveRequest) error {
	ctx, cancelCtx := context.WithCancel(stream.Context())
	defer cancelCtx()

	repoPath, err := s.locator.GetRepoPath(req.Repository)
	if err != nil {
		return err
	}

	stdin := streamio.NewReader(func() ([]byte, error) {
		request, err := stream.Recv()
		return request.GetStdin(), err
	})

	var m sync.Mutex
	stdout := streamio.NewSyncWriter(&m, func(p []byte) error {
		return stream.Send(&gitalypb.SSHUploadArchiveResponse{Stdout: p})
	})
	stderr := streamio.NewSyncWriter(&m, func(p []byte) error {
		return stream.Send(&gitalypb.SSHUploadArchiveResponse{Stderr: p})
	})

	cmd, monitor, err := monitorStdinCommand(ctx, s.gitCmdFactory, req.GetRepository(), stdin, stdout, stderr, git.Command{
		Name: "upload-archive",
		Args: []string{repoPath},
	})
	if err != nil {
		return err
	}

	timeoutTicker := tick.NewTimerTicker(s.uploadArchiveRequestTimeout)

	// upload-archive expects a list of options terminated by a flush packet:
	// https://github.com/git/git/blob/v2.22.0/builtin/upload-archive.c#L38
	//
	// Place a timeout on receiving the flush packet to mitigate use-after-check
	// attacks
	go monitor.Monitor(ctx, pktline.PktFlush(), timeoutTicker, cancelCtx)

	if err := cmd.Wait(); err != nil {
		// When waiting for the packfile negotiation to end times out we'll cancel the local
		// context, but not cancel the overall RPC's context. Our statushandler middleware
		// thus cannot observe the fact that we're cancelling the context, and neither do we
		// provide any valuable information to the caller that we do indeed kill the command
		// because of our own internal timeout.
		//
		// We thus need to special-case the situation where we cancel our own context in
		// order to provide that information and return a proper gRPC error code.
		if ctx.Err() != nil && stream.Context().Err() == nil {
			return structerr.NewDeadlineExceeded("waiting for packfile negotiation: %w", ctx.Err())
		}

		if status, ok := command.ExitStatus(err); ok {
			if sendErr := stream.Send(&gitalypb.SSHUploadArchiveResponse{
				ExitStatus: &gitalypb.ExitStatus{Value: int32(status)},
			}); sendErr != nil {
				return sendErr
			}
			return fmt.Errorf("send: %w", err)
		}
		return fmt.Errorf("wait cmd: %w", err)
	}

	return stream.Send(&gitalypb.SSHUploadArchiveResponse{
		ExitStatus: &gitalypb.ExitStatus{Value: 0},
	})
}

func validateFirstUploadArchiveRequest(req *gitalypb.SSHUploadArchiveRequest) error {
	if err := service.ValidateRepository(req.GetRepository()); err != nil {
		return err
	}
	if req.Stdin != nil {
		return errors.New("non-empty stdin in first request")
	}

	return nil
}
