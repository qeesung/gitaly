package ref

import (
	"errors"
	"fmt"
	"io/ioutil"

	"github.com/golang/protobuf/proto"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gitpipe"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) GetTagSignatures(request *gitalypb.GetTagSignaturesRequest, stream gitalypb.RefService_GetTagSignaturesServer) error {
	if err := validateGetTagSignaturesRequest(request); err != nil {
		return status.Errorf(codes.InvalidArgument, "GetTagSignatures: %v", err)
	}

	ctx := stream.Context()
	repo := s.localrepo(request.GetRepository())

	chunker := chunk.New(&tagSignatureSender{
		send: func(signatures []*gitalypb.TagSignature) error {
			return stream.Send(&gitalypb.GetTagSignaturesResponse{
				Signatures: signatures,
			})
		},
	})

	catfileProcess, err := s.catfileCache.BatchProcess(ctx, repo)
	if err != nil {
		return helper.ErrInternal(fmt.Errorf("creating catfile process: %w", err))
	}

	signatures := make([]gitpipe.RevisionResult, len(request.GetTagIds()))
	for i, signatureID := range request.GetTagIds() {
		signatures[i] = gitpipe.RevisionResult{OID: git.ObjectID(signatureID)}
	}

	catfileInfoIter := gitpipe.CatfileInfo(ctx, catfileProcess, gitpipe.NewRevisionIterator(signatures))
	catfileObjectIter := gitpipe.CatfileObject(ctx, catfileProcess, catfileInfoIter)

	for catfileObjectIter.Next() {
		tag := catfileObjectIter.Result()

		raw, err := ioutil.ReadAll(tag.ObjectReader)
		if err != nil {
			return helper.ErrInternal(err)
		}

		signatureKey, tagText := catfile.ExtractTagSignature(raw)

		if err := chunker.Send(&gitalypb.TagSignature{
			TagId:      tag.ObjectInfo.Oid.String(),
			Signature:  signatureKey,
			SignedText: tagText,
		}); err != nil {
			return helper.ErrInternal(fmt.Errorf("sending tag signature chunk: %w", err))
		}
	}

	if err := catfileObjectIter.Err(); err != nil {
		return helper.ErrInternal(err)
	}

	if err := chunker.Flush(); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func validateGetTagSignaturesRequest(request *gitalypb.GetTagSignaturesRequest) error {
	if request.GetRepository() == nil {
		return errors.New("empty Repository")
	}

	if len(request.GetTagIds()) == 0 {
		return errors.New("empty TagIds")
	}

	// Do not support shorthand or invalid tag SHAs
	for _, tagID := range request.TagIds {
		if err := git.ValidateObjectID(tagID); err != nil {
			return err
		}
	}

	return nil
}

type tagSignatureSender struct {
	signatures []*gitalypb.TagSignature
	send       func([]*gitalypb.TagSignature) error
}

func (t *tagSignatureSender) Reset() {
	t.signatures = t.signatures[:0]
}

func (t *tagSignatureSender) Append(m proto.Message) {
	t.signatures = append(t.signatures, m.(*gitalypb.TagSignature))
}

func (t *tagSignatureSender) Send() error {
	return t.send(t.signatures)
}
