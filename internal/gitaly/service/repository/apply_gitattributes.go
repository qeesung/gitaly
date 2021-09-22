package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v14/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v14/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

const attributesFileMode os.FileMode = 0o644

func (s *server) applyGitattributes(ctx context.Context, c catfile.Batch, repoPath string, revision []byte) error {
	infoPath := filepath.Join(repoPath, "info")
	attributesPath := filepath.Join(infoPath, "attributes")

	_, err := c.Info(ctx, git.Revision(revision))
	if err != nil {
		if catfile.IsNotFound(err) {
			return helper.ErrInvalidArgumentf("revision does not exist")
		}

		return err
	}

	blobInfo, err := c.Info(ctx, git.Revision(fmt.Sprintf("%s:.gitattributes", revision)))
	if err != nil && !catfile.IsNotFound(err) {
		return err
	}

	if catfile.IsNotFound(err) || blobInfo.Type != "blob" {
		// If there is no gitattributes file, we simply use the ZeroOID
		// as a placeholder to vote on the removal.
		if err := s.vote(ctx, git.ZeroOID); err != nil {
			return fmt.Errorf("could not remove gitattributes: %w", err)
		}

		// Remove info/attributes file if there's no .gitattributes file
		if err := os.Remove(attributesPath); err != nil && !os.IsNotExist(err) {
			return err
		}

		return nil
	}

	// Create  /info folder if it doesn't exist
	if err := os.MkdirAll(infoPath, 0o755); err != nil {
		return err
	}

	blobObj, err := c.Blob(ctx, git.Revision(blobInfo.Oid))
	if err != nil {
		return err
	}

	writer, err := safe.NewLockingFileWriter(attributesPath, safe.LockingFileWriterConfig{
		FileWriterConfig: safe.FileWriterConfig{FileMode: attributesFileMode},
	})
	if err != nil {
		return fmt.Errorf("creating gitattributes writer: %w", err)
	}
	defer writer.Close()

	if _, err := io.CopyN(writer, blobObj.Reader, blobInfo.Size); err != nil {
		return err
	}

	if err := transaction.CommitLockedFile(ctx, s.txManager, writer); err != nil {
		return fmt.Errorf("committing gitattributes: %w", err)
	}

	return nil
}

func (s *server) vote(ctx context.Context, oid git.ObjectID) error {
	tx, err := txinfo.TransactionFromContext(ctx)
	if errors.Is(err, txinfo.ErrTransactionNotFound) {
		return nil
	}

	hash, err := oid.Bytes()
	if err != nil {
		return fmt.Errorf("vote with invalid object ID: %w", err)
	}

	vote, err := voting.VoteFromHash(hash)
	if err != nil {
		return fmt.Errorf("cannot convert OID to vote: %w", err)
	}

	if err := s.txManager.Vote(ctx, tx, vote); err != nil {
		return fmt.Errorf("vote failed: %w", err)
	}

	return nil
}

func (s *server) ApplyGitattributes(ctx context.Context, in *gitalypb.ApplyGitattributesRequest) (*gitalypb.ApplyGitattributesResponse, error) {
	repo := s.localrepo(in.GetRepository())
	repoPath, err := s.locator.GetRepoPath(repo)
	if err != nil {
		return nil, err
	}

	if err := git.ValidateRevision(in.GetRevision()); err != nil {
		return nil, helper.ErrInvalidArgumentf("revision: %v", err)
	}

	c, err := s.catfileCache.BatchProcess(ctx, repo)
	if err != nil {
		return nil, err
	}

	if err := s.applyGitattributes(ctx, c, repoPath, in.GetRevision()); err != nil {
		return nil, err
	}

	return &gitalypb.ApplyGitattributesResponse{}, nil
}
