package ssh

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

func (s *server) SSHUploadPack(stream gitalypb.SSHService_SSHUploadPackServer) error {
	req, err := stream.Recv() // First request contains Repository only
	if err != nil {
		return helper.ErrInternal(err)
	}

	if err = validateFirstUploadPackRequest(req); err != nil {
		return helper.ErrInvalidArgument(err)
	}

	if err = sshUploadPack(stream, req); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func sshUploadPack(stream gitalypb.SSHService_SSHUploadPackServer, req *gitalypb.SSHUploadPackRequest) error {
	ctx := stream.Context()

	stdin := streamio.NewReader(func() ([]byte, error) {
		request, err := stream.Recv()
		return request.GetStdin(), err
	})
	stdout := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.SSHUploadPackResponse{Stdout: p})
	})
	stderr := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.SSHUploadPackResponse{Stderr: p})
	})

	env := git.AddGitProtocolEnv(ctx, req, command.GitEnv)

	repoPath, err := helper.GetRepoPath(req.Repository)
	if err != nil {
		return err
	}

	git.WarnIfTooManyBitmaps(ctx, repoPath)

	args := []string{}

	for _, params := range req.GitConfigOptions {
		args = append(args, "-c", params)
	}

	args = append(args, "upload-pack", repoPath)

	cmd, err := git.BareCommand(ctx, stdin, stdout, stderr, env, args...)

	if err != nil {
		return fmt.Errorf("start cmd: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		if status, ok := command.ExitStatus(err); ok {
			return stream.Send(&gitalypb.SSHUploadPackResponse{
				ExitStatus: &gitalypb.ExitStatus{Value: int32(status)},
			})
		}
		return fmt.Errorf("cmd wait: %v", err)
	}

	return nil
}

func validateFirstUploadPackRequest(req *gitalypb.SSHUploadPackRequest) error {
	if req.Stdin != nil {
		return fmt.Errorf("non-empty stdin in first request")
	}

	return nil
}
