package commit

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/signature"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func (s *server) GetCommitSignatures(request *gitalypb.GetCommitSignaturesRequest, stream gitalypb.CommitService_GetCommitSignaturesServer) error {
	ctx := stream.Context()

	if err := s.locator.ValidateRepository(request.GetRepository()); err != nil {
		return err
	}

	repo := s.localrepo(request.GetRepository())

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object hash: %w", err)
	}

	if err := validateGetCommitSignaturesRequest(objectHash, request); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return structerr.NewInternal("%w", err)
	}
	defer cancel()

	var signingKeys *signature.SigningKeys
	if s.cfg.Git.SigningKey != "" {
		signingKeys, err = signature.ParseSigningKeys(s.cfg.Git.SigningKey, s.cfg.Git.RotatedSigningKeys...)
		if err != nil {
			return fmt.Errorf("failed to parse signing key: %w", err)
		}
	}

	parser := catfile.NewParser()
	for _, commitID := range request.CommitIds {
		commitObj, err := objectReader.Object(ctx, git.Revision(commitID)+"^{commit}")
		if err != nil {
			if errors.As(err, &catfile.NotFoundError{}) {
				continue
			}
			return structerr.NewInternal("%w", err)
		}

		commit, err := parser.ParseCommit(commitObj)
		if err != nil {
			return structerr.NewInternal("%w", err)
		}

		signature := []byte{}
		if len(commit.SignatureData.Signatures) > 0 {
			// While there could be potentially multiple signatures in a Git
			// commit, like Git, we only consider the first.
			signature = commit.SignatureData.Signatures[0]
		}

		signer := gitalypb.GetCommitSignaturesResponse_SIGNER_USER
		if signingKeys != nil {
			if err := signingKeys.Verify(signature, commit.SignatureData.Payload); err == nil {
				signer = gitalypb.GetCommitSignaturesResponse_SIGNER_SYSTEM
			}
		}

		if err = sendResponse(commitID, signature, commit.SignatureData.Payload, signer, stream); err != nil {
			return structerr.NewInternal("%w", err)
		}
	}

	return nil
}

func sendResponse(
	commitID string,
	signatureKey []byte,
	commitText []byte,
	signer gitalypb.GetCommitSignaturesResponse_Signer,
	stream gitalypb.CommitService_GetCommitSignaturesServer,
) error {
	if len(signatureKey) <= 0 {
		return nil
	}

	err := stream.Send(&gitalypb.GetCommitSignaturesResponse{
		CommitId:  commitID,
		Signature: signatureKey,
		Signer:    signer,
	})
	if err != nil {
		return err
	}

	streamWriter := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.GetCommitSignaturesResponse{SignedText: p})
	})

	msgReader := bytes.NewReader(commitText)

	_, err = io.Copy(streamWriter, msgReader)
	if err != nil {
		return fmt.Errorf("failed to send response: %w", err)
	}

	return nil
}

func validateGetCommitSignaturesRequest(objectHash git.ObjectHash, request *gitalypb.GetCommitSignaturesRequest) error {
	if len(request.GetCommitIds()) == 0 {
		return errors.New("empty CommitIds")
	}

	// Do not support shorthand or invalid commit SHAs
	for _, commitID := range request.CommitIds {
		if err := objectHash.ValidateHex(commitID); err != nil {
			return err
		}
	}

	return nil
}
