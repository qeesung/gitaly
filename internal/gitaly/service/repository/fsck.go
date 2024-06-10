package repository

import (
	"context"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) Fsck(ctx context.Context, req *gitalypb.FsckRequest) (*gitalypb.FsckResponse, error) {
	repository := req.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	var output strings.Builder
	cmd, err := s.gitCmdFactory.New(ctx, repository,
		git.Command{
			Name: "fsck",
			Flags: []git.Option{
				// We don't care about any progress bars.
				git.Flag{Name: "--no-progress"},
				// We don't want to get warning about dangling objects. It is
				// expected that repositories have these and makes the signal to
				// noise ratio a lot worse.
				git.Flag{Name: "--no-dangling"},
			},
		},
		git.WithStdout(&output),
		git.WithStderr(&output),
	)
	if err != nil {
		return nil, err
	}

	if err = cmd.Wait(); err != nil {
		return &gitalypb.FsckResponse{Error: []byte(output.String())}, nil
	}

	return &gitalypb.FsckResponse{}, nil
}
