package repoutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tempdir"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type createConfig struct {
	gitOptions []git.Option
	skipInit   bool
}

// CreateOption is an option that can be passed to Create.
type CreateOption func(cfg *createConfig)

// WithBranchName overrides the default branch name that is to be used when creating the repository.
// If called with an empty string then the default branch name will not be changed.
func WithBranchName(branch string) CreateOption {
	return func(cfg *createConfig) {
		if branch == "" {
			return
		}

		cfg.gitOptions = append(cfg.gitOptions, git.ValueFlag{Name: "--initial-branch", Value: branch})
	}
}

// WithObjectHash overrides the default object hash of the created repository.
func WithObjectHash(hash git.ObjectHash) CreateOption {
	return func(cfg *createConfig) {
		cfg.gitOptions = append(cfg.gitOptions, git.ValueFlag{Name: "--object-format", Value: hash.Format})
	}
}

// WithSkipInit causes Create to skip calling git-init(1) so that the seeding function will be
// called with a nonexistent target directory. This can be useful when using git-clone(1) to seed
// the repository.
func WithSkipInit() CreateOption {
	return func(cfg *createConfig) {
		cfg.skipInit = true
	}
}

// Create will create a new repository in a race-free way with proper transactional semantics. The
// repository will only be created if it doesn't yet exist and if nodes which take part in the
// transaction reach quorum. Otherwise, the target path of the new repository will not be modified.
// The repository can optionally be seeded with contents
func Create(
	ctx context.Context,
	logger log.Logger,
	locator storage.Locator,
	gitCmdFactory git.CommandFactory,
	txManager transaction.Manager,
	repoCounter *counter.RepositoryCounter,
	repository storage.Repository,
	seedRepository func(repository *gitalypb.Repository) error,
	options ...CreateOption,
) error {
	targetPath, err := locator.GetRepoPath(repository, storage.WithRepositoryVerificationSkipped())
	if err != nil {
		return structerr.NewInvalidArgument("locate repository: %w", err)
	}

	// The repository must not exist on disk already, or otherwise we won't be able to
	// create it with atomic semantics.
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		return structerr.NewAlreadyExists("repository exists already")
	}

	newRepo, newRepoDir, cleanup, err := tempdir.NewRepository(ctx, repository.GetStorageName(), logger, locator)
	if err != nil {
		return fmt.Errorf("creating temporary repository: %w", err)
	}
	defer cleanup()

	// Note that we do not create the repository directly in its target location, but
	// instead create it in a temporary directory, first. This is done such that we can
	// guarantee atomicity and roll back the change easily in case an error happens.

	var cfg createConfig
	for _, option := range options {
		option(&cfg)
	}

	if !cfg.skipInit {
		stderr := &bytes.Buffer{}
		cmd, err := gitCmdFactory.NewWithoutRepo(ctx, git.Command{
			Name: "init",
			Flags: append([]git.Option{
				git.Flag{Name: "--bare"},
				git.Flag{Name: "--quiet"},
			}, cfg.gitOptions...),
			Args: []string{newRepoDir.Path()},
		}, git.WithStderr(stderr))
		if err != nil {
			return fmt.Errorf("spawning git-init: %w", err)
		}

		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("creating repository: %w, stderr: %q", err, stderr.String())
		}
	} else {
		if err := os.Remove(newRepoDir.Path()); err != nil {
			return fmt.Errorf("removing precreated directory: %w", err)
		}
	}

	if err := seedRepository(newRepo); err != nil {
		// Return the error returned by the callback function as-is so we don't clobber any
		// potential returned gRPC error codes.
		return err
	}

	// In order to guarantee that the repository is going to be the same across all
	// Gitalies in case we're behind Praefect, we walk the repository and hash all of
	// its files.
	voteHash := voting.NewVoteHash()
	if err := filepath.WalkDir(newRepoDir.Path(), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		switch path {
		// The way packfiles are generated may not be deterministic, so we skip over the
		// object database.
		case filepath.Join(newRepoDir.Path(), "objects"):
			return fs.SkipDir
		// FETCH_HEAD refers to the remote we're fetching from. This URL may not be
		// deterministic, e.g. when fetching from a temporary file like we do in
		// CreateRepositoryFromBundle.
		case filepath.Join(newRepoDir.Path(), "FETCH_HEAD"):
			return nil
		}

		// We do not care about directories.
		if entry.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("opening %q: %w", entry.Name(), err)
		}
		defer file.Close()

		if _, err := io.Copy(voteHash, file); err != nil {
			return fmt.Errorf("hashing %q: %w", entry.Name(), err)
		}

		return nil
	}); err != nil {
		return fmt.Errorf("walking repository: %w", err)
	}

	vote, err := voteHash.Vote()
	if err != nil {
		return fmt.Errorf("computing vote: %w", err)
	}

	// Create the full-repack timestamp in the new repository. This is done so that we don't
	// consider new repositories to have never been repacked yet, which would cause repository
	// housekeeping to perform a full repack right away. And in general, this would not really
	// be needed as the end result for most of the repository-creating RPCs would be a either an
	// empty or a neatly-packed repository anyway.
	//
	// As this timestamp should never impact the user-observable state of a repository we do not
	// include it in the voting hash.
	if err := stats.UpdateFullRepackTimestamp(newRepoDir.Path(), time.Now()); err != nil {
		return fmt.Errorf("creating full-repack timestamp: %w", err)
	}

	// We're now entering the critical section where we want to have exclusive access
	// over creation of the repository. So we:
	//
	// 1. Lock the repository path such that no other process can create it at the same
	//    time.
	// 2. Vote on the new repository's state.
	// 3. Move the repository into place.
	// 4. Do another confirmatory vote to signal that we performed the change.
	// 5. Unlock the repository again.
	//
	// This sequence guarantees that the change is atomic and can trivially be rolled
	// back in case we fail to either lock the repository or reach quorum in the initial
	// vote.
	unlock, err := Lock(ctx, logger, locator, repository)
	if err != nil {
		return fmt.Errorf("locking repository: %w", err)
	}
	defer unlock()

	// Now that the repository is locked, we must assert that it _still_ doesn't exist.
	// Otherwise, it could have happened that a concurrent RPC call created it while we created
	// and seeded our temporary repository. While we would notice this at the point of moving
	// the repository into place, we want to be as sure as possible that the action will succeed
	// previous to the first transactional vote.
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		return structerr.NewAlreadyExists("repository exists already")
	}

	if err := transaction.VoteOnContext(ctx, txManager, vote, voting.Prepared); err != nil {
		return structerr.NewFailedPrecondition("preparatory vote: %w", err)
	}

	syncer := safe.NewSyncer()
	if err := syncer.SyncRecursive(newRepoDir.Path()); err != nil {
		return fmt.Errorf("sync recursive: %w", err)
	}

	// Now that we have locked the repository and all Gitalies have agreed that they
	// want to do the same change we can move the repository into place.
	if err := os.Rename(newRepoDir.Path(), targetPath); err != nil {
		return fmt.Errorf("moving repository into place: %w", err)
	}

	storagePath, err := locator.GetStorageByName(repository.GetStorageName())
	if err != nil {
		return fmt.Errorf("get storage by name: %w", err)
	}

	if err := syncer.SyncHierarchy(storagePath, repository.GetRelativePath()); err != nil {
		return fmt.Errorf("sync hierarchy: %w", err)
	}

	if err := transaction.VoteOnContext(ctx, txManager, vote, voting.Committed); err != nil {
		return structerr.NewFailedPrecondition("committing vote: %w", err)
	}

	repoCounter.Increment(repository)

	// We unlock the repository implicitly via the deferred `Close()` call.
	return nil
}
