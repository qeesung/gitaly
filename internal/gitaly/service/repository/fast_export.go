package repository

import (
	"bytes"
	"errors"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func (s *server) FastExport(
	req *gitalypb.FastExportRequest,
	stream gitalypb.RepositoryService_FastExportServer,
) error {
	ctx := stream.Context()

	if req.GetRepository() == nil {
		return structerr.NewInvalidArgument("repository is not set")
	}

	var stderr bytes.Buffer

	repo := s.localrepo(req.GetRepository())
	cmd, err := repo.Exec(ctx, git.Command{
		Name: "fast-export",
		Flags: []git.Option{
			git.Flag{Name: "--all"},
		},
	}, git.WithSetupStdout(), git.WithStderr(&stderr))
	if err != nil {
		return structerr.NewInternal("cmd start failed: %w", err).
			WithMetadata("stderr", stderr.String())
	}

	if err = sendFastExportChunked(cmd, stream); err != nil {
		return structerr.NewInternal("sending chunked response failed: %w", err).
			WithMetadata("stderr", stderr.String())
	}

	return nil
}

func sendFastExportChunked(
	cmd *command.Command,
	stream gitalypb.RepositoryService_FastExportServer,
) error {
	sw := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.FastExportResponse{Data: p})
	})

	for {
		_, err := io.CopyN(sw, cmd, int64(streamio.WriteBufferSize))
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}

	return nil
}
