package operations

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/remoterepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

// UserCommitFiles allows for committing from a set of actions. See the protobuf documentation
// for details.
func (s *Server) UserCommitFiles(stream gitalypb.OperationService_UserCommitFilesServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return err
	}

	header := firstRequest.GetHeader()
	if header == nil {
		return structerr.NewInvalidArgument("empty UserCommitFilesRequestHeader")
	}

	if err = validateUserCommitFilesHeader(header); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	ctx := stream.Context()

	if err := s.userCommitFiles(ctx, header, stream); err != nil {
		ctxlogrus.AddFields(ctx, logrus.Fields{
			"repository_storage":       header.Repository.StorageName,
			"repository_relative_path": header.Repository.RelativePath,
			"branch_name":              header.BranchName,
			"start_branch_name":        header.StartBranchName,
			"start_sha":                header.StartSha,
			"force":                    header.Force,
		})

		if startRepo := header.GetStartRepository(); startRepo != nil {
			ctxlogrus.AddFields(ctx, logrus.Fields{
				"start_repository_storage":       startRepo.StorageName,
				"start_repository_relative_path": startRepo.RelativePath,
			})
		}

		var (
			unknownErr    git2go.UnknownIndexError
			indexErr      git2go.IndexError
			customHookErr updateref.CustomHookError
		)

		switch {
		case errors.As(err, &unknownErr):
			// Problems that occur within git2go itself will still be returned
			// as UnknownIndexErrors. The most common case of this would be
			// creating an invalid path, e.g. '.git' but there are many other
			// potential, if unusual, issues that could occur.
			return unknownErr
		case errors.As(err, &indexErr):
			return indexErr.StructuredError().WithDetail(
				&gitalypb.UserCommitFilesError{
					Error: &gitalypb.UserCommitFilesError_IndexUpdate{
						IndexUpdate: indexErr.Proto(),
					},
				},
			)
		case errors.As(err, &customHookErr):
			return structerr.NewPermissionDenied("denied by custom hooks").WithDetail(
				&gitalypb.UserCommitFilesError{
					Error: &gitalypb.UserCommitFilesError_CustomHook{
						CustomHook: customHookErr.Proto(),
					},
				},
			)
		case errors.As(err, new(git2go.InvalidArgumentError)):
			return structerr.NewInvalidArgument("%w", err)
		default:
			return err
		}
	}

	return nil
}

func validatePath(rootPath, relPath string) (string, error) {
	if relPath == "" {
		return "", git2go.IndexError{Type: git2go.ErrEmptyPath}
	} else if strings.Contains(relPath, "//") {
		// This is a workaround to address a quirk in porting the RPC from Ruby to Go.
		// GitLab's QA pipeline runs tests with filepath 'invalid://file/name/here'.
		// Go's filepath.Clean returns 'invalid:/file/name/here'. The Ruby implementation's
		// filepath normalization accepted the path as is. Adding a file with this path to the
		// index via Rugged failed with an invalid path error. As Go's cleaning resulted a valid
		// filepath, adding the file succeeded, which made the QA pipeline's specs fail.
		//
		// The Rails code expects to receive an error prefixed with 'invalid path', which is done
		// here to retain compatibility.
		return "", git2go.IndexError{Type: git2go.ErrInvalidPath, Path: relPath}
	}

	path, err := storage.ValidateRelativePath(rootPath, relPath)
	if err != nil {
		if errors.Is(err, storage.ErrRelativePathEscapesRoot) {
			return "", git2go.IndexError{Type: git2go.ErrDirectoryTraversal, Path: relPath}
		}

		return "", err
	}

	return path, nil
}

// ErrNotFile indicates an error when a file was expected.
var ErrNotFile = errors.New("not a file")

func applyAction(ctx context.Context, action git2go.Action, treeish git.ObjectID, repo *localrepo.Repo) (git.ObjectID, error) {
	var treeID git.ObjectID
	var err error
	var path string

	switch action := action.(type) {
	case git2go.ChangeFileMode:
		path = action.Path
		treeID, err = repo.ModifyTreeEntry(
			ctx,
			git.Revision(treeish),
			action.Path,
			func(entry *localrepo.TreeEntry) error {
				if action.ExecutableMode {
					if entry.Mode != "100755" {
						entry.Mode = "100755"
					}
				} else {
					if entry.Mode == "100755" {
						entry.Mode = "100644"
					}
				}

				return nil
			})
	case git2go.UpdateFile:
		path = action.Path
		treeID, err = repo.ModifyTreeEntry(
			ctx,
			git.Revision(treeish),
			action.Path,
			func(entry *localrepo.TreeEntry) error {
				entry.OID = git.ObjectID(action.OID)
				return nil
			})
	case git2go.MoveFile:
		path = action.NewPath
		treeID, err = repo.DeleteTreeEntry(
			ctx,
			git.Revision(treeish),
			action.Path,
		)
		if err != nil {
			return "", translateToGit2GoError(err, action.Path)
		}

		if action.OID == "" {
			info, err := repo.ReadObjectInfo(
				ctx,
				git.Revision(
					fmt.Sprintf(
						"%s:%s",
						treeish,
						action.Path,
					),
				),
			)
			if err != nil {
				return "", err
			}

			if !info.IsBlob() {
				return "", translateToGit2GoError(ErrNotFile, action.Path)
			}
			action.OID = string(info.ObjectID())
		}

		treeID, err = repo.AddTreeEntry(
			ctx,
			git.Revision(treeID),
			action.NewPath,
			&localrepo.TreeEntry{
				OID:  git.ObjectID(action.OID),
				Mode: "100644",
				Path: filepath.Base(action.NewPath),
			},
			false,
			true,
		)
	case git2go.CreateDirectory:
		path = action.Path

		treeID, err = repo.AddTreeEntry(
			ctx,
			git.Revision(treeish),
			path,
			&localrepo.TreeEntry{
				Mode: "040000",
				Path: filepath.Base(path),
				Type: localrepo.Tree,
			},
			false,
			false,
		)
	case git2go.CreateFile:
		path = action.Path
		mode := "100644"
		if action.ExecutableMode {
			mode = "100755"
		}

		treeID, err = repo.AddTreeEntry(
			ctx,
			git.Revision(treeish),
			action.Path,
			&localrepo.TreeEntry{
				OID:  git.ObjectID(action.OID),
				Path: filepath.Base(action.Path),
				Mode: mode,
			},
			false,
			true,
		)
	case git2go.DeleteFile:
		path = action.Path
		treeID, err = repo.DeleteTreeEntry(
			ctx,
			git.Revision(treeish),
			action.Path,
		)
	default:
		return "", errors.New("unsupported action")
	}

	return treeID, translateToGit2GoError(err, path)
}

func translateToGit2GoError(err error, path string) error {
	switch err {
	case localrepo.ErrEntryNotFound:
		fallthrough
	case ErrNotFile:
		fallthrough
	case localrepo.ErrObjectNotFound:
		return git2go.IndexError{
			Path: path,
			Type: git2go.ErrFileNotFound,
		}
	case localrepo.ErrInvalidPath:
		//The error coming back from git2go has the path in single
		//quotes. This is to match the git2go error for now.
		//nolint:gitaly-linters
		return git2go.UnknownIndexError(
			fmt.Sprintf("invalid path: '%s'", path),
		)
	case localrepo.ErrPathTraversal:
		return git2go.IndexError{
			Path: path,
			Type: git2go.ErrDirectoryTraversal,
		}
	case localrepo.ErrEntryExists:
		return git2go.IndexError{
			Path: path,
			Type: git2go.ErrFileExists,
		}
	case localrepo.ErrDirExists:
		return git2go.IndexError{
			Path: path,
			Type: git2go.ErrDirectoryExists,
		}
	}
	return err
}

// ErrSignatureMissingNameOrEmail matches the git2go error
var ErrSignatureMissingNameOrEmail = errors.New(
	"commit: failed to parse signature - Signature cannot have an empty name or email",
)

func (s *Server) userCommitFilesGit(
	ctx context.Context,
	header *gitalypb.UserCommitFilesRequestHeader,
	parentCommitOID git.ObjectID,
	quarantineRepo *localrepo.Repo,
	repoPath string,
	actions []git2go.Action,
) (git.ObjectID, error) {
	now, err := dateFromProto(header)
	if err != nil {
		return "", structerr.NewInvalidArgument("%w", err)
	}

	var treeish git.ObjectID

	if parentCommitOID != "" {
		treeish, err = quarantineRepo.ResolveRevision(
			ctx,
			git.Revision(fmt.Sprintf("%s^{tree}", parentCommitOID)),
		)
		if err != nil {
			return "", err
		}
	}

	for _, action := range actions {
		if treeish, err = applyAction(
			ctx,
			action,
			treeish,
			quarantineRepo); err != nil {
			return "", err
		}
	}

	if treeish == "" {
		objectHash, err := quarantineRepo.ObjectHash(ctx)
		if err != nil {
			return "", fmt.Errorf("getting object hash: %w", err)
		}

		treeish = objectHash.EmptyTreeOID
	}

	cfg := localrepo.WriteCommitConfig{
		AuthorDate:     now,
		AuthorName:     strings.TrimSpace(string(header.CommitAuthorName)),
		AuthorEmail:    strings.TrimSpace(string(header.CommitAuthorEmail)),
		CommitterDate:  now,
		CommitterName:  strings.TrimSpace(string(header.User.Name)),
		CommitterEmail: strings.TrimSpace(string(header.User.Email)),
		Message:        string(header.CommitMessage),
		TreeID:         treeish,
	}

	if cfg.AuthorName == "" {
		cfg.AuthorName = cfg.CommitterName
	}

	if cfg.AuthorEmail == "" {
		cfg.AuthorEmail = cfg.CommitterEmail
	}

	if cfg.AuthorName == "" || cfg.AuthorEmail == "" {
		return "", structerr.NewInvalidArgument("%w", ErrSignatureMissingNameOrEmail)
	}

	if parentCommitOID != "" {
		cfg.Parents = []git.ObjectID{parentCommitOID}
	}

	return quarantineRepo.WriteCommit(
		ctx,
		cfg,
	)
}

func (s *Server) userCommitFilesGit2Go(
	ctx context.Context,
	header *gitalypb.UserCommitFilesRequestHeader,
	parentCommitOID git.ObjectID,
	quarantineRepo *localrepo.Repo,
	repoPath string,
	actions []git2go.Action,
) (git.ObjectID, error) {
	now, err := dateFromProto(header)
	if err != nil {
		return "", structerr.NewInvalidArgument("%w", err)
	}

	committer := git2go.NewSignature(string(header.User.Name), string(header.User.Email), now)
	author := committer
	if len(header.CommitAuthorName) > 0 && len(header.CommitAuthorEmail) > 0 {
		author = git2go.NewSignature(string(header.CommitAuthorName), string(header.CommitAuthorEmail), now)
	}

	commitID, err := s.git2goExecutor.Commit(ctx, quarantineRepo, git2go.CommitCommand{
		Repository: repoPath,
		Author:     author,
		Committer:  committer,
		Message:    string(header.CommitMessage),
		Parent:     parentCommitOID.String(),
		Actions:    actions,
	})
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}

	return commitID, nil
}

func (s *Server) userCommitFiles(ctx context.Context, header *gitalypb.UserCommitFilesRequestHeader, stream gitalypb.OperationService_UserCommitFilesServer) error {
	quarantineDir, quarantineRepo, err := s.quarantinedRepo(ctx, header.GetRepository())
	if err != nil {
		return err
	}

	repoPath, err := quarantineRepo.Path()
	if err != nil {
		return err
	}

	remoteRepo := header.GetStartRepository()
	if sameRepository(header.GetRepository(), remoteRepo) {
		// Some requests set a StartRepository that refers to the same repository as the target repository.
		// This check never works behind Praefect. See: https://gitlab.com/gitlab-org/gitaly/-/issues/3294
		// Plain Gitalies still benefit from identifying the case and avoiding unnecessary RPC to resolve the
		// branch.
		remoteRepo = nil
	}

	targetBranchName := git.NewReferenceNameFromBranchName(string(header.BranchName))
	targetBranchCommit, err := quarantineRepo.ResolveRevision(ctx, targetBranchName.Revision()+"^{commit}")
	if err != nil {
		if !errors.Is(err, git.ErrReferenceNotFound) {
			return fmt.Errorf("resolve target branch commit: %w", err)
		}

		// the branch is being created
	}

	var parentCommitOID git.ObjectID
	if header.StartSha == "" {
		parentCommitOID, err = s.resolveParentCommit(
			ctx,
			quarantineRepo,
			remoteRepo,
			targetBranchName,
			targetBranchCommit,
			string(header.StartBranchName),
		)
		if err != nil {
			return fmt.Errorf("resolve parent commit: %w", err)
		}
	} else {
		parentCommitOID, err = git.ObjectHashSHA1.FromHex(header.StartSha)
		if err != nil {
			return structerr.NewInvalidArgument("cannot resolve parent commit: %w", err)
		}
	}

	if parentCommitOID != targetBranchCommit {
		if err := s.fetchMissingCommit(ctx, quarantineRepo, remoteRepo, parentCommitOID); err != nil {
			return fmt.Errorf("fetch missing commit: %w", err)
		}
	}

	type action struct {
		header  *gitalypb.UserCommitFilesActionHeader
		content []byte
	}

	var pbActions []action

	for {
		req, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return fmt.Errorf("receive request: %w", err)
		}

		switch payload := req.GetAction().GetUserCommitFilesActionPayload().(type) {
		case *gitalypb.UserCommitFilesAction_Header:
			pbActions = append(pbActions, action{header: payload.Header})
		case *gitalypb.UserCommitFilesAction_Content:
			if len(pbActions) == 0 {
				return errors.New("content sent before action")
			}

			// append the content to the previous action
			content := &pbActions[len(pbActions)-1].content
			*content = append(*content, payload.Content...)
		default:
			return fmt.Errorf("unhandled action payload type: %T", payload)
		}
	}

	actions := make([]git2go.Action, 0, len(pbActions))
	for _, pbAction := range pbActions {
		if _, ok := gitalypb.UserCommitFilesActionHeader_ActionType_name[int32(pbAction.header.Action)]; !ok {
			return structerr.NewInvalidArgument("NoMethodError: undefined method `downcase' for %d:Integer", pbAction.header.Action)
		}

		path, err := validatePath(repoPath, string(pbAction.header.FilePath))
		if err != nil {
			return structerr.NewInvalidArgument("validate path: %w", err)
		}

		content := io.Reader(bytes.NewReader(pbAction.content))
		if pbAction.header.Base64Content {
			content = base64.NewDecoder(base64.StdEncoding, content)
		}

		switch pbAction.header.Action {
		case gitalypb.UserCommitFilesActionHeader_CREATE:
			blobID, err := quarantineRepo.WriteBlob(ctx, path, content)
			if err != nil {
				return fmt.Errorf("write created blob: %w", err)
			}

			actions = append(actions, git2go.CreateFile{
				OID:            blobID.String(),
				Path:           path,
				ExecutableMode: pbAction.header.ExecuteFilemode,
			})
		case gitalypb.UserCommitFilesActionHeader_CHMOD:
			actions = append(actions, git2go.ChangeFileMode{
				Path:           path,
				ExecutableMode: pbAction.header.ExecuteFilemode,
			})
		case gitalypb.UserCommitFilesActionHeader_MOVE:
			prevPath, err := validatePath(repoPath, string(pbAction.header.PreviousPath))
			if err != nil {
				return structerr.NewInvalidArgument("validate previous path: %w", err)
			}

			var oid git.ObjectID
			if !pbAction.header.InferContent {
				var err error
				oid, err = quarantineRepo.WriteBlob(ctx, path, content)
				if err != nil {
					return err
				}
			}
			actions = append(actions, git2go.MoveFile{
				Path:    prevPath,
				NewPath: path,
				OID:     oid.String(),
			})
		case gitalypb.UserCommitFilesActionHeader_UPDATE:
			oid, err := quarantineRepo.WriteBlob(ctx, path, content)
			if err != nil {
				return fmt.Errorf("write updated blob: %w", err)
			}

			actions = append(actions, git2go.UpdateFile{
				Path: path,
				OID:  oid.String(),
			})
		case gitalypb.UserCommitFilesActionHeader_DELETE:
			actions = append(actions, git2go.DeleteFile{
				Path: path,
			})
		case gitalypb.UserCommitFilesActionHeader_CREATE_DIR:
			actions = append(actions, git2go.CreateDirectory{
				Path: path,
			})
		}
	}

	var commitID git.ObjectID

	if featureflag.CommitFilesInGit.IsEnabled(ctx) {
		commitID, err = s.userCommitFilesGit(
			ctx,
			header,
			parentCommitOID,
			quarantineRepo,
			repoPath,
			actions,
		)
	} else {
		commitID, err = s.userCommitFilesGit2Go(
			ctx,
			header,
			parentCommitOID,
			quarantineRepo,
			repoPath,
			actions,
		)
	}

	if err != nil {
		return err
	}

	hasBranches, err := quarantineRepo.HasBranches(ctx)
	if err != nil {
		return fmt.Errorf("was repo created: %w", err)
	}

	var oldRevision git.ObjectID
	if expectedOldOID := header.GetExpectedOldOid(); expectedOldOID != "" {
		oldRevision, err = git.ObjectHashSHA1.FromHex(expectedOldOID)
		if err != nil {
			return structerr.NewInvalidArgument("invalid expected old object ID: %w", err).WithMetadata("old_object_id", expectedOldOID)
		}

		oldRevision, err = s.localrepo(header.GetRepository()).ResolveRevision(
			ctx, git.Revision(fmt.Sprintf("%s^{object}", oldRevision)),
		)
		if err != nil {
			return structerr.NewInvalidArgument("cannot resolve expected old object ID: %w", err).
				WithMetadata("old_object_id", expectedOldOID)
		}
	} else {
		oldRevision = parentCommitOID
		if targetBranchCommit == "" {
			oldRevision = git.ObjectHashSHA1.ZeroOID
		} else if header.Force {
			oldRevision = targetBranchCommit
		}
	}

	if err := s.updateReferenceWithHooks(ctx, header.GetRepository(), header.User, quarantineDir, targetBranchName, commitID, oldRevision); err != nil {
		if errors.As(err, &updateref.Error{}) {
			return structerr.NewFailedPrecondition("%w", err)
		}

		return fmt.Errorf("update reference: %w", err)
	}

	return stream.SendAndClose(&gitalypb.UserCommitFilesResponse{BranchUpdate: &gitalypb.OperationBranchUpdate{
		CommitId:      commitID.String(),
		RepoCreated:   !hasBranches,
		BranchCreated: git.ObjectHashSHA1.IsZeroOID(oldRevision),
	}})
}

func sameRepository(repoA, repoB *gitalypb.Repository) bool {
	return repoA.GetStorageName() == repoB.GetStorageName() &&
		repoA.GetRelativePath() == repoB.GetRelativePath()
}

func (s *Server) resolveParentCommit(
	ctx context.Context,
	local git.Repository,
	remote *gitalypb.Repository,
	targetBranch git.ReferenceName,
	targetBranchCommit git.ObjectID,
	startBranch string,
) (git.ObjectID, error) {
	if remote == nil && startBranch == "" {
		return targetBranchCommit, nil
	}

	repo := local
	if remote != nil {
		var err error
		repo, err = remoterepo.New(ctx, remote, s.conns)
		if err != nil {
			return "", fmt.Errorf("remote repository: %w", err)
		}
	}

	if hasBranches, err := repo.HasBranches(ctx); err != nil {
		return "", fmt.Errorf("has branches: %w", err)
	} else if !hasBranches {
		// GitLab sends requests to UserCommitFiles where target repository
		// and start repository are the same. If the request hits Gitaly directly,
		// Gitaly could check if the repos are the same by comparing their storages
		// and relative paths and simply resolve the branch locally. When request is proxied
		// through Praefect, the start repository's storage is not rewritten, thus Gitaly can't
		// identify the repos as being the same.
		//
		// If the start repository is set, we have to resolve the branch there as it
		// might be on a different commit than the local repository. As Gitaly can't identify
		// the repositories are the same behind Praefect, it has to perform an RPC to resolve
		// the branch. The resolving would fail as the branch does not yet exist in the start
		// repository, which is actually the local repository.
		//
		// Due to this, we check if the remote has any branches. If not, we likely hit this case
		// and we're creating the first branch. If so, we'll just return the commit that was
		// already resolved locally.
		//
		// See: https://gitlab.com/gitlab-org/gitaly/-/issues/3294
		return targetBranchCommit, nil
	}

	branch := targetBranch
	if startBranch != "" {
		branch = git.NewReferenceNameFromBranchName(startBranch)
	}
	refish := branch + "^{commit}"

	commit, err := repo.ResolveRevision(ctx, git.Revision(refish))
	if err != nil {
		return "", fmt.Errorf("resolving refish %q in %T: %w", refish, repo, err)
	}

	return commit, nil
}

func (s *Server) fetchMissingCommit(
	ctx context.Context,
	localRepo *localrepo.Repo,
	remoteRepo *gitalypb.Repository,
	commit git.ObjectID,
) error {
	if _, err := localRepo.ResolveRevision(ctx, commit.Revision()+"^{commit}"); err != nil {
		if !errors.Is(err, git.ErrReferenceNotFound) || remoteRepo == nil {
			return fmt.Errorf("lookup parent commit: %w", err)
		}

		if err := localRepo.FetchInternal(
			ctx,
			remoteRepo,
			[]string{commit.String()},
			localrepo.FetchOpts{Tags: localrepo.FetchOptsTagsNone},
		); err != nil {
			return fmt.Errorf("fetch parent commit: %w", err)
		}
	}

	return nil
}

func validateUserCommitFilesHeader(header *gitalypb.UserCommitFilesRequestHeader) error {
	if err := service.ValidateRepository(header.GetRepository()); err != nil {
		return err
	}
	if header.GetUser() == nil {
		return errors.New("empty User")
	}
	if len(header.GetCommitMessage()) == 0 {
		return errors.New("empty CommitMessage")
	}
	if len(header.GetBranchName()) == 0 {
		return errors.New("empty BranchName")
	}

	startSha := header.GetStartSha()
	if len(startSha) > 0 {
		err := git.ObjectHashSHA1.ValidateHex(startSha)
		if err != nil {
			return err
		}
	}

	return nil
}
