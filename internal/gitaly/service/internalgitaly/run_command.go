package internalgitaly

import (
	"bytes"
	"context"

	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) RunCommand(
	ctx context.Context,
	req *gitalypb.RunCommandRequest,
) (*gitalypb.RunCommandResponse, error) {
	repo := req.GetRepository()
	gitCmdParams := req.GetGitCommand()

	if repo == nil {
		return nil, structerr.NewInvalidArgument("repository cannot be empty")
	}

	var flags []git.Option

	for _, flag := range gitCmdParams.GetFlags() {
		flags = append(flags, &git.Flag{Name: flag})
	}

	var stdout, stderr bytes.Buffer

	cmd, err := s.gitCmdFactory.New(ctx, repo, git.Command{
		Name:        gitCmdParams.GetName(),
		Action:      gitCmdParams.GetAction(),
		Flags:       flags,
		Args:        gitCmdParams.GetArgs(),
		PostSepArgs: gitCmdParams.GetPostSeparatorArgs(),
	}, git.WithStdout(&stdout), git.WithStderr(&stderr),
	)
	if err != nil {
		return nil, structerr.NewInternal("error creating command: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		exitCode, found := command.ExitStatus(err)
		if found {
			return &gitalypb.RunCommandResponse{
				ReturnCode:  int32(exitCode),
				Output:      stdout.Bytes(),
				ErrorOutput: stderr.Bytes(),
			}, nil
		}

		return nil, structerr.NewInternal("error running command: %w", err)
	}

	return &gitalypb.RunCommandResponse{
		Output:      []byte(text.ChompBytes(stdout.Bytes())),
		ErrorOutput: []byte(text.ChompBytes(stderr.Bytes())),
	}, nil
}
