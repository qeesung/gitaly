package diff

import (
	"context"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func (s *server) RawDiff(in *gitalypb.RawDiffRequest, stream gitalypb.DiffService_RawDiffServer) error {
	if err := validateRequest(stream.Context(), s.locator, in); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	subCmd := git.Command{
		Name:  "diff",
		Flags: []git.Option{git.Flag{Name: "--full-index"}},
		Args:  []string{in.LeftCommitId, in.RightCommitId},
	}

	sw := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.RawDiffResponse{Data: p})
	})

	return sendRawOutput(stream.Context(), s.gitCmdFactory, in.Repository, sw, subCmd)
}

func (s *server) RawPatch(in *gitalypb.RawPatchRequest, stream gitalypb.DiffService_RawPatchServer) error {
	if err := validateRequest(stream.Context(), s.locator, in); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	subCmd := git.Command{
		Name:  "format-patch",
		Flags: []git.Option{git.Flag{Name: "--stdout"}, git.ValueFlag{Name: "--signature", Value: "GitLab"}},
		Args:  []string{in.LeftCommitId + ".." + in.RightCommitId},
	}

	sw := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.RawPatchResponse{Data: p})
	})

	return sendRawOutput(stream.Context(), s.gitCmdFactory, in.Repository, sw, subCmd)
}

func sendRawOutput(ctx context.Context, gitCmdFactory git.CommandFactory, repo *gitalypb.Repository, sender io.Writer, subCmd git.Command) error {
	cmd, err := gitCmdFactory.New(ctx, repo, subCmd, git.WithSetupStdout())
	if err != nil {
		return structerr.NewInternal("cmd: %w", err)
	}

	if _, err := io.Copy(sender, cmd); err != nil {
		return structerr.NewAborted("send: %w", err)
	}

	return cmd.Wait()
}
