package hook

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitlabshell"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func (s *server) PreReceiveHook(ctx context.Context, in *gitalypb.PreReceiveHookRequest) (*gitalypb.PreReceiveHookResponse, error) {
	preReceiveHookPath := filepath.Join(config.Config.Ruby.Dir, "gitlab-shell", "hooks", "pre-receive")

	repoPath, err := helper.GetRepoPath(in.GetRepository())
	if err != nil {
		return nil, helper.ErrInternal(err)
	}

	env := append(gitlabshell.Env(), []string{
		fmt.Sprintf("GL_ID=%s", in.GetKeyId()),
		fmt.Sprintf("GL_PROTOCOL=%s", in.GetProtocol()),
		fmt.Sprintf("GL_REPO_PATH=%s", repoPath),
		fmt.Sprintf("GL_REPOSITORY=%s", in.GetRepository().GetGlRepository()),
	}...)

	var stderr, stdout bytes.Buffer
	cmd, err := command.New(ctx, exec.Command(preReceiveHookPath), bytes.NewBuffer(in.GetStdin()), &stdout, &stderr, env...)
	if err != nil {
		return nil, helper.ErrInternal(err)
	}

	success := true

	// handle an error from the ruby hook by setting success = false
	if err = cmd.Wait(); err != nil {
		grpc_logrus.Extract(ctx).WithError(err).Error("failed to run git pre receive hook")
		success = false
	}

	return &gitalypb.PreReceiveHookResponse{
		Success: success,
		Stdout:  stdout.Bytes(),
		Stderr:  stderr.Bytes(),
	}, nil
}
