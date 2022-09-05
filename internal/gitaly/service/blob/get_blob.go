package blob

import (
	"errors"
	"io"

	gitalyerrors "gitlab.com/gitlab-org/gitaly/v15/internal/errors"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
)

func (s *server) GetBlob(in *gitalypb.GetBlobRequest, stream gitalypb.BlobService_GetBlobServer) error {
	ctx := stream.Context()

	if err := validateRequest(in); err != nil {
		return helper.ErrInvalidArgument(err)
	}

	repo := s.localrepo(in.GetRepository())

	objectReader, cancel, err := s.catfileCache.ObjectReader(stream.Context(), repo)
	if err != nil {
		return helper.ErrInternalf("create object reader: %w", err)
	}
	defer cancel()

	blob, err := objectReader.Object(ctx, git.Revision(in.Oid))
	if err != nil {
		if catfile.IsNotFound(err) {
			return helper.ErrUnavailable(stream.Send(&gitalypb.GetBlobResponse{}))
		}
		return helper.ErrInternalf("read object: %w", err)
	}

	if blob.Type != "blob" {
		return helper.ErrUnavailable(stream.Send(&gitalypb.GetBlobResponse{}))
	}

	readLimit := blob.Size
	if in.Limit >= 0 && in.Limit < readLimit {
		readLimit = in.Limit
	}
	firstMessage := &gitalypb.GetBlobResponse{
		Size: blob.Size,
		Oid:  blob.Oid.String(),
	}

	if readLimit == 0 {
		return helper.ErrUnavailable(stream.Send(firstMessage))
	}

	sw := streamio.NewWriter(func(p []byte) error {
		msg := &gitalypb.GetBlobResponse{}
		if firstMessage != nil {
			msg = firstMessage
			firstMessage = nil
		}
		msg.Data = p
		return stream.Send(msg)
	})

	_, err = io.CopyN(sw, blob, readLimit)
	if err != nil {
		return helper.ErrUnavailablef("send: %w", err)
	}

	return nil
}

func validateRequest(in *gitalypb.GetBlobRequest) error {
	if in.GetRepository() == nil {
		return gitalyerrors.ErrEmptyRepository
	}

	if len(in.GetOid()) == 0 {
		return errors.New("empty Oid")
	}
	return nil
}
