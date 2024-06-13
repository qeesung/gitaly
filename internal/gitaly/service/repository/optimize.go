package repository

import (
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	housekeepingmgr "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/manager"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) OptimizeRepository(ctx context.Context, in *gitalypb.OptimizeRepositoryRequest) (*gitalypb.OptimizeRepositoryResponse, error) {
	if err := s.validateOptimizeRepositoryRequest(in); err != nil {
		return nil, err
	}

	repo := s.localrepo(in.GetRepository())

	gitVersion, err := repo.GitVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting Git version: %w", err)
	}

	var strategyConstructor housekeepingmgr.OptimizationStrategyConstructor
	switch in.GetStrategy() {
	case gitalypb.OptimizeRepositoryRequest_STRATEGY_UNSPECIFIED, gitalypb.OptimizeRepositoryRequest_STRATEGY_HEURISTICAL:
		strategyConstructor = func(info stats.RepositoryInfo) housekeeping.OptimizationStrategy {
			return housekeeping.NewHeuristicalOptimizationStrategy(gitVersion, info)
		}
	case gitalypb.OptimizeRepositoryRequest_STRATEGY_EAGER:
		strategyConstructor = func(info stats.RepositoryInfo) housekeeping.OptimizationStrategy {
			return housekeeping.NewEagerOptimizationStrategy(info)
		}
	default:
		return nil, structerr.NewInvalidArgument("unsupported optimization strategy %d", in.GetStrategy())
	}

	if err := s.housekeepingManager.OptimizeRepository(ctx, repo,
		housekeepingmgr.WithOptimizationStrategyConstructor(strategyConstructor),
		housekeepingmgr.WithUseExistingTransaction(),
	); err != nil {
		return nil, structerr.NewInternal("%w", err)
	}

	return &gitalypb.OptimizeRepositoryResponse{}, nil
}

func (s *server) validateOptimizeRepositoryRequest(in *gitalypb.OptimizeRepositoryRequest) error {
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(repository); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	_, err := s.locator.GetRepoPath(repository)
	if err != nil {
		return err
	}

	return nil
}
