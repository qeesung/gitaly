package commit

import (
	"context"
	"errors"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

func (s *server) CheckObjectsExist(
	stream gitalypb.CommitService_CheckObjectsExistServer,
) error {
	ctx := stream.Context()

	request, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			// Ideally, we'd return an invalid-argument error in case there aren't any
			// requests. We can't do this though as this would diverge from Praefect's
			// behaviour, which always returns `io.EOF`.
			return err
		}
		return structerr.NewInternal("receiving initial request: %w", err)
	}

	repository := request.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	objectInfoReader, cancel, err := s.catfileCache.ObjectInfoReader(
		ctx,
		s.localrepo(repository),
	)
	if err != nil {
		return structerr.NewInternal("creating object info reader: %w", err)
	}
	defer cancel()

	chunker := chunk.New(&checkObjectsExistSender{stream: stream})
	for {
		// Note: we have already fetched the first request containing revisions further up,
		// so we only fetch the next request at the end of this loop.
		for _, revision := range request.GetRevisions() {
			if err := git.ValidateRevision(revision, git.AllowPathScopedRevision()); err != nil {
				return structerr.NewInvalidArgument("invalid revision: %w", err).
					WithMetadata("revision", string(revision))
			}
		}

		if err := checkObjectsExist(ctx, request, objectInfoReader, chunker); err != nil {
			return structerr.NewInternal("checking object existence: %w", err)
		}

		request, err = stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return structerr.NewInternal("receiving request: %w", err)
		}
	}

	if err := chunker.Flush(); err != nil {
		return structerr.NewInternal("flushing results: %w", err)
	}

	return nil
}

type checkObjectsExistSender struct {
	stream    gitalypb.CommitService_CheckObjectsExistServer
	revisions []*gitalypb.CheckObjectsExistResponse_RevisionExistence
}

func (c *checkObjectsExistSender) Send() error {
	return c.stream.Send(&gitalypb.CheckObjectsExistResponse{
		Revisions: c.revisions,
	})
}

func (c *checkObjectsExistSender) Reset() {
	c.revisions = c.revisions[:0]
}

func (c *checkObjectsExistSender) Append(m proto.Message) {
	c.revisions = append(c.revisions, m.(*gitalypb.CheckObjectsExistResponse_RevisionExistence))
}

func checkObjectsExist(
	ctx context.Context,
	request *gitalypb.CheckObjectsExistRequest,
	objectInfoReader catfile.ObjectInfoReader,
	chunker *chunk.Chunker,
) error {
	revisions := request.GetRevisions()

	for _, revision := range revisions {
		revisionExistence := gitalypb.CheckObjectsExistResponse_RevisionExistence{
			Name:   revision,
			Exists: true,
		}
		_, err := objectInfoReader.Info(ctx, git.Revision(revision))
		if err != nil {
			if errors.As(err, &(catfile.NotFoundError{})) {
				revisionExistence.Exists = false
			} else {
				return structerr.NewInternal("reading object info: %w", err)
			}
		}

		if err := chunker.Send(&revisionExistence); err != nil {
			return structerr.NewInternal("adding to chunker: %w", err)
		}
	}

	return nil
}
