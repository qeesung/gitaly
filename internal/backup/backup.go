package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// ErrSkipped means the repository was skipped because there was nothing to backup
	ErrSkipped = errors.New("repository skipped")
	// ErrDoesntExist means that the data was not found.
	ErrDoesntExist = errors.New("doesn't exist")
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
}

// Backup represents all the information needed to restore a backup for a repository
type Backup struct {
	// Steps are the ordered list of steps required to restore this backup
	Steps []Step
	// ObjectFormat is the name of the object hash used by the repository.
	ObjectFormat string
}

// Step represents an incremental step that makes up a complete backup for a repository
type Step struct {
	// BundlePath is the path of the bundle
	BundlePath string
	// RefPath is the path of the ref file
	RefPath string
	// PreviousRefPath is the path of the previous ref file
	PreviousRefPath string
	// CustomHooksPath is the path of the custom hooks archive
	CustomHooksPath string
}

// Locator finds sink backup paths for repositories
type Locator interface {
	// BeginFull returns a tentative first step needed to create a new full backup.
	BeginFull(ctx context.Context, repo *gitalypb.Repository, backupID string) *Step

	// BeginIncremental returns a tentative step needed to create a new incremental backup.
	BeginIncremental(ctx context.Context, repo *gitalypb.Repository, backupID string) (*Step, error)

	// Commit persists the step so that it can be looked up by FindLatest
	Commit(ctx context.Context, step *Step) error

	// FindLatest returns the latest backup that was written by Commit
	FindLatest(ctx context.Context, repo *gitalypb.Repository) (*Backup, error)

	// Find returns the repository backup at the given backupID. If the backup does
	// not exist then the error ErrDoesntExist is returned.
	Find(ctx context.Context, repo *gitalypb.Repository, backupID string) (*Backup, error)
}

// Repository abstracts git access required to make a repository backup
type Repository interface {
	// ListRefs fetches the full set of refs and targets for the repository.
	ListRefs(ctx context.Context) ([]git.Reference, error)
	// GetCustomHooks fetches the custom hooks archive.
	GetCustomHooks(ctx context.Context, out io.Writer) error
	// CreateBundle fetches a bundle that contains refs matching patterns.
	CreateBundle(ctx context.Context, out io.Writer, patterns io.Reader) error
	// Remove removes the repository. Does not return an error if the
	// repository cannot be found.
	Remove(ctx context.Context) error
	// Create creates the repository.
	Create(ctx context.Context, hash git.ObjectHash) error
	// FetchBundle fetches references from a bundle. Refs will be mirrored to
	// the repository.
	FetchBundle(ctx context.Context, reader io.Reader) error
	// SetCustomHooks updates the custom hooks for the repository.
	SetCustomHooks(ctx context.Context, reader io.Reader) error
}

// ResolveLocator returns a locator implementation based on a locator identifier.
func ResolveLocator(layout string, sink Sink) (Locator, error) {
	legacy := LegacyLocator{}
	switch layout {
	case "legacy":
		return legacy, nil
	case "pointer":
		return PointerLocator{
			Sink:     sink,
			Fallback: legacy,
		}, nil
	default:
		return nil, fmt.Errorf("unknown layout: %q", layout)
	}
}

// Manager manages process of the creating/restoring backups.
type Manager struct {
	sink    Sink
	conns   *client.Pool
	locator Locator

	// repositoryFactory returns an abstraction over git repositories in order
	// to create and restore backups.
	repositoryFactory func(ctx context.Context, repo *gitalypb.Repository, server storage.ServerInfo) (Repository, error)
}

// NewManager creates and returns initialized *Manager instance.
func NewManager(sink Sink, locator Locator, pool *client.Pool) *Manager {
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

			return newRemoteRepository(repo, conn), nil
		},
	}
}

// NewManagerLocal creates and returns a *Manager instance for operating on local repositories.
func NewManagerLocal(
	sink Sink,
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
			localRepo := localrepo.New(storageLocator, gitCmdFactory, catfileCache, repo)

			return newLocalRepository(storageLocator, gitCmdFactory, txManager, repoCounter, localRepo), nil
		},
	}
}

// RemoveAllRepositories removes all repositories in the specified storage name.
func (mgr *Manager) RemoveAllRepositories(ctx context.Context, req *RemoveAllRepositoriesRequest) error {
	if err := setContextServerInfo(ctx, &req.Server, req.StorageName); err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	repoClient, err := mgr.newRepoClient(ctx, req.Server)
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	_, err = repoClient.RemoveAll(ctx, &gitalypb.RemoveAllRequest{StorageName: req.StorageName})
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	return nil
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

	var step *Step
	if req.Incremental {
		var err error
		step, err = mgr.locator.BeginIncremental(ctx, req.VanityRepository, req.BackupID)
		if err != nil {
			return fmt.Errorf("manager: %w", err)
		}
	} else {
		step = mgr.locator.BeginFull(ctx, req.VanityRepository, req.BackupID)
	}

	refs, err := repo.ListRefs(ctx)
	switch {
	case status.Code(err) == codes.NotFound:
		return fmt.Errorf("manager: repository not found: %w", ErrSkipped)
	case err != nil:
		return fmt.Errorf("manager: %w", err)
	}

	if err := mgr.writeRefs(ctx, step.RefPath, refs); err != nil {
		return fmt.Errorf("manager: %w", err)
	}
	if err := mgr.writeBundle(ctx, repo, step, refs); err != nil {
		return fmt.Errorf("manager: %w", err)
	}
	if err := mgr.writeCustomHooks(ctx, repo, step.CustomHooksPath); err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	if err := mgr.locator.Commit(ctx, step); err != nil {
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

	if err := repo.Remove(ctx); err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	var backup *Backup
	if req.BackupID == "" {
		backup, err = mgr.locator.FindLatest(ctx, req.VanityRepository)
		if err != nil {
			return fmt.Errorf("manager: %w", err)
		}
	} else {
		backup, err = mgr.locator.Find(ctx, req.VanityRepository, req.BackupID)
		if err != nil {
			return fmt.Errorf("manager: %w", err)
		}
	}

	hash, err := git.ObjectHashByFormat(backup.ObjectFormat)
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	if err := repo.Create(ctx, hash); err != nil {
		return fmt.Errorf("manager: %w", err)
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
			if req.AlwaysCreate {
				return nil
			}

			if err := repo.Remove(ctx); err != nil {
				return fmt.Errorf("manager: remove on skipped: %w", err)
			}

			return fmt.Errorf("manager: %w: %s", ErrSkipped, err.Error())
		case err != nil:
			return fmt.Errorf("manager: %w", err)
		}

		// Git bundles can not be created for empty repositories. Since empty
		// repository backups do not contain a bundle, skip bundle restoration.
		if len(refs) > 0 {
			if err := mgr.restoreBundle(ctx, repo, step.BundlePath); err != nil {
				return fmt.Errorf("manager: %w", err)
			}
		}
		if err := mgr.restoreCustomHooks(ctx, repo, step.CustomHooksPath); err != nil {
			return fmt.Errorf("manager: %w", err)
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

func (mgr *Manager) writeBundle(ctx context.Context, repo Repository, step *Step, refs []git.Reference) (returnErr error) {
	if len(refs) == 0 {
		return nil
	}

	negatedRefs, err := mgr.negatedKnownRefs(ctx, step)
	if err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}
	defer func() {
		if err := negatedRefs.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("write bundle: %w", err)
		}
	}()

	patternReader, patternWriter := io.Pipe()
	defer func() {
		if err := patternReader.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("write bundle: %w", err)
		}
	}()
	go func() {
		defer patternWriter.Close()

		for _, ref := range refs {
			_, err := fmt.Fprintln(patternWriter, ref.Name)
			if err != nil {
				_ = patternWriter.CloseWithError(err)
				return
			}
		}
	}()

	w := NewLazyWriter(func() (io.WriteCloser, error) {
		return mgr.sink.GetWriter(ctx, step.BundlePath)
	})
	defer func() {
		if err := w.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("write bundle: %w", err)
		}
	}()

	if err := repo.CreateBundle(ctx, w, io.MultiReader(negatedRefs, patternReader)); err != nil {
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

			if err := d.Decode(&ref); err == io.EOF {
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

		if err := d.Decode(&ref); err == io.EOF {
			break
		} else if err != nil {
			return refs, fmt.Errorf("read refs: %w", err)
		}

		refs = append(refs, ref)
	}

	return refs, nil
}

func (mgr *Manager) restoreBundle(ctx context.Context, repo Repository, path string) error {
	reader, err := mgr.sink.GetReader(ctx, path)
	if err != nil {
		return fmt.Errorf("restore bundle: %q: %w", path, err)
	}
	defer reader.Close()

	if err := repo.FetchBundle(ctx, reader); err != nil {
		return fmt.Errorf("restore bundle: %q: %w", path, err)
	}
	return nil
}

func (mgr *Manager) writeCustomHooks(ctx context.Context, repo Repository, path string) (returnErr error) {
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
	w, err := mgr.sink.GetWriter(ctx, path)
	if err != nil {
		return fmt.Errorf("write refs: %w", err)
	}
	defer func() {
		if err := w.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("write refs: %w", err)
		}
	}()

	for _, ref := range refs {
		_, err = fmt.Fprintf(w, "%s %s\n", ref.Target, ref.Name)
		if err != nil {
			return fmt.Errorf("write refs: %w", err)
		}
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
