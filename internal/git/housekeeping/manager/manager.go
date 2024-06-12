package manager

import (
	"context"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	gitalycfgprom "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

// Manager is a housekeeping manager. It is supposed to handle housekeeping tasks for repositories
// such as the cleanup of unneeded files and optimizations for the repository's data structures.
type Manager interface {
	// CleanStaleData removes any stale data in the repository as per the provided configuration.
	CleanStaleData(context.Context, *localrepo.Repo, housekeeping.CleanStaleDataConfig) error
	// OptimizeRepository optimizes the repository's data structures such that it can be more
	// efficiently served.
	OptimizeRepository(context.Context, *localrepo.Repo, ...OptimizeRepositoryOption) error
	// AddPackRefsInhibitor allows clients to block housekeeping from running git-pack-refs(1).
	AddPackRefsInhibitor(ctx context.Context, repo storage.Repository) (bool, func(), error)
}

// repositoryState holds the housekeeping state for individual repositories. This structure can be
// used to sync between different housekeeping goroutines. It is safer to access this via the methods
// of repositoryStates structure.
type repositoryState struct {
	sync.Mutex

	// packRefsDone is a channel used to denote when the ongoing (if any) call to packRefsIfNeeded
	// is completed.
	packRefsDone chan struct{}
	// packRefsInhibitors keeps a count of the number of inhibitors on running packRefsIfNeeded.
	packRefsInhibitors int32
	// isRunning is used to indicate if housekeeping is running.
	isRunning bool
}

// refCountedState keeps count of number of goroutines using a particular repository state, this is used
// to ensure that we only delete a particular state of a repository when there are no goroutines which
// are accessing it.
type refCountedState struct {
	// state is a pointer to a single repositories state.
	state *repositoryState
	// rc keeps count of the number of goroutines using the state.
	rc uint32
}

// repositoryStates holds per-repository information to sync between different goroutines.
// Access to the internal fields should be done via the methods provided by the struct.
type repositoryStates struct {
	sync.Mutex
	// values is map which denotes per-repository housekeeping state.
	values map[string]*refCountedState
}

func repositoryStatesKey(repo storage.Repository) string {
	return repo.GetStorageName() + ":" + repo.GetRelativePath()
}

// getState provides the state and cleanup function for a given repository path.
// The cleanup function deletes the state if the caller is the last goroutine referencing
// the state.
func (s *repositoryStates) getState(repo storage.Repository) (*repositoryState, func()) {
	key := repositoryStatesKey(repo)

	s.Lock()
	defer s.Unlock()

	value, ok := s.values[key]
	if !ok {
		s.values[key] = &refCountedState{
			rc:    0,
			state: &repositoryState{},
		}
		value = s.values[key]
	}

	value.rc++

	return value.state, func() {
		s.Lock()
		defer s.Unlock()

		value.rc--
		if value.rc == 0 {
			delete(s.values, key)
		}
	}
}

// tryRunningHousekeeping denotes if housekeeping can be run on a given repository.
// If successful, it also provides a cleanup function which resets the state so other
// goroutines can run housekeeping on the repository.
func (s *repositoryStates) tryRunningHousekeeping(repo storage.Repository) (successful bool, _ func()) {
	state, cleanup := s.getState(repo)
	defer func() {
		if !successful {
			cleanup()
		}
	}()

	state.Lock()
	defer state.Unlock()

	if state.isRunning {
		return false, nil
	}
	state.isRunning = true

	return true, func() {
		defer cleanup()

		state.Lock()
		defer state.Unlock()

		state.isRunning = false
	}
}

// addPackRefsInhibitor is used to add an inhibitor over running `git-pack-refs(1)`.
// If `git-pack-refs(1)` is currently running, the caller waits till it finishes. The caller
// can also exit early by using a context cancellation instead.
//
// The function returns a cleanup function which decreases the inhibitor count and must
// be called by the client when it no longer blocks `git-pack-refs(1)`.
func (s *repositoryStates) addPackRefsInhibitor(ctx context.Context, repo storage.Repository) (successful bool, _ func(), err error) {
	state, cleanup := s.getState(repo)
	defer func() {
		if !successful {
			cleanup()
		}
	}()

	// We don't defer unlock here to ensure that a single inhibitor doesn't lock the others.
	// We want inhibitors to be able to use context cancellation to exit early without being
	// locked by other inhibitors.
	state.Lock()

	for {
		packRefsDone := state.packRefsDone
		if packRefsDone == nil {
			break
		}

		state.Unlock()

		select {
		case <-ctx.Done():
			return false, nil, ctx.Err()
		case <-packRefsDone:
			// We don't use state.packRefsDone, cause there is possibility that it is set
			// to `nil` by the cleanup function after running `git-pack-refs(1)`.
			//
			// We obtain a lock and continue the loop here to avoid a race wherein another
			// goroutine has invoked git-pack-refs(1). By continuing the loop and checking
			// the value of packRefsDone, we can avoid that scenario.
			state.Lock()
			continue
		}
	}

	defer state.Unlock()

	state.packRefsInhibitors++

	return true, func() {
		defer cleanup()

		state.Lock()
		defer state.Unlock()

		state.packRefsInhibitors--
	}, nil
}

// tryRunningPackRefs checks if we can run `git-pack-refs(1)` for a given repository. If there
// is at least one inhibitors then we return false. If there are no inhibitors, we setup the `packRefsDone`
// channel to denote when `git-pack-refs(1)` finishes, this is handled when the caller calls the
// cleanup function returned by this function.
func (s *repositoryStates) tryRunningPackRefs(repo storage.Repository) (successful bool, _ func()) {
	state, cleanup := s.getState(repo)
	defer func() {
		if !successful {
			cleanup()
		}
	}()

	state.Lock()
	defer state.Unlock()

	if state.packRefsInhibitors > 0 || state.packRefsDone != nil {
		return false, nil
	}

	state.packRefsDone = make(chan struct{})

	return true, func() {
		defer cleanup()

		state.Lock()
		defer state.Unlock()

		close(state.packRefsDone)
		state.packRefsDone = nil
	}
}

// RepositoryManager is an implementation of the Manager interface.
type RepositoryManager struct {
	logger log.Logger
	// txManager is Praefect's transaction manager using voting mechanism. It will be deprecated soon.
	txManager transaction.Manager
	// walPartitionManager is the WAL partition manager. It is used to control housekeeping transactions.
	walPartitionManager *storagemgr.PartitionManager

	metrics          *housekeeping.Metrics
	optimizeFunc     func(context.Context, *localrepo.Repo, housekeeping.OptimizationStrategy) error
	repositoryStates repositoryStates
}

// New creates a new RepositoryManager.
func New(promCfg gitalycfgprom.Config, logger log.Logger, txManager transaction.Manager, partitionManager *storagemgr.PartitionManager) *RepositoryManager {
	return &RepositoryManager{
		logger:              logger.WithField("system", "housekeeping"),
		txManager:           txManager,
		walPartitionManager: partitionManager,
		metrics:             housekeeping.NewMetrics(promCfg),
		repositoryStates: repositoryStates{
			values: make(map[string]*refCountedState),
		},
	}
}

// Describe is used to describe Prometheus metrics.
func (m *RepositoryManager) Describe(descs chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(m, descs)
}

// Collect is used to collect Prometheus metrics.
func (m *RepositoryManager) Collect(metrics chan<- prometheus.Metric) {
	m.metrics.Collect(metrics)
}

// AddPackRefsInhibitor exposes the internal function addPackRefsInhibitor on the
// RepositoryManager level. This can then be used by other clients to block housekeeping
// from running git-pack-refs(1).
func (m *RepositoryManager) AddPackRefsInhibitor(ctx context.Context, repo storage.Repository) (successful bool, _ func(), err error) {
	return m.repositoryStates.addPackRefsInhibitor(ctx, repo)
}
