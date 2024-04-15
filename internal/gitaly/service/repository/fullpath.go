package repository

import (
	"context"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const fullPathKey = "gitlab.fullpath"

// SetFullPath writes the provided path value into the repository's gitconfig under the
// "gitlab.fullpath" key.
func (s *server) SetFullPath(
	ctx context.Context,
	request *gitalypb.SetFullPathRequest,
) (*gitalypb.SetFullPathResponse, error) {
	if s.walPartitionManager != nil {
		return nil, structerr.NewUnimplemented("SetFullPath RPC is not supported by WAL")
	}

	repository := request.GetRepository()
	if err := s.locator.ValidateRepository(repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	if len(request.GetPath()) == 0 {
		return nil, structerr.NewInvalidArgument("no path provided")
	}

	repo := s.localrepo(repository)

	if err := repo.SetConfig(ctx, fullPathKey, request.GetPath(), s.txManager); err != nil {
		return nil, structerr.NewInternal("setting config: %w", err)
	}

	return &gitalypb.SetFullPathResponse{}, nil
}

// FullPath reads the path from the repository's gitconfig under the
// "gitlab.fullpath" key.
func (s *server) FullPath(ctx context.Context, request *gitalypb.FullPathRequest) (*gitalypb.FullPathResponse, error) {
	repository := request.GetRepository()
	if err := s.locator.ValidateRepository(repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(repository)
	var stdout strings.Builder
	err := repo.ExecAndWait(ctx, git.Command{
		Name: "config",
		Args: []string{fullPathKey},
	}, git.WithStdout(&stdout))
	if err != nil {
		return nil, structerr.NewInternal("fetch config: %w", err)
	}

	return &gitalypb.FullPathResponse{
		Path: strings.TrimSuffix(stdout.String(), "\n"),
	}, nil
}
