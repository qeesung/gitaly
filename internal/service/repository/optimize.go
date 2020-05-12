package repository

import (
	"context"
	"os"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func (s *server) optimizeRepository(ctx context.Context, repository *gitalypb.Repository) error {
	hasBitmap, err := stats.HasBitmap(repository)
	if err != nil {
		return helper.ErrInternal(err)
	}

	if !hasBitmap {
		altFile, err := git.InfoAlternatesPath(repository)
		if err != nil {
			return helper.ErrInternal(err)
		}

		// repositories with alternates should never have a bitmap, as Git will otherwise complain about
		// multiple bitmaps being present in parent and alternate repository.
		if _, err = os.Stat(altFile); !os.IsNotExist(err) {
			return nil
		}

		_, err = s.RepackFull(ctx, &gitalypb.RepackFullRequest{Repository: repository, CreateBitmap: true})
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *server) OptimizeRepository(ctx context.Context, in *gitalypb.OptimizeRepositoryRequest) (*gitalypb.OptimizeRepositoryResponse, error) {
	if err := validateOptimizeRepositoryRequest(in); err != nil {
		return nil, err
	}

	if err := s.optimizeRepository(ctx, in.GetRepository()); err != nil {
		return nil, helper.ErrInternal(err)
	}

	return &gitalypb.OptimizeRepositoryResponse{}, nil
}

func validateOptimizeRepositoryRequest(in *gitalypb.OptimizeRepositoryRequest) error {
	if in.GetRepository() == nil {
		return helper.ErrInvalidArgumentf("empty repository")
	}

	_, err := helper.GetRepoPath(in.GetRepository())
	if err != nil {
		return err
	}

	return nil
}
