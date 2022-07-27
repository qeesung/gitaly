package operations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

//nolint: revive,stylecheck // This is unintentionally missing documentation.
func (s *Server) UserDeleteTag(ctx context.Context, req *gitalypb.UserDeleteTagRequest) (*gitalypb.UserDeleteTagResponse, error) {
	if len(req.TagName) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "empty tag name")
	}

	if req.User == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty user")
	}

	referenceName := git.ReferenceName(fmt.Sprintf("refs/tags/%s", req.TagName))
	revision, err := s.localrepo(req.GetRepository()).ResolveRevision(ctx, referenceName.Revision())
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "tag not found: %s", req.TagName)
	}

	if err := s.updateReferenceWithHooks(ctx, req.Repository, req.User, nil, referenceName, git.ObjectHashSHA1.ZeroOID, revision); err != nil {
		var customHookErr updateref.CustomHookError
		if errors.As(err, &customHookErr) {
			return &gitalypb.UserDeleteTagResponse{
				PreReceiveError: customHookErr.Error(),
			}, nil
		}

		var updateRefError updateref.Error
		if errors.As(err, &updateRefError) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}

		return nil, err
	}

	return &gitalypb.UserDeleteTagResponse{}, nil
}

func validateUserCreateTag(req *gitalypb.UserCreateTagRequest) error {
	// Emulate validations done by Ruby. A lot of these (e.g. the
	// upper-case error messages) can be simplified once we're not
	// doing bug-for-bug Ruby emulation anymore)
	if len(req.TagName) == 0 {
		return status.Errorf(codes.InvalidArgument, "empty tag name")
	}

	if bytes.Contains(req.TagName, []byte(" ")) {
		return status.Errorf(codes.Unknown, "Gitlab::Git::CommitError: Could not update refs/tags/%s. Please refresh and try again.", req.TagName)
	}

	if req.User == nil {
		return status.Errorf(codes.InvalidArgument, "empty user")
	}

	if len(req.TargetRevision) == 0 {
		return status.Error(codes.InvalidArgument, "empty target revision")
	}

	if bytes.Contains(req.Message, []byte("\000")) {
		return status.Errorf(codes.Unknown, "ArgumentError: string contains null byte")
	}

	// Our own Go-specific validation
	if req.GetRepository() == nil {
		return status.Errorf(codes.Internal, "empty repository")
	}

	return nil
}

//nolint: revive,stylecheck // This is unintentionally missing documentation.
func (s *Server) UserCreateTag(ctx context.Context, req *gitalypb.UserCreateTagRequest) (*gitalypb.UserCreateTagResponse, error) {
	// Validate the request
	if err := validateUserCreateTag(req); err != nil {
		return nil, err
	}

	targetRevision := git.Revision(req.TargetRevision)

	committerTime, err := dateFromProto(req)
	if err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	quarantineDir, quarantineRepo, err := s.quarantinedRepo(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	tag, tagID, err := s.createTag(ctx, quarantineRepo, targetRevision, req.TagName, req.Message, req.User, committerTime)
	if err != nil {
		return nil, err
	}

	referenceName := git.ReferenceName(fmt.Sprintf("refs/tags/%s", req.TagName))
	if err := s.updateReferenceWithHooks(ctx, req.Repository, req.User, quarantineDir, referenceName, tagID, git.ObjectHashSHA1.ZeroOID); err != nil {
		var customHookErrr updateref.CustomHookError
		if errors.As(err, &customHookErrr) {
			// TODO: this case should return a CustomHookError.
			return &gitalypb.UserCreateTagResponse{
				PreReceiveError: customHookErrr.Error(),
			}, nil
		}

		var updateRefError updateref.Error
		if errors.As(err, &updateRefError) {
			refNameOK, err := git.CheckRefFormat(ctx, s.gitCmdFactory, referenceName.String())
			if err != nil {
				// Should only be reachable if "git
				// check-ref-format"'s invocation is
				// incorrect, or if it segfaults on startup
				// etc.
				return nil, status.Error(codes.Internal, err.Error())
			}

			if refNameOK {
				// The tag might not actually exist,
				// perhaps update-ref died for some
				// other reason. But saying that it
				// does is what the Ruby code used to
				// do, so let's follow it off that
				// cliff.
				//
				// TODO: this case should return a ReferenceExistsError.
				return &gitalypb.UserCreateTagResponse{
					Tag:    nil,
					Exists: true,
				}, nil
			}

			// It doesn't make sense either to
			// tell the user to retry with an
			// invalid ref name, but ditto on the
			// Ruby bug-for-bug emulation.
			//
			// TODO: this case should return a ReferenceUpdateError.
			return &gitalypb.UserCreateTagResponse{
				Tag:    nil,
				Exists: true,
			}, status.Errorf(codes.Unknown, "Gitlab::Git::CommitError: Could not update refs/tags/%s. Please refresh and try again.", req.TagName)
		}

		// The Ruby code did not return this, but always an
		// "Exists: true" on update-ref failure without any
		// meaningful error. This is our "PANIC" response, if
		// we've got an unknown error (it should all be
		// updateRefError above) let's relay it to the
		// caller. This should not happen.
		//
		// TODO: this case should either return an AccessCheckError in case Rails refused
		// the update, or alternatively an Internal error.
		return &gitalypb.UserCreateTagResponse{
			Tag:    nil,
			Exists: true,
		}, status.Error(codes.Internal, err.Error())
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
	committer *gitalypb.User,
	committerTime time.Time,
) (*gitalypb.Tag, git.ObjectID, error) {
	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return nil, "", status.Error(codes.Internal, err.Error())
	}
	defer cancel()

	objectInfoReader, cancel, err := s.catfileCache.ObjectInfoReader(ctx, repo)
	if err != nil {
		return nil, "", status.Error(codes.Internal, err.Error())
	}
	defer cancel()

	// We allow all ways to name a revision that cat-file
	// supports, not just OID. Resolve it.
	targetInfo, err := objectInfoReader.Info(ctx, targetRevision)
	if err != nil {
		return nil, "", status.Errorf(codes.FailedPrecondition, "revspec '%s' not found", targetRevision)
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
			return nil, "", status.Error(codes.Internal, err.Error())
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
		tagObjectID, err := repo.WriteTag(ctx, targetObjectID, targetObjectType, tagName, message, committer, committerTime)
		if err != nil {
			var FormatTagError localrepo.FormatTagError
			if errors.As(err, &FormatTagError) {
				return nil, "", status.Errorf(codes.Unknown, "Rugged::InvalidError: failed to parse signature - expected prefix doesn't match actual")
			}

			var MktagError localrepo.MktagError
			if errors.As(err, &MktagError) {
				return nil, "", status.Errorf(codes.NotFound, "Gitlab::Git::CommitError: %s", err.Error())
			}
			return nil, "", status.Error(codes.Internal, err.Error())
		}

		createdTag, err := catfile.GetTag(ctx, objectReader, tagObjectID.Revision(), string(tagName))
		if err != nil {
			return nil, "", status.Error(codes.Internal, err.Error())
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
			return nil, "", status.Error(codes.Internal, err.Error())
		}
		tagObject.TargetCommit = peeledTargetCommit
	}

	return tagObject, refObjectID, nil
}
