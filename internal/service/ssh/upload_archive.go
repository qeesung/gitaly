package ssh

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

func (s *server) SSHUploadArchive(stream gitalypb.SSHService_SSHUploadArchiveServer) error {
	req, err := stream.Recv() // First request contains Repository only
	if err != nil {
		return helper.ErrInternal(err)
	}
	if err = validateFirstUploadArchiveRequest(req); err != nil {
		return helper.ErrInvalidArgument(err)
	}

	if err = sshUploadArchive(stream, req); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func sshUploadArchive(stream gitalypb.SSHService_SSHUploadArchiveServer, req *gitalypb.SSHUploadArchiveRequest) error {
	repoPath, err := helper.GetRepoPath(req.Repository)
	if err != nil {
		return err
	}
	stdin := streamio.NewReader(func() ([]byte, error) {
		request, err := stream.Recv()
		return request.GetStdin(), err
	})
	stdout := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.SSHUploadArchiveResponse{Stdout: p})
	})
	stderr := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.SSHUploadArchiveResponse{Stderr: p})
	})

	cmd, err := git.BareCommand(stream.Context(), stdin, stdout, stderr, nil, "upload-archive", repoPath)

	if err != nil {
		return fmt.Errorf("start cmd: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		if status, ok := command.ExitStatus(err); ok {
			return stream.Send(&gitalypb.SSHUploadArchiveResponse{
				ExitStatus: &gitalypb.ExitStatus{Value: int32(status)},
			})
		}
		return fmt.Errorf("wait cmd: %v", err)
	}

	return stream.Send(&gitalypb.SSHUploadArchiveResponse{
		ExitStatus: &gitalypb.ExitStatus{Value: 0},
	})
}

func validateFirstUploadArchiveRequest(req *gitalypb.SSHUploadArchiveRequest) error {
	if req.Stdin != nil {
		return fmt.Errorf("non-empty stdin in first request")
	}

	return nil
}
