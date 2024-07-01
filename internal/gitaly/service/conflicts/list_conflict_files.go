package conflicts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func (s *server) ListConflictFiles(request *gitalypb.ListConflictFilesRequest, stream gitalypb.ConflictsService_ListConflictFilesServer) error {
	ctx := stream.Context()

	if err := validateListConflictFilesRequest(ctx, s.locator, request); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	_, quarantineRepo, err := s.quarantinedRepo(ctx, request.GetRepository())
	if err != nil {
		return err
	}

	ours, err := quarantineRepo.ResolveRevision(ctx, git.Revision(request.OurCommitOid+"^{commit}"))
	if err != nil {
		return structerr.NewFailedPrecondition("could not lookup 'our' OID: %w", err)
	}

	theirs, err := quarantineRepo.ResolveRevision(ctx, git.Revision(request.TheirCommitOid+"^{commit}"))
	if err != nil {
		return structerr.NewFailedPrecondition("could not lookup 'their' OID: %w", err)
	}

	return s.conflictFilesWithGitMergeTree(ctx, request, stream, ours, theirs, quarantineRepo)
}

func (s *server) conflictFilesWithGitMergeTree(
	ctx context.Context,
	request *gitalypb.ListConflictFilesRequest,
	stream gitalypb.ConflictsService_ListConflictFilesServer,
	ours, theirs git.ObjectID,
	repo *localrepo.Repo,
) error {
	var mergeConflictErr *localrepo.MergeTreeConflictError

	oid, err := repo.MergeTree(ctx, ours.String(), theirs.String(), localrepo.WithAllowUnrelatedHistories())
	if err == nil {
		// When there are no errors, it denotes that there are no conflicts.
		return nil
	} else if !errors.As(err, &mergeConflictErr) {
		// If its not a conflict, we return the error to the user.
		return structerr.NewInternal("couldn't find conflict: %w", err)
	}

	type conflictHeader struct {
		theirPath    string
		ourPath      string
		ancestorPath string
		ourMode      int32
	}

	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return err
	}
	defer cancel()

	// We need to combine data with same path, but we also want to retain
	// the ordering. We use a map to track the data with the same path, but
	// retain ordering by using a slice.
	pathToConflict := make(map[string]*conflictHeader)
	var conflicts []*conflictHeader

	for _, conflictFile := range mergeConflictErr.ConflictingFileInfo {
		val, ok := pathToConflict[conflictFile.FileName]
		if !ok {
			val = &conflictHeader{}
			conflicts = append(conflicts, val)
		}

		switch conflictFile.Stage {
		case localrepo.MergeStageAncestor:
			val.ancestorPath = conflictFile.FileName
		case localrepo.MergeStageOurs:
			val.ourPath = conflictFile.FileName
			val.ourMode = conflictFile.Mode
		case localrepo.MergeStageTheirs:
			val.theirPath = conflictFile.FileName
		}

		pathToConflict[conflictFile.FileName] = val
	}

	var conflictFiles []*gitalypb.ConflictFile
	msgSize := 0

	// Git2Go conflict markers have filenames and git-merge-tree(1) has commit OIDs,
	// to keep the content the same, we replace commit OID with filenames.
	replaceOids := func(conflict *conflictHeader, chunk []byte) []byte {
		chunk = bytes.ReplaceAll(chunk, []byte(ours), []byte(conflict.ourPath))
		chunk = bytes.ReplaceAll(chunk, []byte(theirs), []byte(conflict.theirPath))

		return chunk
	}

	for _, conflict := range conflicts {
		if !request.AllowTreeConflicts && (conflict.theirPath == "" || conflict.ourPath == "") {
			return structerr.NewFailedPrecondition("conflict side missing")
		}

		conflictFiles = append(conflictFiles, &gitalypb.ConflictFile{
			ConflictFilePayload: &gitalypb.ConflictFile_Header{
				Header: &gitalypb.ConflictFileHeader{
					CommitOid:    request.OurCommitOid,
					TheirPath:    []byte(conflict.theirPath),
					OurPath:      []byte(conflict.ourPath),
					AncestorPath: []byte(conflict.ancestorPath),
					OurMode:      conflict.ourMode,
				},
			},
		})

		// Clients do not want the contents of the conflicted files, so we skip this section.
		if request.GetSkipContent() {
			continue
		}

		path := conflict.ourPath
		if path == "" {
			path = conflict.theirPath
		}

		fileOID, err := repo.ResolveRevision(ctx, oid.Revision()+":"+git.Revision(path))
		if err != nil {
			return structerr.NewFailedPrecondition("getting file revision: %w", err)
		}

		object, err := objectReader.Object(ctx, fileOID.Revision())
		if err != nil {
			return structerr.NewFailedPrecondition("getting objectreader: %w", err)
		}

		var content bytes.Buffer
		_, err = content.ReadFrom(object)
		if err != nil && !errors.Is(err, io.EOF) {
			return structerr.NewInternal("reading conflict object: %w", err)
		}

		if !utf8.Valid(content.Bytes()) {
			return structerr.NewFailedPrecondition("unsupported encoding")
		}

		parsedContent := replaceOids(conflict, content.Bytes())
		contentLen := len(parsedContent)

		for i := 0; i < contentLen; i += streamio.WriteBufferSize {
			end := i + streamio.WriteBufferSize
			if contentLen < end {
				end = contentLen
			}

			conflictFiles = append(conflictFiles, &gitalypb.ConflictFile{
				ConflictFilePayload: &gitalypb.ConflictFile_Content{
					Content: parsedContent[i:end],
				},
			})

			// We don't send a message for each chunk because the content of
			// a file may be smaller than the size limit, which means we can
			// keep adding data to the message
			msgSize += end - i
			if msgSize < streamio.WriteBufferSize {
				continue
			}

			if err := stream.Send(&gitalypb.ListConflictFilesResponse{
				Files: conflictFiles,
			}); err != nil {
				return structerr.NewInternal("error streaming conflict files: %w", err)
			}

			conflictFiles = conflictFiles[:0]
			msgSize = 0
		}
	}

	// Send leftover data, if any
	if len(conflictFiles) > 0 {
		if err := stream.Send(&gitalypb.ListConflictFilesResponse{
			Files: conflictFiles,
		}); err != nil {
			return structerr.NewInternal("error streaming conflict files: %w", err)
		}
	}

	return nil
}

func validateListConflictFilesRequest(ctx context.Context, locator storage.Locator, in *gitalypb.ListConflictFilesRequest) error {
	if err := locator.ValidateRepository(ctx, in.GetRepository()); err != nil {
		return err
	}
	if in.GetOurCommitOid() == "" {
		return fmt.Errorf("empty OurCommitOid")
	}
	if in.GetTheirCommitOid() == "" {
		return fmt.Errorf("empty TheirCommitOid")
	}

	return nil
}
