package commit

import (
	"context"
	"errors"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func validateCommitIsAncestorRequest(ctx context.Context, locator storage.Locator, in *gitalypb.CommitIsAncestorRequest) error {
	if err := locator.ValidateRepository(ctx, in.GetRepository()); err != nil {
		return err
	}
	if in.GetAncestorId() == "" {
		return errors.New("empty ancestor sha")
	}
	if in.GetChildId() == "" {
		return errors.New("empty child sha")
	}
	return nil
}

func (s *server) CommitIsAncestor(ctx context.Context, in *gitalypb.CommitIsAncestorRequest) (*gitalypb.CommitIsAncestorResponse, error) {
	if err := validateCommitIsAncestorRequest(ctx, s.locator, in); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	ret, err := s.commitIsAncestorName(ctx, in.Repository, in.AncestorId, in.ChildId)
	return &gitalypb.CommitIsAncestorResponse{Value: ret}, err
}

// Assumes that `path`, `ancestorID` and `childID` are populated :trollface:
func (s *server) commitIsAncestorName(ctx context.Context, repo *gitalypb.Repository, ancestorID, childID string) (bool, error) {
	s.logger.WithFields(log.Fields{
		"ancestorSha": ancestorID,
		"childSha":    childID,
	}).DebugContext(ctx, "commitIsAncestor")

	cmd, err := s.gitCmdFactory.New(ctx, repo, git.Command{
		Name:  "merge-base",
		Flags: []git.Option{git.Flag{Name: "--is-ancestor"}}, Args: []string{ancestorID, childID},
	})
	if err != nil {
		return false, structerr.NewInternal("%w", err)
	}

	return cmd.Wait() == nil, nil
}
