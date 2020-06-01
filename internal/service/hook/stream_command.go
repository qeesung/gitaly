package hook

import (
	"context"
	"io"
	"os/exec"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

func streamCommandResponse(
	ctx context.Context,
	stdin io.Reader,
	stdout, stderr io.Writer,
	c *exec.Cmd,
	env []string,
) (int32, error) {
	cmd, err := command.New(ctx, c, stdin, stdout, stderr, env...)
	if err != nil {
		return 1, helper.ErrInternal(err)
	}

	err = cmd.Wait()
	if err == nil {
		return 0, nil
	}

	if code, ok := command.ExitStatus(err); ok {
		return int32(code), nil
	}

	return 1, err
}
