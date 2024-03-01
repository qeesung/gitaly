package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// repositoryKey uniquely identifies a repository, and is used here to key the
// map of processed repos.
type repositoryKey string

// Strategy used to create/restore backups
type Strategy interface {
	Create(context.Context, *CreateRequest) error
	Restore(context.Context, *RestoreRequest) error
}

// CreateRequest is the request to create a backup
type CreateRequest struct {
	// Server contains gitaly server connection information required to call
	// RPCs in the non-local backup.Manager configuration.
	Server storage.ServerInfo
	// Repository is the repository to be backed up.
	Repository *gitalypb.Repository
	// VanityRepository is used to determine the backup path.
	VanityRepository *gitalypb.Repository
	// Incremental when true will create an increment on the specified full backup.
	Incremental bool
	// BackupID is used to determine a unique path for the backup when a full
	// backup is created.
	BackupID string
}

// RestoreRequest is the request to restore from a backup
type RestoreRequest struct {
	// Server contains gitaly server connection information required to call
	// RPCs in the non-local backup.Manager configuration.
	Server storage.ServerInfo
	// Repository is the repository to be restored.
	Repository *gitalypb.Repository
	// VanityRepository is used to determine the backup path.
	VanityRepository *gitalypb.Repository
	// AlwaysCreate forces the repository to be created even if no bundle for
	// it exists. See https://gitlab.com/gitlab-org/gitlab/-/issues/357044
	AlwaysCreate bool
	// BackupID is the ID of the full backup to restore. If not specified, the
	// latest backup is restored..
	BackupID string
}

// Command handles a specific backup operation
type Command interface {
	Repository() *gitalypb.Repository
	Name() string
	Execute(context.Context) error
}

// CreateCommand creates a backup for a repository
type CreateCommand struct {
	strategy Strategy
	request  CreateRequest
}

// NewCreateCommand builds a CreateCommand
func NewCreateCommand(strategy Strategy, request CreateRequest) *CreateCommand {
	return &CreateCommand{
		strategy: strategy,
		request:  request,
	}
}

// Repository is the repository that will be acted on
func (cmd CreateCommand) Repository() *gitalypb.Repository {
	return cmd.request.Repository
}

// Name is the name of the command
func (cmd CreateCommand) Name() string {
	return "create"
}

// Execute performs the backup
func (cmd CreateCommand) Execute(ctx context.Context) error {
	return cmd.strategy.Create(ctx, &cmd.request)
}

// RestoreCommand restores a backup for a repository
type RestoreCommand struct {
	strategy Strategy
	request  RestoreRequest
}

// NewRestoreCommand builds a RestoreCommand
func NewRestoreCommand(strategy Strategy, request RestoreRequest) *RestoreCommand {
	return &RestoreCommand{
		strategy: strategy,
		request:  request,
	}
}

// Repository is the repository that will be acted on
func (cmd RestoreCommand) Repository() *gitalypb.Repository {
	return cmd.request.Repository
}

// Name is the name of the command
func (cmd RestoreCommand) Name() string {
	return "restore"
}

// Execute performs the restore
func (cmd RestoreCommand) Execute(ctx context.Context) error {
	return cmd.strategy.Restore(ctx, &cmd.request)
}

// commandErrors represents a summary of errors by repository
//
//nolint:errname
type commandErrors struct {
	errs []error
	mu   sync.Mutex
}

// AddError adds an error associated with a repository to the summary.
func (c *commandErrors) AddError(repo *gitalypb.Repository, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if repo.GetGlProjectPath() != "" {
		err = fmt.Errorf("%s (%s): %w", repo.GetRelativePath(), repo.GetGlProjectPath(), err)
	} else {
		err = fmt.Errorf("%s: %w", repo.GetRelativePath(), err)
	}
	c.errs = append(c.errs, err)
}

func (c *commandErrors) Error() string {
	var builder strings.Builder
	_, _ = fmt.Fprintf(&builder, "%d failures encountered:\n", len(c.errs))
	for _, err := range c.errs {
		_, _ = fmt.Fprintf(&builder, " - %s\n", err.Error())
	}
	return builder.String()
}

type contextCommand struct {
	Command Command
	Context context.Context
}

// Pipeline is a pipeline for running backup and restore jobs.
type Pipeline struct {
	log log.Logger

	parallel        int
	parallelStorage int

	// totalWorkers allows the total number of parallel jobs to be
	// limited. This allows us to create the required workers for
	// each storage, while still limiting the absolute parallelism.
	totalWorkers chan struct{}

	workerWg           sync.WaitGroup
	workersByStorage   map[string]chan *contextCommand
	workersByStorageMu sync.Mutex

	// done signals that no more commands will be provided to the Pipeline via
	// Handle(), and the pipeline should wait for workers to complete and exit.
	done chan struct{}

	pipelineError error
	cmdErrors     *commandErrors

	processedRepos   map[string]map[repositoryKey]struct{}
	processedReposMu sync.Mutex
}

// NewPipeline creates a pipeline that executes backup and restore jobs.
// The pipeline executes sequentially by default, but can be made concurrent
// by calling WithConcurrency() after initialisation.
func NewPipeline(log log.Logger, opts ...PipelineOption) (*Pipeline, error) {
	p := &Pipeline{
		log: log,
		// Default to no concurrency.
		parallel:         1,
		parallelStorage:  0,
		done:             make(chan struct{}),
		workersByStorage: make(map[string]chan *contextCommand),
		cmdErrors:        &commandErrors{},
		processedRepos:   make(map[string]map[repositoryKey]struct{}),
	}

	for _, opt := range opts {
		if err := opt(p); err != nil {
			return nil, err
		}
	}

	return p, nil
}

// PipelineOption represents an optional configuration parameter for the Pipeline.
type PipelineOption func(*Pipeline) error

// WithConcurrency configures the pipeline to run backup and restore jobs concurrently.
// total defines the absolute maximum number of jobs that the pipeline should execute
// concurrently. perStorage defines the number of jobs per Gitaly storage that the
// pipeline should attempt to execute concurrently.
//
// For example, in a Gitaly deployment with 2 storages, WithConcurrency(3, 2) means
// that at most 3 jobs will execute concurrently, despite 2 concurrent jobs being allowed
// per storage (2*2=4).
func WithConcurrency(total, perStorage int) PipelineOption {
	return func(p *Pipeline) error {
		if total == 0 && perStorage == 0 {
			return errors.New("total and perStorage cannot both be 0")
		}

		p.parallel = total
		p.parallelStorage = perStorage

		if total > 0 && perStorage > 0 {
			// When both values are provided, we ensure that total limits
			// the global concurrency.
			p.totalWorkers = make(chan struct{}, total)
		}

		return nil
	}
}

// Handle queues a request to create a backup. Commands either processed sequentially
// or concurrently, if WithConcurrency() was called.
func (p *Pipeline) Handle(ctx context.Context, cmd Command) {
	ch := p.getWorker(cmd.Repository().StorageName)

	select {
	case <-ctx.Done():
		p.setErr(ctx.Err())
	case ch <- &contextCommand{
		Command: cmd,
		Context: ctx,
	}:
	}
}

// Done waits for any in progress jobs to complete then reports any accumulated errors
func (p *Pipeline) Done() (processedRepos map[string]map[repositoryKey]struct{}, err error) {
	close(p.done)
	p.workerWg.Wait()

	if p.pipelineError != nil {
		return nil, fmt.Errorf("pipeline: %w", p.pipelineError)
	}

	if len(p.cmdErrors.errs) > 0 {
		return nil, fmt.Errorf("pipeline: %w", p.cmdErrors)
	}

	return p.processedRepos, nil
}

// getWorker finds the channel associated with a storage. When no channel is
// found, one is created and n-workers are started to process requests.
// If parallelStorage is 0, a channel is created against a pseudo-storage to
// enforce the number of total concurrent jobs.
func (p *Pipeline) getWorker(storage string) chan<- *contextCommand {
	p.workersByStorageMu.Lock()
	defer p.workersByStorageMu.Unlock()

	workers := p.parallelStorage

	if p.parallelStorage == 0 {
		// if the workers are not limited by storage, then pretend there is a single storage with `parallel` workers
		storage = ""
		workers = p.parallel
	}

	ch, ok := p.workersByStorage[storage]
	if !ok {
		ch = make(chan *contextCommand)
		p.workersByStorage[storage] = ch

		for i := 0; i < workers; i++ {
			p.workerWg.Add(1)
			go p.worker(ch)
		}
	}
	return ch
}

func (p *Pipeline) worker(ch <-chan *contextCommand) {
	defer p.workerWg.Done()
	for {
		select {
		case <-p.done:
			return
		case cmd := <-ch:
			p.processCommand(cmd.Context, cmd.Command)
		}
	}
}

func (p *Pipeline) processCommand(ctx context.Context, cmd Command) {
	p.acquireWorkerSlot()
	defer p.releaseWorkerSlot()

	log := p.cmdLogger(cmd)
	log.Info(fmt.Sprintf("started %s", cmd.Name()))

	if err := cmd.Execute(ctx); err != nil {
		if errors.Is(err, ErrSkipped) {
			log.Warn(fmt.Sprintf("skipped %s", cmd.Name()))
		} else {
			log.WithError(err).Error(fmt.Sprintf("%s failed", cmd.Name()))
			p.addError(cmd.Repository(), err)
		}
		return
	}

	storageName := cmd.Repository().StorageName
	p.processedReposMu.Lock()
	if _, ok := p.processedRepos[storageName]; !ok {
		p.processedRepos[storageName] = make(map[repositoryKey]struct{})
	}
	p.processedRepos[storageName][NewRepositoryKey(cmd.Repository())] = struct{}{}
	p.processedReposMu.Unlock()

	log.Info(fmt.Sprintf("completed %s", cmd.Name()))
}

func (p *Pipeline) setErr(err error) {
	if p.pipelineError != nil {
		return
	}
	p.pipelineError = err
}

func (p *Pipeline) addError(repo *gitalypb.Repository, err error) {
	p.cmdErrors.AddError(repo, err)
}

func (p *Pipeline) cmdLogger(cmd Command) log.Logger {
	return p.log.WithFields(log.Fields{
		"command":         cmd.Name(),
		"storage_name":    cmd.Repository().StorageName,
		"relative_path":   cmd.Repository().RelativePath,
		"gl_project_path": cmd.Repository().GlProjectPath,
	})
}

// acquireWorkerSlot queues the worker until a slot is available.
func (p *Pipeline) acquireWorkerSlot() {
	if p.totalWorkers == nil {
		return
	}
	p.totalWorkers <- struct{}{}
}

// releaseWorkerSlot releases the worker slot.
func (p *Pipeline) releaseWorkerSlot() {
	if p.totalWorkers == nil {
		return
	}
	<-p.totalWorkers
}

// NewRepositoryKey returns a unique identifier for the provided repo.
func NewRepositoryKey(repo *gitalypb.Repository) repositoryKey {
	return repositoryKey(repo.StorageName + "-" + repo.RelativePath)
}
