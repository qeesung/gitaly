package objectpool

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// DisconnectGitAlternates is a slightly dangerous RPC. It optimistically hard-links all alternate
// objects we might need, and then temporarily removes (renames) objects/info/alternates and runs
// a connectivity check. If we are unlucky that leaves the repository in a broken state during the
// connectivity check. If we are very unlucky and Gitaly crashes, the repository stays in a broken
// state until an administrator intervenes and restores the backed-up copy of
// objects/info/alternates.
func (s *server) DisconnectGitAlternates(ctx context.Context, req *gitalypb.DisconnectGitAlternatesRequest) (*gitalypb.DisconnectGitAlternatesResponse, error) {
	repository := req.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(repository)

	if err := objectpool.Disconnect(ctx, repo, s.logger, s.txManager); err != nil {
		return nil, structerr.NewInternal("%w", err)
	}

	return &gitalypb.DisconnectGitAlternatesResponse{}, nil
}
