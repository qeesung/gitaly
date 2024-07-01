package maintenance

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// WorkerFunc is a function that does a unit of work meant to run in the background
type WorkerFunc func(context.Context, log.Logger) error

// StartWorkers will start any background workers and returns a function that
// can be used to shut down the background workers.
func StartWorkers(
	ctx context.Context,
	l log.Logger,
	workers ...WorkerFunc,
) (func(), error) {
	errQ := make(chan error)

	ctx, cancel := context.WithCancel(ctx)

	for _, worker := range workers {
		worker := worker
		go func() {
			errQ <- worker(ctx, l)
		}()
	}

	shutdown := func() {
		cancel()

		// give the worker 5 seconds to shutdown gracefully
		timeout := 5 * time.Second

		var err error
		select {
		case err = <-errQ:
			break
		case <-time.After(timeout):
			err = fmt.Errorf("timed out after %s", timeout)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			l.WithError(err).Error("maintenance worker shutdown")
		}
	}

	return shutdown, nil
}

func shuffledStoragesCopy(randSrc *rand.Rand, storages []config.Storage) []config.Storage {
	shuffled := make([]config.Storage, len(storages))
	copy(shuffled, storages)
	randSrc.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	return shuffled
}

// Optimizer knows how to optimize a repository
type Optimizer interface {
	OptimizeRepository(context.Context, log.Logger, storage.Repository) error
}

// OptimizerFunc is an adapter to allow the use of an ordinary function as an Optimizer
type OptimizerFunc func(context.Context, log.Logger, storage.Repository) error

// OptimizeRepository calls o(ctx, repo)
func (o OptimizerFunc) OptimizeRepository(ctx context.Context, logger log.Logger, repo storage.Repository) error {
	return o(ctx, logger, repo)
}

// DailyOptimizationWorker creates a worker that runs repository maintenance daily
func DailyOptimizationWorker(cfg config.Cfg, optimizer Optimizer) WorkerFunc {
	return func(ctx context.Context, l log.Logger) error {
		return NewDailyWorker().StartDaily(
			ctx,
			l,
			cfg.DailyMaintenance,
			OptimizeReposRandomly(
				cfg,
				optimizer,
				helper.NewTimerTicker(1*time.Second),
				rand.New(rand.NewSource(time.Now().UnixNano())),
			),
		)
	}
}

func optimizeRepo(
	ctx context.Context,
	l log.Logger,
	o Optimizer,
	repo *gitalypb.Repository,
) error {
	start := time.Now()
	logEntry := l.WithFields(map[string]interface{}{
		"relative_path": repo.GetRelativePath(),
		"storage":       repo.GetStorageName(),
		"source":        "maintenance.daily",
		"start_time":    start.UTC(),
	})

	err := o.OptimizeRepository(ctx, l, repo)
	logEntry = logEntry.WithField("time_ms", time.Since(start).Milliseconds())

	if err != nil {
		logEntry.WithError(err).Error("maintenance: repo optimization failure")
		return err
	}

	logEntry.Info("maintenance: repo optimization succeeded")
	return nil
}

func walkReposShuffled(
	ctx context.Context,
	locator storage.Locator,
	walker *randomWalker,
	l log.Logger,
	s config.Storage,
	o Optimizer,
	ticker helper.Ticker,
) error {
	for {
		fi, path, err := walker.next()
		switch {
		case errors.Is(err, errIterOver):
			return nil
		case os.IsNotExist(err):
			continue // race condition: someone deleted it
		case err != nil:
			return err
		}

		if !fi.IsDir() {
			continue
		}

		relativeRepoPath, err := filepath.Rel(s.Path, path)
		if err != nil {
			return err
		}

		repo := &gitalypb.Repository{
			StorageName:  s.Name,
			RelativePath: relativeRepoPath,
		}

		if locator.ValidateRepository(ctx, repo) != nil {
			continue
		}
		walker.skipDir()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C():
		}

		// Reset the ticker before doing the optimization such that we essentially limit
		// ourselves to doing optimizations once per tick, not once per tick plus the time
		// it takes to do the optimization. It's best effort given that traversing the
		// directory hierarchy takes some time, too, but it should be good enough for now.
		ticker.Reset()

		if err := optimizeRepo(ctx, l, o, repo); err != nil {
			return err
		}
	}
}

// OptimizeReposRandomly returns a function to walk through each storage and attempts to optimize
// any repos encountered. The ticker is used to rate-limit optimizations.
//
// Only storage paths that map to an enabled storage name will be walked. Any storage paths shared
// by multiple storages will only be walked once.
//
// Any errors during the optimization will be logged. Any other errors will be returned and cause
// the walk to end prematurely.
func OptimizeReposRandomly(cfg config.Cfg, optimizer Optimizer, ticker helper.Ticker, rand *rand.Rand) StoragesJob {
	return func(ctx context.Context, l log.Logger, enabledStorageNames []string) error {
		enabledNames := map[string]struct{}{}
		for _, sName := range enabledStorageNames {
			enabledNames[sName] = struct{}{}
		}

		locator := config.NewLocator(cfg)

		visitedPaths := map[string]bool{}

		ticker.Reset()
		defer ticker.Stop()

		for _, storage := range shuffledStoragesCopy(rand, cfg.Storages) {
			if _, ok := enabledNames[storage.Name]; !ok {
				continue // storage not enabled
			}
			if visitedPaths[storage.Path] {
				continue // already visited
			}
			visitedPaths[storage.Path] = true

			l.WithField("storage_path", storage.Path).
				Info("maintenance: optimizing repos in storage")

			walker := newRandomWalker(storage.Path, rand)

			if err := walkReposShuffled(ctx, locator, walker, l, storage, optimizer, ticker); err != nil {
				l.WithError(err).
					WithField("storage_path", storage.Path).
					Error("maintenance: unable to completely walk storage")
			}
		}
		return nil
	}
}
