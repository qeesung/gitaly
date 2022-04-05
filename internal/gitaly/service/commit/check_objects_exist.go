package commit

import (
	"context"
	"io"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

func (s *server) CheckObjectsExist(
	stream gitalypb.CommitService_CheckObjectsExistServer,
) error {
	ctx := stream.Context()

	request, err := stream.Recv()
	if err != nil {
		return err
	}

	if err := validateCheckObjectsExistRequest(request); err != nil {
		return err
	}

	objectInfoReader, err := s.catfileCache.ObjectInfoReader(
		ctx,
		s.localrepo(request.GetRepository()),
	)
	if err != nil {
		return err
	}

	chunker := chunk.New(&checkObjectsExistSender{stream: stream})
	for {
		request, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return chunker.Flush()
			}
			return err
		}

		if err = checkObjectsExist(ctx, request, objectInfoReader, chunker); err != nil {
			return err
		}
	}
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
	c.revisions = make([]*gitalypb.CheckObjectsExistResponse_RevisionExistence, 0)
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
				return err
			}
		}

		if err := chunker.Send(&revisionExistence); err != nil {
			return err
		}
	}

	return nil
}

func validateCheckObjectsExistRequest(in *gitalypb.CheckObjectsExistRequest) error {
	for _, revision := range in.GetRevisions() {
		if err := git.ValidateRevision(revision); err != nil {
			return helper.ErrInvalidArgument(err)
		}
	}

	return nil
}
