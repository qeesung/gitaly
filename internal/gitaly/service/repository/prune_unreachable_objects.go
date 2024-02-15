package repository

import (
	"context"
	"fmt"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// PruneUnreachableObjects prunes objects which aren't reachable from any of its references. To
// ensure that concurrently running commands do not reference those objects anymore when we execute
// the prune we enforce a grace-period: objects will only be pruned if they haven't been accessed
// for at least 30 minutes.
func (s *server) PruneUnreachableObjects(
	ctx context.Context,
	request *gitalypb.PruneUnreachableObjectsRequest,
) (*gitalypb.PruneUnreachableObjectsResponse, error) {
	repository := request.GetRepository()
	if err := s.locator.ValidateRepository(repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(repository)

	// Verify that the repository exists on-disk such that we can return a proper gRPC code in
	// case it doesn't.
	if _, err := repo.Path(); err != nil {
		return nil, err
	}

	// Verify that the repository is not an object pool. Pruning objects in object pools is not
	// a safe operation and is likely to cause corruption of object pool members.
	repoInfo, err := stats.RepositoryInfoForRepository(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("deriving repository info: %w", err)
	}
	if repoInfo.IsObjectPool {
		return nil, structerr.NewInvalidArgument("pruning objects for object pool")
	}

	expireBefore := time.Now().Add(-30 * time.Minute)

	// We need to prune loose unreachable objects that exist in the repository.
	if err := housekeeping.PruneObjects(ctx, repo, housekeeping.PruneObjectsConfig{
		ExpireBefore: expireBefore,
	}); err != nil {
		return nil, structerr.NewInternal("pruning objects: %w", err)
	}

	// But we also have to prune unreachable objects part of cruft packs. The only way to do
	// that is to do a full repack. So unfortunately, this is quite expensive.
	if err := housekeeping.RepackObjects(ctx, repo, housekeeping.RepackObjectsConfig{
		Strategy:            housekeeping.RepackObjectsStrategyFullWithCruft,
		WriteMultiPackIndex: true,
		WriteBitmap:         len(repoInfo.Alternates.ObjectDirectories) == 0,
		CruftExpireBefore:   expireBefore,
	}); err != nil {
		return nil, structerr.NewInternal("repacking objects: %w", err)
	}

	// Rewrite the commit-graph so that it doesn't contain references to pruned commits
	// anymore.
	if err := housekeeping.WriteCommitGraph(ctx, repo, housekeeping.WriteCommitGraphConfig{
		ReplaceChain: true,
	}); err != nil {
		return nil, structerr.NewInternal("rewriting commit-graph: %w", err)
	}

	stats.LogRepositoryInfo(ctx, s.logger, repo)

	return &gitalypb.PruneUnreachableObjectsResponse{}, nil
}
