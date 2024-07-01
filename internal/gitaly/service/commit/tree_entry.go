package commit

import (
	"context"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func sendTreeEntry(
	stream gitalypb.CommitService_TreeEntryServer,
	objectReader catfile.ObjectContentReader,
	objectInfoReader catfile.ObjectInfoReader,
	revision, path string,
	limit, maxSize int64,
) error {
	ctx := stream.Context()

	treeEntry, err := catfile.NewTreeEntryFinder(objectReader).FindByRevisionAndPath(ctx, revision, path)
	if err != nil {
		return err
	}

	if treeEntry == nil || len(treeEntry.Oid) == 0 {
		return structerr.NewNotFound("tree entry not found").WithMetadata("path", path)
	}

	if treeEntry.Type == gitalypb.TreeEntry_COMMIT {
		response := &gitalypb.TreeEntryResponse{
			Type: gitalypb.TreeEntryResponse_COMMIT,
			Mode: treeEntry.Mode,
			Oid:  treeEntry.Oid,
		}
		if err := stream.Send(response); err != nil {
			return structerr.NewAborted("send: %w", err)
		}

		return nil
	}

	if treeEntry.Type == gitalypb.TreeEntry_TREE {
		treeInfo, err := objectInfoReader.Info(ctx, git.Revision(treeEntry.Oid))
		if err != nil {
			return err
		}

		response := &gitalypb.TreeEntryResponse{
			Type: gitalypb.TreeEntryResponse_TREE,
			Oid:  treeEntry.Oid,
			Size: treeInfo.Size,
			Mode: treeEntry.Mode,
		}

		if err := stream.Send(response); err != nil {
			return structerr.NewAborted("sending response: %w", err)
		}

		return nil
	}

	objectInfo, err := objectInfoReader.Info(ctx, git.Revision(treeEntry.Oid))
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	if strings.ToLower(treeEntry.Type.String()) != objectInfo.Type {
		return structerr.NewInternal(
			"mismatched object type: tree-oid=%s object-oid=%s entry-type=%s object-type=%s",
			treeEntry.Oid, objectInfo.Oid, treeEntry.Type.String(), objectInfo.Type,
		)
	}

	dataLength := objectInfo.Size

	if maxSize > 0 && dataLength > maxSize {
		return structerr.NewFailedPrecondition(
			"object size (%d) is bigger than the maximum allowed size (%d)",
			dataLength, maxSize,
		)
	}

	if limit > 0 && dataLength > limit {
		dataLength = limit
	}

	response := &gitalypb.TreeEntryResponse{
		Type: gitalypb.TreeEntryResponse_BLOB,
		Oid:  objectInfo.Oid.String(),
		Size: objectInfo.Size,
		Mode: treeEntry.Mode,
	}
	if dataLength == 0 {
		if err := stream.Send(response); err != nil {
			return structerr.NewAborted("sending response: %w", err)
		}

		return nil
	}

	blobObj, err := objectReader.Object(ctx, git.Revision(objectInfo.Oid))
	if err != nil {
		return err
	}
	if blobObj.Type != "blob" {
		return fmt.Errorf("blob has unexpected type %q", blobObj.Type)
	}

	sw := streamio.NewWriter(func(p []byte) error {
		response.Data = p

		if err := stream.Send(response); err != nil {
			return structerr.NewAborted("send: %w", err)
		}

		// Use a new response so we don't send other fields (Size, ...) over and over
		response = &gitalypb.TreeEntryResponse{}

		return nil
	})

	_, err = io.CopyN(sw, blobObj, dataLength)
	return err
}

func (s *server) TreeEntry(in *gitalypb.TreeEntryRequest, stream gitalypb.CommitService_TreeEntryServer) error {
	if err := validateRequest(stream.Context(), s.locator, in); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(in.GetRepository())

	requestPath := string(in.GetPath())
	// filepath.Dir("api/docs") => "api" Correct!
	// filepath.Dir("api/docs/") => "api/docs" WRONG!
	if len(requestPath) > 1 {
		requestPath = strings.TrimRight(requestPath, "/")
	}

	objectReader, cancel, err := s.catfileCache.ObjectReader(stream.Context(), repo)
	if err != nil {
		return err
	}
	defer cancel()

	objectInfoReader, cancel, err := s.catfileCache.ObjectInfoReader(stream.Context(), repo)
	if err != nil {
		return err
	}
	defer cancel()

	return sendTreeEntry(stream, objectReader, objectInfoReader, string(in.GetRevision()), requestPath, in.GetLimit(), in.GetMaxSize())
}

func validateRequest(ctx context.Context, locator storage.Locator, in *gitalypb.TreeEntryRequest) error {
	if err := locator.ValidateRepository(ctx, in.GetRepository()); err != nil {
		return err
	}
	if err := git.ValidateRevision(in.Revision); err != nil {
		return err
	}

	if len(in.GetPath()) == 0 {
		return fmt.Errorf("empty Path")
	}

	if in.GetLimit() < 0 {
		return fmt.Errorf("negative Limit")
	}
	if in.GetMaxSize() < 0 {
		return fmt.Errorf("negative MaxSize")
	}

	return nil
}
