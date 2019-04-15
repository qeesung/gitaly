package praefect

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
)

// Replicator performs the actual replication logic between two nodes
type Replicator interface {
	Replicate(ctx context.Context, source Repository, target Node) error
}

type defaultReplicator struct {
	log *logrus.Logger
}

func (dr defaultReplicator) Replicate(ctx context.Context, source Repository, target Node) error {
	dr.log.Infof("replicating from %v to target %q", source, target.Storage)
	return nil
}

// ReplMgr is a replication manager for handling replication jobs
type ReplMgr struct {
	log         *logrus.Logger
	jobsStore   ReplJobsDatastore
	coordinator *Coordinator
	storage     string     // which replica is this replicator responsible for?
	replicator  Replicator // does the actual replication logic

	// whitelist contains the project names of the repos we wish to replicate
	whitelist map[string]struct{}
}

// ReplMgrOpt allows a replicator to be configured with additional options
type ReplMgrOpt func(*ReplMgr)

// NewReplMgr initializes a replication manager with the provided dependencies
// and options
func NewReplMgr(storage string, log *logrus.Logger, ds ReplJobsDatastore, c *Coordinator, opts ...ReplMgrOpt) ReplMgr {
	r := ReplMgr{
		log:         log,
		jobsStore:   ds,
		whitelist:   map[string]struct{}{},
		replicator:  defaultReplicator{log},
		storage:     storage,
		coordinator: c,
	}

	for _, opt := range opts {
		opt(&r)
	}

	return r
}

// WithWhitelist will configure a whitelist for repos to allow replication
func WithWhitelist(whitelistedRepos []string) ReplMgrOpt {
	return func(r *ReplMgr) {
		for _, repo := range whitelistedRepos {
			r.whitelist[repo] = struct{}{}
		}
	}
}

// WithReplicator overrides the default replicator
func WithReplicator(r Replicator) ReplMgrOpt {
	return func(rm *ReplMgr) {
		rm.replicator = r
	}
}

// ScheduleReplication will store a replication job in the datastore for later
// execution. It filters out projects that are not whitelisted.
// TODO: add a parameter to delay replication
func (r ReplMgr) ScheduleReplication(ctx context.Context, repo Repository) error {
	_, ok := r.whitelist[repo.RelativePath]
	if !ok {
		r.log.WithField(logKeyProjectPath, repo.RelativePath).
			Infof("project %q is not whitelisted for replication", repo.RelativePath)
		return nil
	}

	id, err := r.jobsStore.CreateSecondaryReplJobs(repo)
	if err != nil {
		return err
	}

	r.log.Infof(
		"replication manager for storage %q created replication job with ID %d",
		r.storage,
		id,
	)

	return nil
}

// ProcessBacklog will process queued jobs. It will block while processing jobs.
func (r ReplMgr) ProcessBacklog(ctx context.Context) error {
	since := time.Time{}
	for {
		r.log.Debugf("fetching replication jobs since %s", since)
		jobs, err := r.jobsStore.GetIncompleteJobs(r.storage, 10)
		if err != nil {
			return err
		}

		if len(jobs) == 0 {
			select {

			// TODO: exponential backoff when no queries are returned
			case <-time.After(10 * time.Millisecond):
				continue

			case <-ctx.Done():
				return ctx.Err()

			}
		}

		r.log.Debugf("fetched replication jobs: %#v", jobs)

		for _, job := range jobs {
			r.log.Infof("processing replication job %#v", job)
			node, err := r.coordinator.GetStorageNode(job.Target)
			if err != nil {
				return err
			}

			err = r.replicator.Replicate(ctx, job.Source, node)
			if err != nil {
				return err
			}
		}
	}
}
