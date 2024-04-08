package repository

import (
	"context"
	"fmt"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	housekeepingcfg "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/config"
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

	// If WAL transaction is enabled, there shouldn't be any loose objects in the repository. New objects are always
	// packed and attached to a log entry. During the migration to WAL transaction, there might be some loose
	// objects left. Eventually, they will go away after a full repack. Thus, this RPC is a no-op in the context of
	// transaction. We still need to perform repository validation. It doesn't make sense if it returns successful
	// status code for a non-existent repository.
	if s.walPartitionManager != nil {
		return &gitalypb.PruneUnreachableObjectsResponse{}, nil
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
	if err := housekeeping.RepackObjects(ctx, repo, housekeepingcfg.RepackObjectsConfig{
		Strategy:            housekeepingcfg.RepackObjectsStrategyFullWithCruft,
		WriteMultiPackIndex: true,
		WriteBitmap:         len(repoInfo.Alternates.ObjectDirectories) == 0,
		CruftExpireBefore:   expireBefore,
	}); err != nil {
		return nil, structerr.NewInternal("repacking objects: %w", err)
	}

	// Rewrite the commit-graph so that it doesn't contain references to pruned commits
	// anymore.
	if err := housekeeping.WriteCommitGraph(ctx, repo, housekeepingcfg.WriteCommitGraphConfig{
		ReplaceChain: true,
	}); err != nil {
		return nil, structerr.NewInternal("rewriting commit-graph: %w", err)
	}

	stats.LogRepositoryInfo(ctx, s.logger, repo)

	return &gitalypb.PruneUnreachableObjectsResponse{}, nil
}
