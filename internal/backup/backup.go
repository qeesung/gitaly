package backup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// ErrSkipped means the repository was skipped because there was nothing to backup
	ErrSkipped = errors.New("repository skipped")
	// ErrDoesntExist means that the data was not found.
	ErrDoesntExist = errors.New("doesn't exist")

	backupLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "gitaly_backup_latency_seconds",
			Help: "Latency of a repository backup by phase",
		},
		[]string{"phase"})
	backupBundleSize = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "gitaly_backup_bundle_bytes",
			Help:    "Size of a Git bundle uploaded in a backup",
			Buckets: prometheus.ExponentialBucketsRange(1, 10*math.Pow(1024, 3), 20), // up to 10GB
		})
)

// Sink is an abstraction over the real storage used for storing/restoring backups.
type Sink interface {
	io.Closer
	// GetWriter saves the written data to relativePath. It is the callers
	// responsibility to call Close and check any subsequent errors.
	GetWriter(ctx context.Context, relativePath string) (io.WriteCloser, error)
	// GetReader returns a reader that servers the data stored by relativePath.
	// If relativePath doesn't exists the ErrDoesntExist will be returned.
	GetReader(ctx context.Context, relativePath string) (io.ReadCloser, error)
	// SignedURL returns a URL that can be used to GET the blob for the duration
	// specified in expiry.
	SignedURL(ctx context.Context, relativePath string, expiry time.Duration) (string, error)
}

// Backup represents all the information needed to restore a backup for a repository
type Backup struct {
	// ID is the identifier that uniquely identifies the backup for this repository.
	ID string `toml:"-"`
	// Repository is the repository being backed up.
	Repository storage.Repository `toml:"-"`
	// Steps are the ordered list of steps required to restore this backup
	Steps []Step `toml:"steps"`
	// ObjectFormat is the name of the object hash used by the repository.
	ObjectFormat string `toml:"object_format"`
	// HeadReference is the reference that HEAD points to.
	HeadReference string `toml:"head_reference,omitempty"`
}

// Step represents an incremental step that makes up a complete backup for a repository
type Step struct {
	// BundlePath is the path of the bundle
	BundlePath string `toml:"bundle_path,omitempty"`
	// RefPath is the path of the ref file
	RefPath string `toml:"ref_path,omitempty"`
	// PreviousRefPath is the path of the previous ref file
	PreviousRefPath string `toml:"previous_ref_path,omitempty"`
	// CustomHooksPath is the path of the custom hooks archive
	CustomHooksPath string `toml:"custom_hooks_path,omitempty"`
}

// Locator finds sink backup paths for repositories
type Locator interface {
	// BeginFull returns the tentative backup paths needed to create a full backup.
	BeginFull(ctx context.Context, repo storage.Repository, backupID string) *Backup

	// BeginIncremental returns the backup with the last element of Steps being
	// the tentative step needed to create an incremental backup.
	BeginIncremental(ctx context.Context, repo storage.Repository, backupID string) (*Backup, error)

	// Commit persists the backup so that it can be looked up by FindLatest. It
	// is expected that the last element of Steps will be the newly created
	// backup.
	Commit(ctx context.Context, backup *Backup) error

	// FindLatest returns the latest backup that was written by Commit
	FindLatest(ctx context.Context, repo storage.Repository) (*Backup, error)

	// Find returns the repository backup at the given backupID. If the backup does
	// not exist then the error ErrDoesntExist is returned.
	Find(ctx context.Context, repo storage.Repository, backupID string) (*Backup, error)
}

// Repository abstracts git access required to make a repository backup
type Repository interface {
	// ListRefs fetches the full set of refs and targets for the repository.
	ListRefs(ctx context.Context) ([]git.Reference, error)
	// GetCustomHooks fetches the custom hooks archive.
	GetCustomHooks(ctx context.Context, out io.Writer) error
	// CreateBundle fetches a bundle that contains refs matching patterns. When
	// patterns is nil all refs are bundled.
	CreateBundle(ctx context.Context, out io.Writer, patterns io.Reader) error
	// Remove removes the repository. Does not return an error if the
	// repository cannot be found.
	Remove(ctx context.Context) error
	// Create creates the repository.
	Create(ctx context.Context, hash git.ObjectHash, defaultBranch string) error
	// FetchBundle fetches references from a bundle. Refs will be mirrored to
	// the repository.
	FetchBundle(ctx context.Context, reader io.Reader, updateHead bool) error
	// SetCustomHooks updates the custom hooks for the repository.
	SetCustomHooks(ctx context.Context, reader io.Reader) error
	// ObjectHash detects the object hash used by the repository.
	ObjectHash(ctx context.Context) (git.ObjectHash, error)
	// HeadReference fetches the reference pointed to by HEAD.
	HeadReference(ctx context.Context) (git.ReferenceName, error)
	// ResetRefs attempts to reset the list of refs in the repository to match the
	// specified refs slice. This can fail if objects pointed to by a ref no longer
	// exists in the repository. The list of refs should not include the symbolic
	// HEAD reference.
	ResetRefs(ctx context.Context, refs []git.Reference) error
	// SetHeadReference sets the symbolic HEAD reference of the repository to the
	// given target, for example a branch name.
	SetHeadReference(ctx context.Context, target git.ReferenceName) error
}

// ResolveLocator returns a locator implementation based on a locator identifier.
func ResolveLocator(layout string, sink Sink) (Locator, error) {
	var locator Locator = LegacyLocator{}

	switch layout {
	case "legacy":
	case "pointer":
		locator = PointerLocator{
			Sink:     sink,
			Fallback: locator,
		}
	default:
		return nil, fmt.Errorf("unknown layout: %q", layout)
	}

	locator = ManifestLocator{
		Sink:     sink,
		Fallback: locator,
	}

	return locator, nil
}

// Manager manages process of the creating/restoring backups.
type Manager struct {
	sink    Sink
	conns   *client.Pool
	locator Locator
	logger  log.Logger

	// repositoryFactory returns an abstraction over git repositories in order
	// to create and restore backups.
	repositoryFactory func(ctx context.Context, repo *gitalypb.Repository, server storage.ServerInfo) (Repository, error)
}

// NewManager creates and returns initialized *Manager instance.
func NewManager(sink Sink, logger log.Logger, locator Locator, pool *client.Pool) *Manager {
	return &Manager{
		sink:    sink,
		conns:   pool,
		locator: locator,
		repositoryFactory: func(ctx context.Context, repo *gitalypb.Repository, server storage.ServerInfo) (Repository, error) {
			if err := setContextServerInfo(ctx, &server, repo.GetStorageName()); err != nil {
				return nil, err
			}

			conn, err := pool.Dial(ctx, server.Address, server.Token)
			if err != nil {
				return nil, err
			}

			return NewRemoteRepository(repo, conn), nil
		},
		logger: logger,
	}
}

// NewManagerLocal creates and returns a *Manager instance for operating on local repositories.
func NewManagerLocal(
	sink Sink,
	logger log.Logger,
	locator Locator,
	storageLocator storage.Locator,
	gitCmdFactory git.CommandFactory,
	catfileCache catfile.Cache,
	txManager transaction.Manager,
	repoCounter *counter.RepositoryCounter,
) *Manager {
	return &Manager{
		sink:    sink,
		conns:   nil, // Will be removed once the restore operations are part of the Repository interface.
		locator: locator,
		repositoryFactory: func(ctx context.Context, repo *gitalypb.Repository, server storage.ServerInfo) (Repository, error) {
			localRepo := localrepo.New(logger, storageLocator, gitCmdFactory, catfileCache, repo)

			return NewLocalRepository(logger, storageLocator, gitCmdFactory, txManager, repoCounter, localRepo), nil
		},
		logger: logger,
	}
}

// RemoveRepository removes the specified repository from its storage.
func (mgr *Manager) RemoveRepository(ctx context.Context, req *RemoveRepositoryRequest) error {
	if err := setContextServerInfo(ctx, &req.Server, req.Repo.StorageName); err != nil {
		return fmt.Errorf("remove repo: set context: %w", err)
	}

	repoClient, err := mgr.newRepoClient(ctx, req.Server)
	if err != nil {
		return fmt.Errorf("remove repo: create client: %w", err)
	}

	_, err = repoClient.RemoveRepository(ctx, &gitalypb.RemoveRepositoryRequest{Repository: req.Repo})
	if err != nil {
		return fmt.Errorf("remove repo: remove: %w", err)
	}

	return nil
}

// ListRepositories returns a list of repositories found in the given storage.
func (mgr *Manager) ListRepositories(ctx context.Context, req *ListRepositoriesRequest) (repos []*gitalypb.Repository, err error) {
	if err := setContextServerInfo(ctx, &req.Server, req.StorageName); err != nil {
		return nil, fmt.Errorf("list repos: set context: %w", err)
	}

	internalClient, err := mgr.newInternalClient(ctx, req.Server)
	if err != nil {
		return nil, fmt.Errorf("list repos: create client: %w", err)
	}

	stream, err := internalClient.WalkRepos(ctx, &gitalypb.WalkReposRequest{StorageName: req.StorageName})
	if err != nil {
		return nil, fmt.Errorf("list repos: walk: %w", err)
	}

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list repos: receiving messages: %w", err)
		}

		repos = append(repos, &gitalypb.Repository{RelativePath: resp.RelativePath, StorageName: req.StorageName})
	}

	return repos, nil
}

// Create creates a repository backup.
func (mgr *Manager) Create(ctx context.Context, req *CreateRequest) error {
	if req.VanityRepository == nil {
		req.VanityRepository = req.Repository
	}
	repo, err := mgr.repositoryFactory(ctx, req.Repository, req.Server)
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	var backup *Backup
	if req.Incremental {
		var err error
		backup, err = mgr.locator.BeginIncremental(ctx, req.VanityRepository, req.BackupID)
		if err != nil {
			return fmt.Errorf("manager: %w", err)
		}
	} else {
		backup = mgr.locator.BeginFull(ctx, req.VanityRepository, req.BackupID)
	}

	hash, err := repo.ObjectHash(ctx)
	switch {
	case status.Code(err) == codes.NotFound:
		return fmt.Errorf("manager: repository not found: %w", ErrSkipped)
	case err != nil:
		return fmt.Errorf("manager: %w", err)
	}
	backup.ObjectFormat = hash.Format

	headRef, err := repo.HeadReference(ctx)
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}
	backup.HeadReference = headRef.String()

	refs, err := repo.ListRefs(ctx)
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	step := &backup.Steps[len(backup.Steps)-1]

	if err := mgr.writeRefs(ctx, step.RefPath, refs); err != nil {
		return fmt.Errorf("manager: %w", err)
	}
	if len(refs) > 0 {
		if err := mgr.writeBundle(ctx, repo, step); err != nil {
			return fmt.Errorf("manager: %w", err)
		}
	}
	if err := mgr.writeCustomHooks(ctx, repo, step.CustomHooksPath); err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	if err := mgr.locator.Commit(ctx, backup); err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	return nil
}

// Restore restores a repository from a backup. If req.BackupID is empty, the
// latest backup will be used.
func (mgr *Manager) Restore(ctx context.Context, req *RestoreRequest) error {
	if req.VanityRepository == nil {
		req.VanityRepository = req.Repository
	}

	repo, err := mgr.repositoryFactory(ctx, req.Repository, req.Server)
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	var backup *Backup
	if req.BackupID == "" {
		backup, err = mgr.locator.FindLatest(ctx, req.VanityRepository)
	} else {
		backup, err = mgr.locator.Find(ctx, req.VanityRepository, req.BackupID)
	}
	switch {
	case errors.Is(err, ErrDoesntExist):
		return fmt.Errorf("manager: %w: %s", ErrSkipped, err.Error())
	case err != nil:
		return fmt.Errorf("manager: %w", err)
	}

	if len(backup.Steps) == 0 {
		return fmt.Errorf("manager: no backup steps")
	}

	// If we can't reset the refs, perform a full restore by recreating the repo and cloning from the bundle.
	if err := mgr.restoreFromBundle(ctx, repo, backup, req.AlwaysCreate); err != nil {
		return fmt.Errorf("manager: restore from bundle: %w", err)
	}

	// Restore custom hooks. The path is the same regardless of increment.
	latestStep := backup.Steps[len(backup.Steps)-1]
	return mgr.restoreCustomHooks(ctx, repo, latestStep.CustomHooksPath)
}

func (mgr *Manager) restoreFromBundle(ctx context.Context, repo Repository, backup *Backup, alwaysCreate bool) error {
	hash, err := git.ObjectHashByFormat(backup.ObjectFormat)
	if err != nil {
		return err
	}

	defaultBranch, defaultBranchKnown := git.ReferenceName(backup.HeadReference).Branch()

	if err := repo.Remove(ctx); err != nil {
		return err
	}
	if err := repo.Create(ctx, hash, defaultBranch); err != nil {
		return err
	}

	for _, step := range backup.Steps {
		refs, err := mgr.readRefs(ctx, step.RefPath)
		switch {
		case errors.Is(err, ErrDoesntExist):
			// For compatibility with existing backups we need to make sure the
			// repository exists even if there's no bundle for project
			// repositories (not wiki or snippet repositories).  Gitaly does
			// not know which repository is which type so here we accept a
			// parameter to tell us to employ this behaviour. Since the
			// repository has already been created, we simply skip cleaning up.
			if alwaysCreate {
				return nil
			}

			if err := repo.Remove(ctx); err != nil {
				return fmt.Errorf("remove on skipped: %w", err)
			}

			return fmt.Errorf("%w: %s", ErrSkipped, err.Error())
		case err != nil:
			return fmt.Errorf("read refs: %w", err)
		}

		// Git bundles can not be created for empty repositories. Since empty
		// repository backups do not contain a bundle, skip bundle restoration.
		if len(refs) > 0 {
			if err := mgr.restoreBundle(ctx, repo, step.BundlePath, !defaultBranchKnown); err != nil {
				return fmt.Errorf("restore bundle: %w", err)
			}
		}
	}

	return nil
}

// setContextServerInfo overwrites server with gitaly connection info from ctx metadata when server is zero.
func setContextServerInfo(ctx context.Context, server *storage.ServerInfo, storageName string) error {
	if !server.Zero() {
		return nil
	}

	var err error
	*server, err = storage.ExtractGitalyServer(ctx, storageName)
	if err != nil {
		return fmt.Errorf("set context server info: %w", err)
	}

	return nil
}

func (mgr *Manager) writeBundle(ctx context.Context, repo Repository, step *Step) (returnErr error) {
	timer := prometheus.NewTimer(backupLatency.WithLabelValues("bundle"))
	defer timer.ObserveDuration()

	var patterns io.Reader
	if len(step.PreviousRefPath) > 0 {
		// If there is a previous ref path, then we are creating an increment
		negatedRefs, err := mgr.negatedKnownRefs(ctx, step)
		if err != nil {
			return fmt.Errorf("write bundle: %w", err)
		}
		defer func() {
			if err := negatedRefs.Close(); err != nil && returnErr == nil {
				returnErr = fmt.Errorf("write bundle: %w", err)
			}
		}()

		patterns = io.MultiReader(strings.NewReader("--all\n"), negatedRefs)
	}

	w := NewLazyWriter(func() (io.WriteCloser, error) {
		return mgr.sink.GetWriter(ctx, step.BundlePath)
	})
	defer func() {
		backupBundleSize.Observe(float64(w.BytesWritten()))
		if err := w.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("write bundle: %w", err)
		}
	}()

	if err := repo.CreateBundle(ctx, w, patterns); err != nil {
		if errors.Is(err, localrepo.ErrEmptyBundle) {
			return fmt.Errorf("write bundle: %w: no changes to bundle", ErrSkipped)
		}
		return fmt.Errorf("write bundle: %w", err)
	}

	return nil
}

func (mgr *Manager) negatedKnownRefs(ctx context.Context, step *Step) (io.ReadCloser, error) {
	if len(step.PreviousRefPath) == 0 {
		return io.NopCloser(new(bytes.Reader)), nil
	}

	r, w := io.Pipe()

	go func() {
		defer w.Close()

		reader, err := mgr.sink.GetReader(ctx, step.PreviousRefPath)
		if err != nil {
			_ = w.CloseWithError(err)
			return
		}
		defer reader.Close()

		d := git.NewShowRefDecoder(reader)
		for {
			var ref git.Reference

			if err := d.Decode(&ref); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				_ = w.CloseWithError(err)
				return
			}

			if _, err := fmt.Fprintf(w, "^%s\n", ref.Target); err != nil {
				_ = w.CloseWithError(err)
				return
			}
		}
	}()

	return r, nil
}

func (mgr *Manager) readRefs(ctx context.Context, path string) ([]git.Reference, error) {
	reader, err := mgr.sink.GetReader(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read refs: %w", err)
	}
	defer reader.Close()

	var refs []git.Reference

	d := git.NewShowRefDecoder(reader)
	for {
		var ref git.Reference

		if err := d.Decode(&ref); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return refs, fmt.Errorf("read refs: %w", err)
		}

		// HEAD is tracked as a symbolic reference in the backup manifest and will be restored separately.
		if ref.Name == "HEAD" {
			continue
		}

		refs = append(refs, ref)
	}

	return refs, nil
}

func (mgr *Manager) restoreBundle(ctx context.Context, repo Repository, path string, updateHead bool) error {
	reader, err := mgr.sink.GetReader(ctx, path)
	if err != nil {
		return fmt.Errorf("restore bundle: %q: %w", path, err)
	}
	defer reader.Close()

	if err := repo.FetchBundle(ctx, reader, updateHead); err != nil {
		return fmt.Errorf("restore bundle: %q: %w", path, err)
	}
	return nil
}

func (mgr *Manager) writeCustomHooks(ctx context.Context, repo Repository, path string) (returnErr error) {
	timer := prometheus.NewTimer(backupLatency.WithLabelValues("custom_hooks"))
	defer timer.ObserveDuration()

	w := NewLazyWriter(func() (io.WriteCloser, error) {
		return mgr.sink.GetWriter(ctx, path)
	})
	defer func() {
		if err := w.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("write custom hooks: %w", err)
		}
	}()
	if err := repo.GetCustomHooks(ctx, w); err != nil {
		return fmt.Errorf("write custom hooks: %w", err)
	}
	return nil
}

func (mgr *Manager) restoreCustomHooks(ctx context.Context, repo Repository, path string) error {
	reader, err := mgr.sink.GetReader(ctx, path)
	if err != nil {
		if errors.Is(err, ErrDoesntExist) {
			return nil
		}
		return fmt.Errorf("restore custom hooks: %w", err)
	}
	defer reader.Close()

	if err := repo.SetCustomHooks(ctx, reader); err != nil {
		return fmt.Errorf("restore custom hooks, %q: %w", path, err)
	}
	return nil
}

// writeRefs writes the previously fetched list of refs in the same output
// format as `git-show-ref(1)`
func (mgr *Manager) writeRefs(ctx context.Context, path string, refs []git.Reference) (returnErr error) {
	timer := prometheus.NewTimer(backupLatency.WithLabelValues("refs"))
	defer timer.ObserveDuration()

	w, err := mgr.sink.GetWriter(ctx, path)
	if err != nil {
		return fmt.Errorf("write refs: %w", err)
	}
	defer func() {
		if err := w.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("write refs: %w", err)
		}
	}()

	buf := bufio.NewWriter(w)
	for _, ref := range refs {
		_, err = fmt.Fprintf(buf, "%s %s\n", ref.Target, ref.Name)
		if err != nil {
			return fmt.Errorf("write refs: %w", err)
		}
	}

	if err := buf.Flush(); err != nil {
		return fmt.Errorf("write refs: %w", err)
	}

	return nil
}

func (mgr *Manager) newRepoClient(ctx context.Context, server storage.ServerInfo) (gitalypb.RepositoryServiceClient, error) {
	conn, err := mgr.conns.Dial(ctx, server.Address, server.Token)
	if err != nil {
		return nil, err
	}

	return gitalypb.NewRepositoryServiceClient(conn), nil
}

func (mgr *Manager) newInternalClient(ctx context.Context, server storage.ServerInfo) (gitalypb.InternalGitalyClient, error) {
	conn, err := mgr.conns.Dial(ctx, server.Address, server.Token)
	if err != nil {
		return nil, err
	}

	return gitalypb.NewInternalGitalyClient(conn), nil
}
