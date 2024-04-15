package conflicts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/conflict"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/remoterepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) ResolveConflicts(stream gitalypb.ConflictsService_ResolveConflictsServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return err
	}

	header := firstRequest.GetHeader()
	if header == nil {
		return structerr.NewInvalidArgument("empty ResolveConflictsRequestHeader")
	}

	if err = validateResolveConflictsHeader(s.locator, header); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	err = s.resolveConflicts(header, stream)
	return s.handleResolveConflictsErr(err, stream)
}

func (s *server) handleResolveConflictsErr(err error, stream gitalypb.ConflictsService_ResolveConflictsServer) error {
	var errStr string // normalized error message
	if err != nil {
		errStr = strings.TrimPrefix(err.Error(), "resolve: ") // remove subcommand artifact
		errStr = strings.TrimSpace(errStr)                    // remove newline artifacts

		// only send back resolution errors that match expected pattern
		for _, p := range []string{
			"Missing resolution for section ID:",
			"Resolved content has no changes for file",
			"Missing resolutions for the following files:",
		} {
			if strings.HasPrefix(errStr, p) {
				// log the error since the interceptor won't catch this
				// error due to the unique way the RPC is defined to
				// handle resolution errors
				s.logger.
					WithError(err).
					ErrorContext(stream.Context(), "ResolveConflicts: unable to resolve conflict")
				return stream.SendAndClose(&gitalypb.ResolveConflictsResponse{
					ResolutionError: errStr,
				})
			}
		}

		return err
	}
	return stream.SendAndClose(&gitalypb.ResolveConflictsResponse{})
}

func validateResolveConflictsHeader(locator storage.Locator, header *gitalypb.ResolveConflictsRequestHeader) error {
	if header.GetOurCommitOid() == "" {
		return errors.New("empty OurCommitOid")
	}
	if err := locator.ValidateRepository(header.GetRepository()); err != nil {
		return err
	}
	if header.GetTargetRepository() == nil {
		return errors.New("empty TargetRepository")
	}
	if header.GetTheirCommitOid() == "" {
		return errors.New("empty TheirCommitOid")
	}
	if header.GetSourceBranch() == nil {
		return errors.New("empty SourceBranch")
	}
	if header.GetTargetBranch() == nil {
		return errors.New("empty TargetBranch")
	}
	if header.GetCommitMessage() == nil {
		return errors.New("empty CommitMessage")
	}
	if header.GetUser() == nil {
		return errors.New("empty User")
	}

	return nil
}

func (s *server) resolveConflicts(header *gitalypb.ResolveConflictsRequestHeader, stream gitalypb.ConflictsService_ResolveConflictsServer) error {
	authorSignature, err := git.SignatureFromRequest(header)
	if err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	b := bytes.NewBuffer(nil)
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		if _, err := b.Write(req.GetFilesJson()); err != nil {
			return err
		}
	}

	var checkKeys []map[string]interface{}
	if err := json.Unmarshal(b.Bytes(), &checkKeys); err != nil {
		return err
	}

	for _, ck := range checkKeys {
		_, sectionExists := ck["sections"]
		_, contentExists := ck["content"]
		if !sectionExists && !contentExists {
			return structerr.NewInvalidArgument("missing sections or content for a resolution")
		}
	}

	var resolutions []conflict.Resolution
	if err := json.Unmarshal(b.Bytes(), &resolutions); err != nil {
		return err
	}

	ctx := stream.Context()
	targetRepo, err := remoterepo.New(ctx, header.GetTargetRepository(), s.pool)
	if err != nil {
		return err
	}

	quarantineDir, quarantineRepo, err := s.quarantinedRepo(ctx, header.GetRepository())
	if err != nil {
		return err
	}

	if err := s.repoWithBranchCommit(ctx,
		quarantineRepo,
		targetRepo,
		header.TargetBranch,
	); err != nil {
		return err
	}

	objectHash, err := quarantineRepo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object hash: %w", err)
	}

	if objectHash.ValidateHex(header.GetOurCommitOid()) != nil ||
		objectHash.ValidateHex(header.GetTheirCommitOid()) != nil {
		return errors.New("Rugged::InvalidError: unable to parse OID - contains invalid characters")
	}

	commitOID, err := s.resolveConflictsWithGit(
		ctx,
		header.GetOurCommitOid(),
		header.GetTheirCommitOid(),
		quarantineRepo,
		resolutions,
		authorSignature,
		header.GetCommitMessage(),
	)
	if err != nil {
		return err
	}

	if err := s.updater.UpdateReference(
		ctx,
		header.Repository,
		header.User,
		quarantineDir,
		git.ReferenceName("refs/heads/"+string(header.GetSourceBranch())),
		commitOID,
		git.ObjectID(header.OurCommitOid),
	); err != nil {
		return err
	}

	return nil
}

func (s *server) resolveConflictsWithGit(
	ctx context.Context,
	ours, theirs string,
	repo *localrepo.Repo,
	resolutions []conflict.Resolution,
	author git.Signature,
	commitMessage []byte,
) (git.ObjectID, error) {
	treeOID, err := repo.MergeTree(ctx, ours, theirs, localrepo.WithAllowUnrelatedHistories())

	var mergeConflictErr *localrepo.MergeTreeConflictError
	if errors.As(err, &mergeConflictErr) {
		conflictedFiles := mergeConflictErr.ConflictedFiles()
		checkedConflictedFiles := make(map[string]bool)
		for _, conflictedFile := range conflictedFiles {
			checkedConflictedFiles[conflictedFile] = false
		}

		tree, err := repo.ReadTree(ctx, treeOID.Revision(), localrepo.WithRecursive())
		if err != nil {
			return "", structerr.NewInternal("getting tree: %w", err)
		}

		objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
		if err != nil {
			return "", structerr.NewInternal("getting objectreader: %w", err)
		}
		defer cancel()

		for _, resolution := range resolutions {
			path := resolution.OldPath

			if _, ok := checkedConflictedFiles[path]; !ok {
				// Note: this emulates the Ruby error that occurs when
				// there are no conflicts for a resolution
				return "", errors.New("NoMethodError: undefined method `resolve_lines' for nil:NilClass")
			}

			// We mark the file as checked, any remaining files, which don't have a resolution
			// associated, will throw an error.
			checkedConflictedFiles[path] = true

			conflictedBlob, err := tree.Get(path)
			if err != nil {
				return "", structerr.NewInternal("path not found in merged-tree: %w", err)
			}

			if conflictedBlob.Type != localrepo.Blob {
				return "", structerr.NewInternal("entry should be of type blob").
					WithMetadataItems(
						structerr.MetadataItem{Key: "path", Value: path},
						structerr.MetadataItem{Key: "type", Value: conflictedBlob.Type},
					)
			}

			// We first read the object completely to see if the content is similar
			// to the content in the resolution.
			if resolution.Content != "" {
				object, err := objectReader.Object(ctx, conflictedBlob.OID.Revision())
				if err != nil {
					return "", structerr.NewInternal("retrieving object: %w", err)
				}

				content, err := io.ReadAll(object)
				if err != nil {
					return "", structerr.NewInternal("reading object: %w", err)
				}

				// Git2Go conflict markers have filenames and git-merge-tree(1) has commit OIDs.
				// Rails uses the older form, so to check if the content is the same, we need to
				// adhere to this.
				//
				// Should be fixed with: https://gitlab.com/gitlab-org/git/-/issues/168
				content = bytes.ReplaceAll(content, []byte(ours), []byte(resolution.OldPath))
				content = bytes.ReplaceAll(content, []byte(theirs), []byte(resolution.NewPath))

				if bytes.Equal([]byte(resolution.Content), content) {
					// This is to keep the error consistent with git2go implementation
					return "", structerr.NewInvalidArgument("Resolved content has no changes for file %s", path)
				}
			}

			object, err := objectReader.Object(ctx, git.Revision(fmt.Sprintf("%s:%s", ours, resolution.OldPath)))
			if err != nil {
				return "", structerr.NewInternal("retrieving object: %w", err)
			}

			// Rails expects files ending with newlines to retain them post conflict, but
			// git swallows ending newlines. So we manually append them if necessary.
			needsNewLine := false

			oursContent, err := io.ReadAll(object)
			if err != nil {
				return "", structerr.NewInternal("reading object: %w", err)
			}
			if len(oursContent) > 0 {
				needsNewLine = oursContent[len(oursContent)-1] == '\n'
			}

			object, err = objectReader.Object(ctx, conflictedBlob.OID.Revision())
			if err != nil {
				return "", structerr.NewInternal("retrieving object: %w", err)
			}

			resolvedContent, err := conflict.Resolve(object, git.ObjectID(ours), git.ObjectID(theirs), path, resolution, needsNewLine)
			if err != nil {
				// If there are delimeters still present in the conflict resolution
				// this means the client hasn't addressed all conflicts.
				if errors.Is(err, conflict.ErrUnexpectedDelimiter) {
					return "", structerr.NewInvalidArgument("%w", err)
				}

				return "", structerr.NewInternal("%w", err)
			}

			blobOID, err := repo.WriteBlob(ctx, resolvedContent, localrepo.WriteBlobConfig{
				Path: filepath.Base(path),
			})
			if err != nil {
				return "", structerr.NewInternal("writing blob: %w", err)
			}

			err = tree.Add(path, localrepo.TreeEntry{
				OID:  blobOID,
				Mode: conflictedBlob.Mode,
				Path: filepath.Base(path),
				Type: localrepo.Blob,
			}, localrepo.WithOverwriteFile())
			if err != nil {
				return "", structerr.NewInternal("add to tree: %w", err)
			}
		}

		for conflictedFile, checked := range checkedConflictedFiles {
			if !checked {
				return "", fmt.Errorf("Missing resolutions for the following files: %s", conflictedFile) //nolint // this is to stay consistent with rugged-rails error
			}
		}

		err = tree.Write(ctx, repo)
		if err != nil {
			return "", structerr.NewInternal("write tree: %w", err)
		}

		treeOID = tree.OID
	} else if err != nil {
		return "", structerr.NewInternal("merge-tree: %w", err)
	}

	commitOID, err := repo.WriteCommit(ctx, localrepo.WriteCommitConfig{
		Parents:        []git.ObjectID{git.ObjectID(ours), git.ObjectID(theirs)},
		CommitterDate:  author.When,
		CommitterEmail: author.Email,
		CommitterName:  author.Name,
		AuthorDate:     author.When,
		AuthorEmail:    author.Email,
		AuthorName:     author.Name,
		Message:        string(commitMessage),
		TreeID:         treeOID,
	})
	if err != nil {
		return "", structerr.NewInternal("writing commit: %w", err)
	}

	return commitOID, nil
}

func sameRepo(left, right storage.Repository) bool {
	lgaod := left.GetGitAlternateObjectDirectories()
	rgaod := right.GetGitAlternateObjectDirectories()
	if len(lgaod) != len(rgaod) {
		return false
	}
	sort.Strings(lgaod)
	sort.Strings(rgaod)
	for i := 0; i < len(lgaod); i++ {
		if lgaod[i] != rgaod[i] {
			return false
		}
	}
	if left.GetGitObjectDirectory() != right.GetGitObjectDirectory() {
		return false
	}
	if left.GetRelativePath() != right.GetRelativePath() {
		return false
	}
	if left.GetStorageName() != right.GetStorageName() {
		return false
	}
	return true
}

// repoWithCommit ensures that the source repo contains the same commit we
// hope to merge with from the target branch, else it will be fetched from the
// target repo. This is necessary since all merge/resolve logic occurs on the
// same filesystem
func (s *server) repoWithBranchCommit(ctx context.Context, sourceRepo *localrepo.Repo, targetRepo *remoterepo.Repo, targetBranch []byte) error {
	const peelCommit = "^{commit}"

	targetRevision := "refs/heads/" + git.Revision(string(targetBranch)) + peelCommit

	if sameRepo(sourceRepo, targetRepo) {
		_, err := sourceRepo.ResolveRevision(ctx, targetRevision)
		return err
	}

	oid, err := targetRepo.ResolveRevision(ctx, targetRevision)
	if err != nil {
		return fmt.Errorf("could not resolve target revision %q: %w", targetRevision, err)
	}

	ok, err := sourceRepo.HasRevision(ctx, git.Revision(oid)+peelCommit)
	if err != nil {
		return err
	}
	if ok {
		// target branch commit already exists in source repo; nothing
		// to do
		return nil
	}

	if err := sourceRepo.FetchInternal(
		ctx,
		targetRepo.Repository,
		[]string{oid.String()},
		localrepo.FetchOpts{Tags: localrepo.FetchOptsTagsNone},
	); err != nil {
		return fmt.Errorf("could not fetch target commit: %w", err)
	}

	return nil
}
