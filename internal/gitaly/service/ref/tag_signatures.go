package ref

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v14/streamio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var signaturePrefix = []byte("-----BEGIN")

func (s *server) GetTagSignatures(request *gitalypb.GetTagSignaturesRequest, stream gitalypb.RefService_GetTagSignaturesServer) error {
	if err := validateGetTagSignaturesRequest(request); err != nil {
		return status.Errorf(codes.InvalidArgument, "GetTagSignatures: %v", err)
	}

	return s.getTagSignatures(request, stream)
}

func (s *server) getTagSignatures(request *gitalypb.GetTagSignaturesRequest, stream gitalypb.RefService_GetTagSignaturesServer) error {
	ctx := stream.Context()
	repo := s.localrepo(request.GetRepository())

	c, err := s.catfileCache.BatchProcess(ctx, repo)
	if err != nil {
		return helper.ErrInternal(err)
	}

	for _, tagID := range request.TagIds {
		tagObj, err := c.Tag(ctx, git.Revision(tagID))
		if err != nil {
			return err
		}

		raw, err := ioutil.ReadAll(tagObj.Reader)
		if err != nil {
			return err
		}

		signatureKey, tagText := catfile.ExtractTagSignature(raw)

		if err = sendResponse(tagID, signatureKey, tagText, stream); err != nil {
			return helper.ErrInternal(err)
		}
	}

	return nil
}

func extractSignature(reader io.Reader) ([]byte, []byte, error) {
	tagText := []byte{}
	signatureKey := []byte{}
	inSignature := false
	lineBreak := []byte("\n")
	whiteSpace := []byte(" ")
	bufferedReader := bufio.NewReader(reader)

	for {
		line, err := bufferedReader.ReadBytes('\n')

		if err == io.EOF {
			tagText = append(tagText, line...)
			break
		}
		if err != nil {
			return nil, nil, err
		}

		if !inSignature && bytes.HasPrefix(line, signaturePrefix) {
			inSignature = true
		}

		if inSignature && !bytes.Equal(line, lineBreak) {
			line = bytes.TrimPrefix(line, whiteSpace)
			signatureKey = append(signatureKey, line...)
		} else {
			tagText = append(tagText, line...)
		}
	}

	// Remove last line break from signature and message
	signatureKey = bytes.TrimSuffix(signatureKey, lineBreak)

	return signatureKey, tagText, nil
}

func sendResponse(tagID string, signatureKey []byte, tagText []byte, stream gitalypb.RefService_GetTagSignaturesServer) error {
	if len(signatureKey) <= 0 {
		return nil
	}

	err := stream.Send(&gitalypb.GetTagSignaturesResponse{
		TagId:     tagID,
		Signature: signatureKey,
	})
	if err != nil {
		return err
	}

	streamWriter := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.GetTagSignaturesResponse{SignedText: p})
	})

	msgReader := bytes.NewReader(tagText)

	_, err = io.Copy(streamWriter, msgReader)
	if err != nil {
		return fmt.Errorf("failed to send response: %v", err)
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
