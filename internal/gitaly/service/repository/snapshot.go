package repository

import (
	"errors"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func (s *server) GetSnapshot(in *gitalypb.GetSnapshotRequest, stream gitalypb.RepositoryService_GetSnapshotServer) error {
	if err := s.locator.ValidateRepository(stream.Context(), in.GetRepository()); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	writer := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.GetSnapshotResponse{Data: p})
	})

	err := s.localrepo(in.GetRepository()).CreateSnapshot(stream.Context(), writer)
	switch {
	case errors.Is(err, localrepo.ErrSnapshotAlternates):
		// This RPC historically does not consider an invalid alternates as a hard failure.
		s.logger.WithField("error", err).WarnContext(stream.Context(), "error getting alternate object directories")
	case err != nil:
		return err
	}

	return nil
}
