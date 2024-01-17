package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gitattributes"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) GetFileAttributes(ctx context.Context, in *gitalypb.GetFileAttributesRequest) (*gitalypb.GetFileAttributesResponse, error) {
	if err := validateGetFileAttributesRequest(s.locator, in); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(in.GetRepository())

	// In git 2.43.0+, gitattributes supports reading from HEAD:.gitattributes,
	// so info/attributes is no longer needed. To make sure info/attributes file is cleaned up,
	// we delete it if it exists when reading from HEAD:.gitattributes is called.
	// This logic can be removed when ApplyGitattributes and GetInfoAttributes PRC are totally removed from
	// the code base.
	repoPath, err := s.locator.GetRepoPath(repo)
	if err != nil {
		s.logger.WithError(err).Error("failed to get repo path of info/gitattributes")
	} else {
		deletionErr := deleteInfoAttributesFile(repoPath)
		if !os.IsNotExist(deletionErr) {
			s.logger.WithError(deletionErr).Error("failed to delete info/gitattributes file at " + repoPath)
		}
	}

	checkAttrCmd, finishAttr, err := gitattributes.CheckAttr(ctx, repo, git.Revision(in.GetRevision()), in.GetAttributes())
	if err != nil {
		return nil, structerr.New("check attr: %w", err)
	}

	defer finishAttr()

	var attrValues []*gitalypb.GetFileAttributesResponse_AttributeInfo

	for _, path := range in.GetPaths() {
		attrs, err := checkAttrCmd.Check(path)
		if err != nil {
			return nil, structerr.New("check attr: %w", err)
		}

		for _, attr := range attrs {
			attrValues = append(attrValues, &gitalypb.GetFileAttributesResponse_AttributeInfo{Path: path, Attribute: attr.Name, Value: attr.State})
		}
	}

	return &gitalypb.GetFileAttributesResponse{AttributeInfos: attrValues}, nil
}

func validateGetFileAttributesRequest(locator storage.Locator, in *gitalypb.GetFileAttributesRequest) error {
	if err := locator.ValidateRepository(in.GetRepository()); err != nil {
		return err
	}

	if len(in.GetRevision()) == 0 {
		return errors.New("revision is required")
	}

	if len(in.GetPaths()) == 0 {
		return errors.New("file paths are required")
	}

	if len(in.GetAttributes()) == 0 {
		return errors.New("attributes are required")
	}

	return nil
}

// deleteInfoAttributesFile delete the info/attributes file in the repoPath if it exists
func deleteInfoAttributesFile(repoPath string) error {
	attrFile := filepath.Join(repoPath, "info", "attributes")
	err := os.Remove(attrFile)
	if err != nil {
		return err
	}
	return nil
}
