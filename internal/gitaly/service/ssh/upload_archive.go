package ssh

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func (s *server) SSHUploadArchive(stream gitalypb.SSHService_SSHUploadArchiveServer) error {
	req, err := stream.Recv() // First request contains Repository only
	if err != nil {
		return structerr.NewInternal("%w", err)
	}
	if err = validateFirstUploadArchiveRequest(stream.Context(), s.locator, req); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	if err = s.sshUploadArchive(stream, req); err != nil {
		return structerr.NewInternal("%w", err)
	}

	return nil
}

func (s *server) sshUploadArchive(stream gitalypb.SSHService_SSHUploadArchiveServer, req *gitalypb.SSHUploadArchiveRequest) error {
	ctx := stream.Context()

	repoPath, err := s.locator.GetRepoPath(ctx, req.Repository)
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

	timeoutTicker := s.uploadArchiveRequestTimeoutTickerFactory()

	if err := s.runUploadCommand(ctx, req.GetRepository(), stdin, stdout, stderr, timeoutTicker, pktline.PktFlush(), git.Command{
		Name: "upload-archive",
		Args: []string{repoPath},
	}); err != nil {
		if status, ok := command.ExitStatus(err); ok {
			if err := stream.Send(&gitalypb.SSHUploadArchiveResponse{
				ExitStatus: &gitalypb.ExitStatus{Value: int32(status)},
			}); err != nil {
				return fmt.Errorf("sending exit status: %w", err)
			}

			return fmt.Errorf("send: %w", err)
		}

		return fmt.Errorf("running upload-archive: %w", err)
	}

	return stream.Send(&gitalypb.SSHUploadArchiveResponse{
		ExitStatus: &gitalypb.ExitStatus{Value: 0},
	})
}

func validateFirstUploadArchiveRequest(ctx context.Context, locator storage.Locator, req *gitalypb.SSHUploadArchiveRequest) error {
	if err := locator.ValidateRepository(ctx, req.GetRepository()); err != nil {
		return err
	}
	if req.Stdin != nil {
		return errors.New("non-empty stdin in first request")
	}

	return nil
}
