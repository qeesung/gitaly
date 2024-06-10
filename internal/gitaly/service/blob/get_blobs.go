package blob

import (
	"bytes"
	"context"
	"errors"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

var treeEntryToObjectType = map[gitalypb.TreeEntry_EntryType]gitalypb.ObjectType{
	gitalypb.TreeEntry_BLOB:   gitalypb.ObjectType_BLOB,
	gitalypb.TreeEntry_TREE:   gitalypb.ObjectType_TREE,
	gitalypb.TreeEntry_COMMIT: gitalypb.ObjectType_COMMIT,
}

func sendGetBlobsResponse(
	req *gitalypb.GetBlobsRequest,
	stream gitalypb.BlobService_GetBlobsServer,
	objectReader catfile.ObjectContentReader,
	objectInfoReader catfile.ObjectInfoReader,
) error {
	ctx := stream.Context()

	tef := catfile.NewTreeEntryFinder(objectReader)

	for _, revisionPath := range req.RevisionPaths {
		revision := revisionPath.Revision
		path := revisionPath.Path

		if len(path) > 1 {
			path = bytes.TrimRight(path, "/")
		}

		treeEntry, err := tef.FindByRevisionAndPath(ctx, revision, string(path))
		if err != nil {
			return structerr.NewInternal("find by revision and path: %w", err)
		}

		response := &gitalypb.GetBlobsResponse{Revision: revision, Path: path}

		if treeEntry == nil || len(treeEntry.Oid) == 0 {
			if err := stream.Send(response); err != nil {
				return structerr.NewAborted("send: %w", err)
			}

			continue
		}

		response.Mode = treeEntry.Mode
		response.Oid = treeEntry.Oid

		if treeEntry.Type == gitalypb.TreeEntry_COMMIT {
			response.IsSubmodule = true
			response.Type = gitalypb.ObjectType_COMMIT

			if err := stream.Send(response); err != nil {
				return structerr.NewAborted("send: %w", err)
			}

			continue
		}

		objectInfo, err := objectInfoReader.Info(ctx, git.Revision(treeEntry.Oid))
		if err != nil {
			return structerr.NewInternal("read object info: %w", err)
		}

		response.Size = objectInfo.Size

		var ok bool
		response.Type, ok = treeEntryToObjectType[treeEntry.Type]

		if !ok {
			continue
		}

		if response.Type != gitalypb.ObjectType_BLOB {
			if err := stream.Send(response); err != nil {
				return structerr.NewAborted("send: %w", err)
			}
			continue
		}

		if err = sendBlobTreeEntry(response, stream, objectReader, req.GetLimit()); err != nil {
			return err
		}
	}

	return nil
}

func sendBlobTreeEntry(
	response *gitalypb.GetBlobsResponse,
	stream gitalypb.BlobService_GetBlobsServer,
	objectReader catfile.ObjectContentReader,
	limit int64,
) (returnedErr error) {
	ctx := stream.Context()

	var readLimit int64
	if limit < 0 || limit > response.Size {
		readLimit = response.Size
	} else {
		readLimit = limit
	}

	// For correctness, it does not matter, but for performance, the order is
	// important: first check if readlimit == 0, if not, only then create
	// blobObj.
	if readLimit == 0 {
		if err := stream.Send(response); err != nil {
			return structerr.NewAborted("send: %w", err)
		}
		return nil
	}

	blobObj, err := objectReader.Object(ctx, git.Revision(response.Oid))
	if err != nil {
		return structerr.NewInternal("read object: %w", err)
	}
	defer func() {
		if _, err := io.Copy(io.Discard, blobObj); err != nil && returnedErr == nil {
			returnedErr = structerr.NewInternal("discarding data: %w", err)
		}
	}()
	if blobObj.Type != "blob" {
		return structerr.NewInternal("blob got unexpected type %q", blobObj.Type)
	}

	sw := streamio.NewWriter(func(p []byte) error {
		msg := &gitalypb.GetBlobsResponse{}
		if response != nil {
			msg = response
			response = nil
		}

		msg.Data = p

		return stream.Send(msg)
	})

	_, err = io.CopyN(sw, blobObj, readLimit)
	if err != nil {
		return structerr.NewAborted("send: %w", err)
	}

	return nil
}

func (s *server) GetBlobs(req *gitalypb.GetBlobsRequest, stream gitalypb.BlobService_GetBlobsServer) error {
	if err := validateGetBlobsRequest(stream.Context(), s.locator, req); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(req.GetRepository())

	objectReader, cancel, err := s.catfileCache.ObjectReader(stream.Context(), repo)
	if err != nil {
		return structerr.NewInternal("creating object reader: %w", err)
	}
	defer cancel()

	objectInfoReader, cancel, err := s.catfileCache.ObjectInfoReader(stream.Context(), repo)
	if err != nil {
		return structerr.NewInternal("creating object info reader: %w", err)
	}
	defer cancel()

	return sendGetBlobsResponse(req, stream, objectReader, objectInfoReader)
}

func validateGetBlobsRequest(ctx context.Context, locator storage.Locator, req *gitalypb.GetBlobsRequest) error {
	if err := locator.ValidateRepository(ctx, req.GetRepository()); err != nil {
		return err
	}

	if len(req.RevisionPaths) == 0 {
		return errors.New("empty RevisionPaths")
	}

	for _, rp := range req.RevisionPaths {
		if err := git.ValidateRevision([]byte(rp.Revision)); err != nil {
			return err
		}
	}

	return nil
}
