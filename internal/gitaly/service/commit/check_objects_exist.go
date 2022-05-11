package commit

import (
	"context"
	"io"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

func (s *server) CheckObjectsExist(
	stream gitalypb.CommitService_CheckObjectsExistServer,
) error {
	ctx := stream.Context()

	request, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			// Ideally, we'd return an invalid-argument error in case there aren't any
			// requests. We can't do this though as this would diverge from Praefect's
			// behaviour, which always returns `io.EOF`.
			return err
		}
		return helper.ErrInternalf("receiving initial request: %w", err)
	}

	if request.GetRepository() == nil {
		return helper.ErrInvalidArgumentf("empty Repository")
	}

	objectInfoReader, cancel, err := s.catfileCache.ObjectInfoReader(
		ctx,
		s.localrepo(request.GetRepository()),
	)
	if err != nil {
		return helper.ErrInternalf("creating object info reader: %w", err)
	}
	defer cancel()

	chunker := chunk.New(&checkObjectsExistSender{stream: stream})
	for {
		// Note: we have already fetched the first request containing revisions further up,
		// so we only fetch the next request at the end of this loop.
		for _, revision := range request.GetRevisions() {
			if err := git.ValidateRevision(revision); err != nil {
				return helper.ErrInvalidArgumentf("invalid revision %q: %w", revision, err)
			}
		}

		if err := checkObjectsExist(ctx, request, objectInfoReader, chunker); err != nil {
			return helper.ErrInternalf("checking object existence: %w", err)
		}

		request, err = stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}

			return helper.ErrInternalf("receiving request: %w", err)
		}
	}

	if err := chunker.Flush(); err != nil {
		return helper.ErrInternalf("flushing results: %w", err)
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
			if catfile.IsNotFound(err) {
				revisionExistence.Exists = false
			} else {
				return helper.ErrInternalf("reading object info: %w", err)
			}
		}

		if err := chunker.Send(&revisionExistence); err != nil {
			return helper.ErrInternalf("adding to chunker: %w", err)
		}
	}

	return nil
}
