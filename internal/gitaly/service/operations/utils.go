package operations

import (
	"context"
	"errors"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type cherryPickOrRevertRequest interface {
	GetRepository() *gitalypb.Repository
	GetUser() *gitalypb.User
	GetCommit() *gitalypb.GitCommit
	GetBranchName() []byte
	GetMessage() []byte
}

func validateCherryPickOrRevertRequest(ctx context.Context, locator storage.Locator, req cherryPickOrRevertRequest) error {
	if err := locator.ValidateRepository(ctx, req.GetRepository()); err != nil {
		return err
	}

	if req.GetUser() == nil {
		return errors.New("empty User")
	}

	if req.GetCommit() == nil {
		return errors.New("empty Commit")
	}

	if len(req.GetBranchName()) == 0 {
		return errors.New("empty BranchName")
	}

	if len(req.GetMessage()) == 0 {
		return errors.New("empty Message")
	}

	return nil
}
