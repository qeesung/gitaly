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

func (s *server) UpdateHook(ctx context.Context, in *gitalypb.UpdateHookRequest) (*gitalypb.UpdateHookResponse, error) {
	updateHookPath := filepath.Join(config.Config.Ruby.Dir, "gitlab-shell", "hooks", "update")

	repoPath, err := helper.GetRepoPath(in.GetRepository())
	if err != nil {
		return nil, helper.ErrInternal(err)
	}

	env := append(gitlabshell.Env(), []string{
		fmt.Sprintf("GL_ID=%s", in.GetKeyId()),
		fmt.Sprintf("GL_REPO_PATH=%s", repoPath),
	}...)

	var stderr, stdout bytes.Buffer
	cmd, err := command.New(ctx, exec.Command(updateHookPath, string(in.GetRef()), in.GetOldValue(), in.GetNewValue()), nil, &stdout, &stderr, env...)
	if err != nil {
		return nil, helper.ErrInternal(err)
	}

	success := true

	// handle an error from the ruby hook by setting success = false
	if err = cmd.Wait(); err != nil {
		grpc_logrus.Extract(ctx).WithError(err).Error("failed to run git update hook")
		success = false
	}

	return &gitalypb.UpdateHookResponse{
		Success: success,
		Stdout:  stdout.Bytes(),
		Stderr:  stderr.Bytes(),
	}, nil
}
