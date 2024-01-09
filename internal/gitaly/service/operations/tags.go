package operations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"

	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func validateUserDeleteTagRequest(locator storage.Locator, in *gitalypb.UserDeleteTagRequest) error {
	if err := locator.ValidateRepository(in.GetRepository()); err != nil {
		return err
	}
	if len(in.GetTagName()) == 0 {
		return errors.New("empty tag name")
	}
	if in.GetUser() == nil {
		return errors.New("empty user")
	}
	return nil
}

// UserDeleteTag deletes an existing tag.
func (s *Server) UserDeleteTag(ctx context.Context, req *gitalypb.UserDeleteTagRequest) (*gitalypb.UserDeleteTagResponse, error) {
	if err := validateUserDeleteTagRequest(s.locator, req); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}
	referenceName := git.ReferenceName(fmt.Sprintf("refs/tags/%s", req.TagName))

	repo := s.localrepo(req.GetRepository())

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting object format: %w", err)
	}

	var revision git.ObjectID
	if expectedOldOID := req.GetExpectedOldOid(); expectedOldOID != "" {
		revision, err = objectHash.FromHex(expectedOldOID)
		if err != nil {
			return nil, structerr.NewInvalidArgument("invalid expected old object ID: %w", err).WithMetadata("old_object_id", expectedOldOID)
		}

		revision, err = repo.ResolveRevision(
			ctx, git.Revision(fmt.Sprintf("%s^{object}", revision)),
		)
		if err != nil {
			return nil, structerr.NewInvalidArgument("cannot resolve expected old object ID: %w", err).
				WithMetadata("old_object_id", expectedOldOID)
		}
	} else {
		revision, err = repo.ResolveRevision(ctx, referenceName.Revision())
		if err != nil {
			if errors.Is(err, git.ErrReferenceNotFound) {
				return nil, structerr.NewFailedPrecondition("tag not found: %s", req.TagName)
			}
			return nil, structerr.NewInternal("resolving revision %q: %w", referenceName, err).WithMetadata("tag", req.TagName)
		}
	}

	if err := s.updateReferenceWithHooks(ctx, req.Repository, req.User, nil, referenceName, objectHash.ZeroOID, revision); err != nil {
		if featureflag.ReturnStructuredErrorsInUserDeleteTag.IsEnabled(ctx) {
			var notAllowedError hook.NotAllowedError
			var customHookErr updateref.CustomHookError
			var updateRefError updateref.Error

			if errors.As(err, &notAllowedError) {
				return nil, structerr.NewPermissionDenied("deletion denied by access checks: %w", err).WithDetail(
					&gitalypb.UserDeleteTagError{
						Error: &gitalypb.UserDeleteTagError_AccessCheck{
							AccessCheck: &gitalypb.AccessCheckError{
								ErrorMessage: notAllowedError.Message,
								UserId:       notAllowedError.UserID,
								Protocol:     notAllowedError.Protocol,
								Changes:      notAllowedError.Changes,
							},
						},
					},
				)
			} else if errors.As(err, &customHookErr) {
				return nil, structerr.NewPermissionDenied("deletion denied by custom hooks: %w", err).WithDetail(
					&gitalypb.UserDeleteTagError{
						Error: &gitalypb.UserDeleteTagError_CustomHook{
							CustomHook: customHookErr.Proto(),
						},
					},
				)
			} else if errors.As(err, &updateRefError) {
				return nil, structerr.NewFailedPrecondition("reference update failed: %w", updateRefError).WithDetail(
					&gitalypb.UserDeleteTagError{
						Error: &gitalypb.UserDeleteTagError_ReferenceUpdate{
							ReferenceUpdate: &gitalypb.ReferenceUpdateError{
								ReferenceName: []byte(updateRefError.Reference.String()),
								OldOid:        updateRefError.OldOID.String(),
								NewOid:        updateRefError.NewOID.String(),
							},
						},
					},
				)
			}
		}

		var customHookErr updateref.CustomHookError
		if errors.As(err, &customHookErr) {
			return nil, structerr.NewPermissionDenied("deletion denied by custom hooks: %w", err).WithDetail(
				&gitalypb.UserDeleteTagError{
					Error: &gitalypb.UserDeleteTagError_CustomHook{
						CustomHook: customHookErr.Proto(),
					},
				},
			)
		}

		var updateRefError updateref.Error
		if errors.As(err, &updateRefError) {
			return nil, structerr.NewFailedPrecondition("%w", err)
		}
		return nil, structerr.NewInternal("%w", err)
	}

	return &gitalypb.UserDeleteTagResponse{}, nil
}

func validateUserCreateTag(locator storage.Locator, req *gitalypb.UserCreateTagRequest) error {
	if len(req.TagName) == 0 {
		return errors.New("empty tag name")
	}

	if err := git.ValidateRevision(req.TagName); err != nil {
		return fmt.Errorf("invalid tag name: %w", err)
	}

	if req.User == nil {
		return errors.New("empty user")
	}

	if len(req.TargetRevision) == 0 {
		return errors.New("empty target revision")
	}

	if bytes.Contains(req.Message, []byte("\000")) {
		return errors.New("tag message contains NUL byte")
	}

	if err := locator.ValidateRepository(req.GetRepository()); err != nil {
		return err
	}

	return nil
}

//nolint:revive // This is unintentionally missing documentation.
func (s *Server) UserCreateTag(ctx context.Context, req *gitalypb.UserCreateTagRequest) (*gitalypb.UserCreateTagResponse, error) {
	if err := validateUserCreateTag(s.locator, req); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	targetRevision := git.Revision(req.TargetRevision)
	referenceName := git.ReferenceName(fmt.Sprintf("refs/tags/%s", req.TagName))

	taggerSignature, err := git.SignatureFromRequest(req)
	if err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	quarantineDir, quarantineRepo, err := s.quarantinedRepo(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	objectHash, err := quarantineRepo.ObjectHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting object hash: %w", err)
	}

	tag, tagID, err := s.createTag(ctx, quarantineRepo, targetRevision, req.TagName, req.Message, taggerSignature)
	if err != nil {
		return nil, err
	}

	// We check whether the reference exists up-front so that we can return a proper
	// detailed error to the caller in case the tag cannot be created. While this is
	// racy given that the tag can be created afterwards by a concurrent caller, we'd
	// detect that raciness in `updateReferenceWithHooks()` anyway.
	if oid, err := quarantineRepo.ResolveRevision(ctx, referenceName.Revision()); err != nil {
		// If the reference wasn't found we're on the happy path, otherwise
		// something has gone wrong.
		if !errors.Is(err, git.ErrReferenceNotFound) {
			return nil, structerr.NewInternal("resolving target reference: %w", err)
		}
	} else {
		// When resolving the revision succeeds the tag reference exists already.
		// Because we don't want to overwrite preexisting references in this RPC we
		// thus return an error and alert the caller of this condition.
		return nil, structerr.NewAlreadyExists("tag reference exists already").WithDetail(
			&gitalypb.UserCreateTagError{
				Error: &gitalypb.UserCreateTagError_ReferenceExists{
					ReferenceExists: &gitalypb.ReferenceExistsError{
						ReferenceName: []byte(referenceName.String()),
						Oid:           oid.String(),
					},
				},
			},
		)
	}

	if err := s.updateReferenceWithHooks(ctx, req.Repository, req.User, quarantineDir, referenceName, tagID, objectHash.ZeroOID); err != nil {
		var notAllowedError hook.NotAllowedError
		var customHookErr updateref.CustomHookError
		var updateRefError updateref.Error

		if errors.As(err, &notAllowedError) {
			return nil, structerr.NewPermissionDenied("reference update denied by access checks: %w", err).WithDetail(
				&gitalypb.UserCreateTagError{
					Error: &gitalypb.UserCreateTagError_AccessCheck{
						AccessCheck: &gitalypb.AccessCheckError{
							ErrorMessage: notAllowedError.Message,
							UserId:       notAllowedError.UserID,
							Protocol:     notAllowedError.Protocol,
							Changes:      notAllowedError.Changes,
						},
					},
				},
			)
		} else if errors.As(err, &customHookErr) {
			// We explicitly don't include the custom hook error itself
			// in the returned error because that would also contain the
			// standard output or standard error in the error message.
			// It's thus needlessly verbose and duplicates information
			// we have available in the structured error anyway.
			return nil, structerr.NewPermissionDenied("reference update denied by custom hooks").WithDetail(
				&gitalypb.UserCreateTagError{
					Error: &gitalypb.UserCreateTagError_CustomHook{
						CustomHook: customHookErr.Proto(),
					},
				},
			)
		} else if errors.As(err, &updateRefError) {
			return nil, structerr.NewFailedPrecondition("reference update failed: %w", err).WithDetail(
				&gitalypb.UserCreateTagError{
					Error: &gitalypb.UserCreateTagError_ReferenceUpdate{
						ReferenceUpdate: &gitalypb.ReferenceUpdateError{
							ReferenceName: []byte(updateRefError.Reference.String()),
							OldOid:        updateRefError.OldOID.String(),
							NewOid:        updateRefError.NewOID.String(),
						},
					},
				},
			)
		}

		return nil, structerr.NewInternal("updating reference: %w", err)
	}

	return &gitalypb.UserCreateTagResponse{
		Tag: tag,
	}, nil
}

func (s *Server) createTag(
	ctx context.Context,
	repo *localrepo.Repo,
	targetRevision git.Revision,
	tagName []byte,
	message []byte,
	tagger git.Signature,
) (*gitalypb.Tag, git.ObjectID, error) {
	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return nil, "", structerr.NewInternal("creating object reader: %w", err)
	}
	defer cancel()

	objectInfoReader, cancel, err := s.catfileCache.ObjectInfoReader(ctx, repo)
	if err != nil {
		return nil, "", structerr.NewInternal("creating object info reader: %w", err)
	}
	defer cancel()

	// We allow all ways to name a revision that cat-file
	// supports, not just OID. Resolve it.
	targetInfo, err := objectInfoReader.Info(ctx, targetRevision)
	if err != nil {
		return nil, "", structerr.NewFailedPrecondition("revspec '%s' not found", targetRevision)
	}
	targetObjectID, targetObjectType := targetInfo.Oid, targetInfo.Type

	// Whether we have a "message" parameter tells us if we're
	// making a lightweight tag or an annotated tag. Maybe we can
	// use strings.TrimSpace() eventually, but as the tests show
	// the Ruby idea of Unicode whitespace is different than its
	// idea.
	makingTag := regexp.MustCompile(`\S`).Match(message)

	// If we're creating a tag to another "tag" we'll need to peel
	// (possibly recursively) all the way down to the
	// non-tag. That'll be our returned TargetCommit if it's a
	// commit (nil if tree or blob).
	//
	// If we're not pointing to a tag we pretend our "peeled" is
	// the commit/tree/blob object itself in subsequent logic.
	peeledTargetObjectID, peeledTargetObjectType := targetObjectID, targetObjectType
	if targetObjectType == "tag" {
		peeledTargetObjectInfo, err := objectInfoReader.Info(ctx, targetRevision+"^{}")
		if err != nil {
			return nil, "", structerr.NewInternal("reading object info: %w", err)
		}
		peeledTargetObjectID, peeledTargetObjectType = peeledTargetObjectInfo.Oid, peeledTargetObjectInfo.Type

		// If we're not making an annotated tag ourselves and
		// the user pointed to a "tag" we'll ignore what they
		// asked for and point to what we found when peeling
		// that tag.
		if !makingTag {
			targetObjectID = peeledTargetObjectID
		}
	}

	// At this point we'll either be pointing to an object we were
	// provided with, or creating a new tag object and pointing to
	// that.
	refObjectID := targetObjectID
	var tagObject *gitalypb.Tag
	if makingTag {
		tagObjectID, err := repo.WriteTag(ctx, targetObjectID, targetObjectType, tagName, message, tagger)
		if err != nil {
			var formatTagErr localrepo.FormatTagError
			if errors.As(err, &formatTagErr) {
				return nil, "", structerr.NewInvalidArgument("formatting tag: %w", formatTagErr)
			}

			var MktagError localrepo.MktagError
			if errors.As(err, &MktagError) {
				return nil, "", structerr.NewNotFound("Gitlab::Git::CommitError: %s", err.Error())
			}
			return nil, "", structerr.NewInternal("writing tag: %w", err)
		}

		createdTag, err := catfile.GetTag(ctx, objectReader, tagObjectID.Revision(), string(tagName))
		if err != nil {
			return nil, "", structerr.NewInternal("getting tag: %w", err)
		}

		tagObject = &gitalypb.Tag{
			Name:        tagName,
			Id:          tagObjectID.String(),
			Message:     createdTag.Message,
			MessageSize: createdTag.MessageSize,
			// TargetCommit: is filled in below if needed
		}

		refObjectID = tagObjectID
	} else {
		tagObject = &gitalypb.Tag{
			Name: tagName,
			Id:   peeledTargetObjectID.String(),
			// TargetCommit: is filled in below if needed
		}
	}

	// Save ourselves looking this up earlier in case update-ref
	// died
	if peeledTargetObjectType == "commit" {
		peeledTargetCommit, err := catfile.GetCommit(ctx, objectReader, peeledTargetObjectID.Revision())
		if err != nil {
			return nil, "", structerr.NewInternal("getting commit: %w", err)
		}
		tagObject.TargetCommit = peeledTargetCommit
	}

	return tagObject, refObjectID, nil
}
