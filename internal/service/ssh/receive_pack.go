package ssh

import (
	"fmt"
	"strconv"

	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"

	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

func (s *server) SSHReceivePack(stream gitalypb.SSHService_SSHReceivePackServer) error {
	req, err := stream.Recv() // First request contains only Repository, GlId, and GlUsername
	if err != nil {
		return helper.ErrInternal(err)
	}

	grpc_logrus.Extract(stream.Context()).WithFields(log.Fields{
		"GlID":             req.GlId,
		"GlRepository":     req.GlRepository,
		"GlUsername":       req.GlUsername,
		"GitConfigOptions": req.GitConfigOptions,
	}).Debug("SSHReceivePack")

	if err = validateFirstReceivePackRequest(req); err != nil {
		return helper.ErrInvalidArgument(err)
	}

	if err := sshReceivePack(stream, req); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func sshReceivePack(stream gitalypb.SSHService_SSHReceivePackServer, req *gitalypb.SSHReceivePackRequest) error {
	ctx := stream.Context()

	stdin := streamio.NewReader(func() ([]byte, error) {
		request, err := stream.Recv()
		return request.GetStdin(), err
	})
	stdout := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.SSHReceivePackResponse{Stdout: p})
	})
	stderr := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.SSHReceivePackResponse{Stderr: p})
	})

	env := append(git.HookEnv(req), "GL_PROTOCOL=ssh")

	repoPath, err := helper.GetRepoPath(req.Repository)
	if err != nil {
		return err
	}

	env = git.AddGitProtocolEnv(ctx, req, env)
	env = append(env, command.GitEnv...)
	env = append(env, fmt.Sprintf("%s=%s", featureflag.HooksRPCEnvVar, strconv.FormatBool(featureflag.IsEnabled(ctx, featureflag.HooksRPC))))

	globalOpts := git.ReceivePackConfig()
	for _, o := range req.GitConfigOptions {
		globalOpts = append(globalOpts, git.ValueFlag{"-c", o})
	}

	cmd, err := git.SafeBareCmd(ctx, stdin, stdout, stderr, env, globalOpts, git.SubCmd{
		Name: "receive-pack",
		Args: []string{repoPath},
	})

	if err != nil {
		return fmt.Errorf("start cmd: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		if status, ok := command.ExitStatus(err); ok {
			return stream.Send(&gitalypb.SSHReceivePackResponse{
				ExitStatus: &gitalypb.ExitStatus{Value: int32(status)},
			})
		}

		return fmt.Errorf("cmd wait: %v", err)
	}

	return nil
}

func validateFirstReceivePackRequest(req *gitalypb.SSHReceivePackRequest) error {
	if req.GlId == "" {
		return fmt.Errorf("empty GlId")
	}
	if req.Stdin != nil {
		return fmt.Errorf("non-empty data in first request")
	}

	return nil
}
