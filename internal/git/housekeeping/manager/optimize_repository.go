package manager

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tracing"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

// OptimizeRepositoryConfig is the configuration used by OptimizeRepository that is computed by
// applying all the OptimizeRepositoryOption modifiers.
type OptimizeRepositoryConfig struct {
	StrategyConstructor OptimizationStrategyConstructor
}

// OptimizeRepositoryOption is an option that can be passed to OptimizeRepository.
type OptimizeRepositoryOption func(cfg *OptimizeRepositoryConfig)

// OptimizationStrategyConstructor is a constructor for an OptimizationStrategy that is being
// informed by the passed-in RepositoryInfo.
type OptimizationStrategyConstructor func(stats.RepositoryInfo) housekeeping.OptimizationStrategy

// WithOptimizationStrategyConstructor changes the constructor for the optimization strategy.that is
// used to determine which parts of the repository will be optimized. By default the
// HeuristicalOptimizationStrategy is used.
func WithOptimizationStrategyConstructor(strategyConstructor OptimizationStrategyConstructor) OptimizeRepositoryOption {
	return func(cfg *OptimizeRepositoryConfig) {
		cfg.StrategyConstructor = strategyConstructor
	}
}

// OptimizeRepository performs optimizations on the repository. Whether optimizations are performed
// or not depends on a set of heuristics.
func (m *RepositoryManager) OptimizeRepository(
	ctx context.Context,
	repo *localrepo.Repo,
	opts ...OptimizeRepositoryOption,
) error {
	span, ctx := tracing.StartSpanIfHasParent(ctx, "housekeeping.OptimizeRepository", nil)
	defer span.Finish()

	ok, cleanup := m.repositoryStates.tryRunningHousekeeping(repo)
	// If we didn't succeed to set the state to "running" because of a concurrent housekeeping run
	// we exit early.
	if !ok {
		return nil
	}
	defer cleanup()

	if m.optimizeFunc != nil {
		strategy, err := m.validate(ctx, repo, opts)
		if err != nil {
			return err
		}
		return m.optimizeFunc(ctx, repo, strategy)
	}

	if m.walPartitionManager != nil {
		return m.optimizeRepositoryWithTransaction(ctx, repo, opts)
	}
	return m.optimizeRepository(ctx, repo, opts)
}

func (m *RepositoryManager) validate(
	ctx context.Context,
	repo *localrepo.Repo,
	opts []OptimizeRepositoryOption,
) (housekeeping.OptimizationStrategy, error) {
	gitVersion, err := repo.GitVersion(ctx)
	if err != nil {
		return nil, err
	}

	var cfg OptimizeRepositoryConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	repositoryInfo, err := stats.RepositoryInfoForRepository(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("deriving repository info: %w", err)
	}

	repositoryInfo.Log(ctx, m.logger)
	m.metrics.ReportRepositoryInfo(repositoryInfo)

	var strategy housekeeping.OptimizationStrategy
	if cfg.StrategyConstructor == nil {
		strategy = housekeeping.NewHeuristicalOptimizationStrategy(gitVersion, repositoryInfo)
	} else {
		strategy = cfg.StrategyConstructor(repositoryInfo)
	}

	return strategy, nil
}

func (m *RepositoryManager) optimizeRepository(
	ctx context.Context,
	repo *localrepo.Repo,
	opts []OptimizeRepositoryOption,
) error {
	strategy, err := m.validate(ctx, repo, opts)
	if err != nil {
		return err
	}

	finishTotalTimer := m.metrics.ReportTaskLatency("total", "apply")
	totalStatus := "failure"

	optimizations := make(map[string]string)
	defer func() {
		finishTotalTimer()
		m.logger.WithField("optimizations", optimizations).Info("optimized repository")

		for task, status := range optimizations {
			m.metrics.TasksTotal.WithLabelValues(task, status).Inc()
		}

		m.metrics.TasksTotal.WithLabelValues("total", totalStatus).Add(1)
	}()

	finishTimer := m.metrics.ReportTaskLatency("clean-stale-data", "apply")
	if err := m.CleanStaleData(ctx, repo, housekeeping.DefaultStaleDataCleanup()); err != nil {
		return fmt.Errorf("could not execute housekeeping: %w", err)
	}
	finishTimer()

	finishTimer = m.metrics.ReportTaskLatency("clean-worktrees", "apply")
	if err := housekeeping.CleanupWorktrees(ctx, repo); err != nil {
		return fmt.Errorf("could not clean up worktrees: %w", err)
	}
	finishTimer()

	finishTimer = m.metrics.ReportTaskLatency("repack", "apply")
	didRepack, repackCfg, err := repackIfNeeded(ctx, repo, strategy)
	if err != nil {
		optimizations["packed_objects_"+string(repackCfg.Strategy)] = "failure"
		if repackCfg.WriteBitmap {
			optimizations["written_bitmap"] = "failure"
		}
		if repackCfg.WriteMultiPackIndex {
			optimizations["written_multi_pack_index"] = "failure"
		}

		return fmt.Errorf("could not repack: %w", err)
	}
	if didRepack {
		optimizations["packed_objects_"+string(repackCfg.Strategy)] = "success"

		if repackCfg.WriteBitmap {
			optimizations["written_bitmap"] = "success"
		}
		if repackCfg.WriteMultiPackIndex {
			optimizations["written_multi_pack_index"] = "success"
		}
	}
	finishTimer()

	finishTimer = m.metrics.ReportTaskLatency("prune", "apply")
	didPrune, err := pruneIfNeeded(ctx, repo, strategy)
	if err != nil {
		optimizations["pruned_objects"] = "failure"
		return fmt.Errorf("could not prune: %w", err)
	} else if didPrune {
		optimizations["pruned_objects"] = "success"
	}
	finishTimer()

	finishTimer = m.metrics.ReportTaskLatency("pack-refs", "apply")
	didPackRefs, err := m.packRefsIfNeeded(ctx, repo, strategy)
	if err != nil {
		optimizations["packed_refs"] = "failure"
		return fmt.Errorf("could not pack refs: %w", err)
	} else if didPackRefs {
		optimizations["packed_refs"] = "success"
	}
	finishTimer()

	finishTimer = m.metrics.ReportTaskLatency("commit-graph", "apply")
	if didWriteCommitGraph, writeCommitGraphCfg, err := writeCommitGraphIfNeeded(ctx, repo, strategy); err != nil {
		optimizations["written_commit_graph_full"] = "failure"
		optimizations["written_commit_graph_incremental"] = "failure"
		return fmt.Errorf("could not write commit-graph: %w", err)
	} else if didWriteCommitGraph {
		if writeCommitGraphCfg.ReplaceChain {
			optimizations["written_commit_graph_full"] = "success"
		} else {
			optimizations["written_commit_graph_incremental"] = "success"
		}
	}
	finishTimer()

	totalStatus = "success"

	return nil
}

// optimizeRepositoryWithTransaction performs optimizations in the context of WAL transaction.
func (m *RepositoryManager) optimizeRepositoryWithTransaction(
	ctx context.Context,
	repo *localrepo.Repo,
	opts []OptimizeRepositoryOption,
) (returnedError error) {
	transaction, err := m.walPartitionManager.Begin(ctx, repo.GetStorageName(), repo.GetRelativePath(), storagemgr.TransactionOptions{})
	if err != nil {
		return fmt.Errorf("initializing WAL transaction: %w", err)
	}
	defer func() {
		if returnedError != nil {
			// We prioritize actual housekeeping error and log rollback error.
			if err := transaction.Rollback(); err != nil {
				m.logger.WithError(err).Error("could not rollback housekeeping transaction")
			}
		}
	}()

	snapshotRepo := localrepo.NewFrom(repo, transaction.RewriteRepository(&gitalypb.Repository{
		StorageName:                   repo.GetStorageName(),
		GitAlternateObjectDirectories: repo.GetGitAlternateObjectDirectories(),
		GitObjectDirectory:            repo.GetGitObjectDirectory(),
		RelativePath:                  repo.GetRelativePath(),
	}))

	strategy, err := m.validate(ctx, snapshotRepo, opts)
	if err != nil {
		return err
	}

	repackNeeded, repackCfg := strategy.ShouldRepackObjects(ctx)
	if repackNeeded {
		transaction.Repack(repackCfg)
	}

	packRefsNeeded := strategy.ShouldRepackReferences(ctx)
	if packRefsNeeded {
		transaction.PackRefs()
	}

	writeCommitGraphNeeded, writeCommitGraphCfg, err := strategy.ShouldWriteCommitGraph(ctx)
	if err != nil {
		return fmt.Errorf("checking commit graph writing eligibility: %w", err)
	}
	if writeCommitGraphNeeded {
		transaction.WriteCommitGraphs(writeCommitGraphCfg)
	}

	logResult := func(status string) {
		optimizations := make(map[string]string)

		if repackNeeded {
			optimizations["packed_objects_"+string(repackCfg.Strategy)] = status
			if repackCfg.WriteBitmap {
				optimizations["written_bitmap"] = status
			}
			if repackCfg.WriteMultiPackIndex {
				optimizations["written_multi_pack_index"] = status
			}
		}

		if packRefsNeeded {
			optimizations["packed_refs"] = status
		}

		if writeCommitGraphNeeded {
			if writeCommitGraphCfg.ReplaceChain {
				optimizations["written_commit_graph_full"] = status
			} else {
				optimizations["written_commit_graph_incremental"] = status
			}
		}

		m.logger.WithField("optimizations", optimizations).Info("optimized repository with WAL")
		for task, status := range optimizations {
			m.metrics.TasksTotal.WithLabelValues(task, status).Inc()
		}
		m.metrics.TasksTotal.WithLabelValues("total", status).Add(1)
	}

	if err := transaction.Commit(ctx); err != nil {
		logResult("failure")
		return fmt.Errorf("committing housekeeping transaction: %w", err)
	}

	logResult("success")
	return nil
}

// repackIfNeeded repacks the repository according to the strategy.
func repackIfNeeded(ctx context.Context, repo *localrepo.Repo, strategy housekeeping.OptimizationStrategy) (bool, config.RepackObjectsConfig, error) {
	repackNeeded, cfg := strategy.ShouldRepackObjects(ctx)
	if !repackNeeded {
		return false, config.RepackObjectsConfig{}, nil
	}

	if err := housekeeping.RepackObjects(ctx, repo, cfg); err != nil {
		return false, cfg, err
	}

	return true, cfg, nil
}

// writeCommitGraphIfNeeded writes the commit-graph if required.
func writeCommitGraphIfNeeded(ctx context.Context, repo *localrepo.Repo, strategy housekeeping.OptimizationStrategy) (bool, config.WriteCommitGraphConfig, error) {
	needed, cfg, err := strategy.ShouldWriteCommitGraph(ctx)
	if !needed || err != nil {
		return false, config.WriteCommitGraphConfig{}, err
	}

	if err := housekeeping.WriteCommitGraph(ctx, repo, cfg); err != nil {
		return true, cfg, fmt.Errorf("writing commit-graph: %w", err)
	}

	return true, cfg, nil
}

// pruneIfNeeded removes objects from the repository which are either unreachable or which are
// already part of a packfile. We use a grace period of two weeks.
func pruneIfNeeded(ctx context.Context, repo *localrepo.Repo, strategy housekeeping.OptimizationStrategy) (bool, error) {
	needed, cfg := strategy.ShouldPruneObjects(ctx)
	if !needed {
		return false, nil
	}

	if err := housekeeping.PruneObjects(ctx, repo, cfg); err != nil {
		return true, fmt.Errorf("pruning objects: %w", err)
	}

	return true, nil
}

func (m *RepositoryManager) packRefsIfNeeded(ctx context.Context, repo *localrepo.Repo, strategy housekeeping.OptimizationStrategy) (bool, error) {
	if !strategy.ShouldRepackReferences(ctx) {
		return false, nil
	}

	// If there are any inhibitors, we don't run git-pack-refs(1).
	ok, cleanup := m.repositoryStates.tryRunningPackRefs(repo)
	if !ok {
		return false, nil
	}
	defer cleanup()

	var stderr bytes.Buffer
	if err := repo.ExecAndWait(ctx, git.Command{
		Name: "pack-refs",
		Flags: []git.Option{
			git.Flag{Name: "--all"},
		},
	}, git.WithStderr(&stderr)); err != nil {
		return false, fmt.Errorf("packing refs: %w, stderr: %q", err, stderr.String())
	}

	return true, nil
}

// CleanStaleData removes any stale data in the repository as per the provided configuration.
func (m *RepositoryManager) CleanStaleData(ctx context.Context, repo *localrepo.Repo, cfg housekeeping.CleanStaleDataConfig) error {
	span, ctx := tracing.StartSpanIfHasParent(ctx, "housekeeping.CleanStaleData", nil)
	defer span.Finish()

	repoPath, err := repo.Path()
	if err != nil {
		m.logger.WithError(err).WarnContext(ctx, "housekeeping failed to get repo path")
		if structerr.GRPCCode(err) == codes.NotFound {
			return nil
		}
		return fmt.Errorf("housekeeping failed to get repo path: %w", err)
	}

	staleDataByType := map[string]int{}
	defer func() {
		if len(staleDataByType) == 0 {
			return
		}

		logEntry := m.logger
		for staleDataType, count := range staleDataByType {
			logEntry = logEntry.WithField(fmt.Sprintf("stale_data.%s", staleDataType), count)
			m.metrics.PrunedFilesTotal.WithLabelValues(staleDataType).Add(float64(count))
		}
		logEntry.InfoContext(ctx, "removed files")
	}()

	var filesToPrune []string
	for staleFileType, staleFileFinder := range cfg.StaleFileFinders {
		staleFiles, err := staleFileFinder(ctx, repoPath)
		if err != nil {
			return fmt.Errorf("housekeeping failed to find %s: %w", staleFileType, err)
		}

		filesToPrune = append(filesToPrune, staleFiles...)
		staleDataByType[staleFileType] = len(staleFiles)
	}

	for _, path := range filesToPrune {
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			staleDataByType["failures"]++
			m.logger.WithError(err).WithField("path", path).WarnContext(ctx, "unable to remove stale file")
		}
	}

	for repoCleanupName, repoCleanupFn := range cfg.RepoCleanups {
		cleanupCount, err := repoCleanupFn(ctx, repo)
		staleDataByType[repoCleanupName] = cleanupCount
		if err != nil {
			return fmt.Errorf("housekeeping could not perform cleanup %s: %w", repoCleanupName, err)
		}
	}

	for repoCleanupName, repoCleanupFn := range cfg.RepoCleanupWithTxManagers {
		cleanupCount, err := repoCleanupFn(ctx, repo, m.txManager)
		staleDataByType[repoCleanupName] = cleanupCount
		if err != nil {
			return fmt.Errorf("housekeeping could not perform cleanup (with TxManager) %s: %w", repoCleanupName, err)
		}
	}

	return nil
}
