package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/remoterepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/repoutil"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tempdir"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ErrInvalidSourceRepository is returned when attempting to replicate from an invalid source repository.
var ErrInvalidSourceRepository = status.Error(codes.NotFound, "invalid source repository")

// ReplicateRepository replicates data from a source repository to target repository. On the target
// repository, this operation ensures synchronization of the following components:
//
// - Git config
// - Git attributes
// - Custom Git hooks,
// - References and objects
// - (Optional) Object deduplication network membership
func (s *server) ReplicateRepository(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest) (*gitalypb.ReplicateRepositoryResponse, error) {
	if err := validateReplicateRepository(s.locator, in); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	if err := s.locator.ValidateRepository(in.GetRepository()); err != nil {
		repoPath, err := s.locator.GetRepoPath(in.GetRepository(), storage.WithRepositoryVerificationSkipped())
		if err != nil {
			return nil, structerr.NewInternal("%w", err)
		}

		if err = s.create(ctx, in, repoPath); err != nil {
			if errors.Is(err, ErrInvalidSourceRepository) {
				return nil, ErrInvalidSourceRepository
			}

			return nil, structerr.NewInternal("%w", err)
		}
	}

	repoClient, err := s.newRepoClient(ctx, in.GetSource().GetStorageName())
	if err != nil {
		return nil, structerr.NewInternal("new client: %w", err)
	}

	// We're checking for repository existence up front such that we can give a conclusive error
	// in case it doesn't. Otherwise, the error message returned to the client would depend on
	// the order in which the sync functions were executed. Most importantly, given that
	// `syncRepository` uses FetchInternalRemote which in turn uses gitaly-ssh, this code path
	// cannot pass up NotFound errors given that there is no communication channel between
	// Gitaly and gitaly-ssh.
	request, err := repoClient.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
		Repository: in.GetSource(),
	})
	if err != nil {
		return nil, structerr.NewInternal("checking for repo existence: %w", err)
	}
	if !request.GetExists() {
		return nil, ErrInvalidSourceRepository
	}

	outgoingCtx := metadata.IncomingToOutgoing(ctx)

	if err := s.replicateRepository(outgoingCtx, in.GetSource(), in.GetRepository(), in.GetReplicateObjectDeduplicationNetworkMembership()); err != nil {
		return nil, structerr.NewInternal("replicating repository: %w", err)
	}

	return &gitalypb.ReplicateRepositoryResponse{}, nil
}

func (s *server) replicateRepository(ctx context.Context, source, target *gitalypb.Repository, replicateObjectDeduplicationNetworkMembership bool) error {
	if err := s.syncGitconfig(ctx, source, target); err != nil {
		return fmt.Errorf("synchronizing gitconfig: %w", err)
	}

	// In git 2.43.0+, gitattributes supports reading from HEAD:.gitattributes,
	// so info/attributes is no longer needed. To make sure info/attributes file is cleaned up,
	// we delete it if it exists when reading from HEAD:.gitattributes is called.
	// This logic can be removed when ApplyGitattributes and GetInfoAttributes RPC are totally removed from
	// the code base.
	for _, repo := range []*gitalypb.Repository{source, target} {
		if repo == nil {
			continue
		}
		repoPath, err := s.locator.GetRepoPath(repo)
		if err != nil {
			s.logger.WithError(err).Error("failed to get repo path to delete info/gitattributes")
		} else {
			deletionErr := deleteInfoAttributesFile(repoPath)
			if !os.IsNotExist(deletionErr) {
				s.logger.WithError(deletionErr).Error("failed to delete info/gitattributes file at " + repoPath)
			}
		}
	}

	if replicateObjectDeduplicationNetworkMembership {
		// Sync the repository object pool before fetching the repository to avoid additional
		// duplication. If the object pool already exists on the target node, this will potentially
		// reduce the amount of time it takes to fetch the repository.
		if err := s.syncObjectPool(ctx, source, target); err != nil {
			return fmt.Errorf("synchronizing object pools: %w", err)
		}
	}

	if err := s.syncReferences(ctx, source, target); err != nil {
		return fmt.Errorf("synchronizing references: %w", err)
	}

	if err := s.syncCustomHooks(ctx, source, target); err != nil {
		return fmt.Errorf("synchronizing custom hooks: %w", err)
	}

	return nil
}

func validateReplicateRepository(locator storage.Locator, in *gitalypb.ReplicateRepositoryRequest) error {
	if err := locator.ValidateRepository(in.GetRepository(), storage.WithSkipRepositoryExistenceCheck()); err != nil {
		return err
	}

	if in.GetSource() == nil {
		return errors.New("source repository cannot be empty")
	}

	if in.GetRepository().GetStorageName() == in.GetSource().GetStorageName() {
		return errors.New("repository and source have the same storage")
	}

	return nil
}

func (s *server) create(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest, repoPath string) error {
	// if the directory exists, remove it
	if _, err := os.Stat(repoPath); err == nil {
		tempDir, err := tempdir.NewWithoutContext(in.GetRepository().GetStorageName(), s.logger, s.locator)
		if err != nil {
			return err
		}

		if err = os.Rename(repoPath, filepath.Join(tempDir.Path(), filepath.Base(repoPath))); err != nil {
			return fmt.Errorf("error deleting invalid repo: %w", err)
		}

		s.logger.WithField("repo_path", repoPath).WarnContext(ctx, "removed invalid repository")
	}

	if err := s.createFromSnapshot(ctx, in.GetSource(), in.GetRepository()); err != nil {
		return fmt.Errorf("could not create repository from snapshot: %w", err)
	}

	return nil
}

func (s *server) createFromSnapshot(ctx context.Context, source, target *gitalypb.Repository) error {
	if err := repoutil.Create(ctx, s.logger, s.locator, s.gitCmdFactory, s.txManager, s.repositoryCounter, target, func(repo *gitalypb.Repository) error {
		if err := s.extractSnapshot(ctx, source, repo); err != nil {
			return fmt.Errorf("extracting snapshot: %w", err)
		}

		return nil
	}); err != nil {
		return fmt.Errorf("creating repository: %w", err)
	}

	return nil
}

func (s *server) extractSnapshot(ctx context.Context, source, target *gitalypb.Repository) error {
	repoClient, err := s.newRepoClient(ctx, source.GetStorageName())
	if err != nil {
		return fmt.Errorf("new client: %w", err)
	}

	stream, err := repoClient.GetSnapshot(ctx, &gitalypb.GetSnapshotRequest{Repository: source})
	if err != nil {
		return fmt.Errorf("get snapshot: %w", err)
	}

	// We need to catch a possible 'invalid repository' error from GetSnapshot. On an empty read,
	// BSD tar exits with code 0 so we'd receive the error when waiting for the command. GNU tar on
	// Linux exits with a non-zero code, which causes Go to return an os.ExitError hiding the original
	// error reading from stdin. To get access to the error on both Linux and macOS, we read the first
	// message from the stream here to get access to the possible 'invalid repository' first on both
	// platforms.
	firstBytes, err := stream.Recv()
	if err != nil {
		switch {
		case structerr.GRPCCode(err) == codes.NotFound && strings.Contains(err.Error(), "GetRepoPath: not a git repository:"):
			// The error condition exists for backwards compatibility purposes, only,
			// and can be removed in the next release.
			return ErrInvalidSourceRepository
		case structerr.GRPCCode(err) == codes.NotFound && strings.Contains(err.Error(), storage.ErrRepositoryNotFound.Error()):
			return ErrInvalidSourceRepository
		case structerr.GRPCCode(err) == codes.FailedPrecondition && strings.Contains(err.Error(), storage.ErrRepositoryNotValid.Error()):
			return ErrInvalidSourceRepository
		default:
			return fmt.Errorf("first snapshot read: %w", err)
		}
	}

	snapshotReader := io.MultiReader(
		bytes.NewReader(firstBytes.GetData()),
		streamio.NewReader(func() ([]byte, error) {
			resp, err := stream.Recv()
			return resp.GetData(), err
		}),
	)

	targetPath, err := s.locator.GetRepoPath(target, storage.WithRepositoryVerificationSkipped())
	if err != nil {
		return fmt.Errorf("target path: %w", err)
	}

	stderr := &bytes.Buffer{}
	cmd, err := command.New(ctx, s.logger, []string{"tar", "-C", targetPath, "-xvf", "-"},
		command.WithStdin(snapshotReader),
		command.WithStderr(stderr),
	)
	if err != nil {
		return fmt.Errorf("create tar command: %w", err)
	}

	if err = cmd.Wait(); err != nil {
		return structerr.New("wait for tar: %w", err).WithMetadata("stderr", stderr)
	}

	return nil
}

func (s *server) syncReferences(ctx context.Context, source, target *gitalypb.Repository) error {
	repo := s.localrepo(target)

	if err := fetchInternalRemote(ctx, s.txManager, s.conns, repo, source); err != nil {
		return fmt.Errorf("fetch internal remote: %w", err)
	}

	return nil
}

func fetchInternalRemote(
	ctx context.Context,
	txManager transaction.Manager,
	conns *client.Pool,
	repo *localrepo.Repo,
	remoteRepoProto *gitalypb.Repository,
) error {
	var stderr bytes.Buffer
	if err := repo.FetchInternal(
		ctx,
		remoteRepoProto,
		[]string{git.MirrorRefSpec},
		localrepo.FetchOpts{
			Prune:  true,
			Stderr: &stderr,
			// By default, Git will fetch any tags that point into the fetched references. This check
			// requires time, and is ultimately a waste of compute because we already mirror all refs
			// anyway, including tags. By adding `--no-tags` we can thus ask Git to skip that and thus
			// accelerate the fetch.
			Tags: localrepo.FetchOptsTagsNone,
			CommandOptions: []git.CmdOpt{
				git.WithConfig(git.ConfigPair{Key: "fetch.negotiationAlgorithm", Value: "skipping"}),
				// Disable the consistency checks of objects fetched into the replicated repository.
				// These fetched objects come from preexisting internal sources, thus it would be
				// problematic for the fetch to fail consistency checks due to altered requirements.
				git.WithConfig(git.ConfigPair{Key: "fetch.fsckObjects", Value: "false"}),
			},
		},
	); err != nil {
		if errors.As(err, &localrepo.FetchFailedError{}) {
			return structerr.New("%w", err).WithMetadata("stderr", stderr.String())
		}

		return fmt.Errorf("fetch: %w", err)
	}

	remoteRepo, err := remoterepo.New(ctx, remoteRepoProto, conns)
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	remoteDefaultBranch, err := remoteRepo.HeadReference(ctx)
	if err != nil {
		return structerr.NewInternal("getting remote default branch: %w", err)
	}

	defaultBranch, err := repo.HeadReference(ctx)
	if err != nil {
		return structerr.NewInternal("getting local default branch: %w", err)
	}

	if defaultBranch != remoteDefaultBranch {
		if err := repo.SetDefaultBranch(ctx, txManager, remoteDefaultBranch); err != nil {
			return structerr.NewInternal("setting default branch: %w", err)
		}
	}

	return nil
}

// syncCustomHooks replicates custom hooks from a source to a target.
func (s *server) syncCustomHooks(ctx context.Context, source, target *gitalypb.Repository) error {
	repoClient, err := s.newRepoClient(ctx, source.GetStorageName())
	if err != nil {
		return fmt.Errorf("creating repo client: %w", err)
	}

	stream, err := repoClient.GetCustomHooks(ctx, &gitalypb.GetCustomHooksRequest{
		Repository: source,
	})
	if err != nil {
		return fmt.Errorf("getting custom hooks: %w", err)
	}

	reader := streamio.NewReader(func() ([]byte, error) {
		request, err := stream.Recv()
		return request.GetData(), err
	})

	if err := repoutil.SetCustomHooks(ctx, s.logger, s.locator, s.txManager, reader, target); err != nil {
		return fmt.Errorf("setting custom hooks: %w", err)
	}

	return nil
}

func (s *server) syncGitconfig(ctx context.Context, source, target *gitalypb.Repository) error {
	repoClient, err := s.newRepoClient(ctx, source.GetStorageName())
	if err != nil {
		return err
	}

	repoPath, err := s.locator.GetRepoPath(target)
	if err != nil {
		return err
	}

	stream, err := repoClient.GetConfig(ctx, &gitalypb.GetConfigRequest{
		Repository: source,
	})
	if err != nil {
		return err
	}

	configPath := filepath.Join(repoPath, "config")
	if err := s.writeFile(ctx, configPath, perm.SharedFile, streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetData(), err
	})); err != nil {
		return err
	}

	return nil
}

// syncObjectPool syncs the Git alternates file and sets up object pools as needed.
func (s *server) syncObjectPool(ctx context.Context, sourceRepoProto, targetRepoProto *gitalypb.Repository) error {
	sourceObjectPoolClient, err := s.newObjectPoolClient(ctx, sourceRepoProto.GetStorageName())
	if err != nil {
		return fmt.Errorf("create object pool client: %w", err)
	}

	// Check if source repository has an object pool. If the source repository is not linked to an
	// object pool, the RPC returns successfully with no error and an empty response.
	objectPoolResp, err := sourceObjectPoolClient.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{
		Repository: sourceRepoProto,
	})
	if err != nil {
		return fmt.Errorf("get source object pool: %w", err)
	}

	sourcePoolProto := objectPoolResp.GetObjectPool()
	targetRepo := s.localrepo(targetRepoProto)

	// If the source repository is not linked with an object pool, the target repository will not
	// need to be linked.
	if sourcePoolProto == nil {
		// In the case where the source repository does not have any Git alternates, but the
		// existing target repository does, the target repository should have its alternates
		// disconnected to match the current state of the source repository.
		if err := objectpool.Disconnect(ctx, targetRepo, s.logger, s.txManager); err != nil {
			return fmt.Errorf("disconnect target from object pool: %w", err)
		}

		return nil
	}

	// Check the target repository for an existing object pool link.
	targetPool, err := objectpool.FromRepo(s.logger, s.locator, s.gitCmdFactory, s.catfileCache, s.txManager, s.housekeepingManager, targetRepo)
	if err != nil && !errors.Is(err, objectpool.ErrAlternateObjectDirNotExist) {
		return fmt.Errorf("get target object pool: %w", err)
	}

	if targetPool != nil {
		sourcePoolRelPath := sourcePoolProto.GetRepository().GetRelativePath()
		targetPoolRelPath := targetPool.GetRelativePath()

		// If the target repository is linked to a different object pool than the source repository,
		// the target repository can not be linked to the new object pool without first
		// disconnecting from its current object pool. This scenario is very unlikely so an error is
		// returned instead of performing the object pool disconnect. In the future, it may be
		// decided to appropriately handle this scenario.
		if sourcePoolRelPath != targetPoolRelPath {
			return structerr.NewFailedPrecondition("target repository links to different object pool").
				WithMetadata("source_pool_path", sourcePoolRelPath).
				WithMetadata("target_pool_path", targetPoolRelPath)
		}

		// If the target repository is linked to the same object pool as the source repository,
		// object pool replication is not necessary.
		return nil
	}

	targetPoolProto := proto.Clone(sourcePoolProto).(*gitalypb.ObjectPool)
	targetPoolProto.GetRepository().StorageName = targetRepoProto.GetStorageName()

	// Check if object pool required for target repository already exists on the current node.
	targetPool, err = objectpool.FromProto(s.logger, s.locator, s.gitCmdFactory, s.catfileCache, s.txManager, s.housekeepingManager, targetPoolProto)
	switch {
	case errors.Is(err, objectpool.ErrInvalidPoolRepository):
		// In the case where the source repository does link to an object pool, but the object pool
		// does not yet exist on the target storage, the object pool must be replicated as well. To
		// accomplish this, a snapshot of the source object pool is extracted onto the target
		// storage.
		if err := s.createFromSnapshot(ctx, sourcePoolProto.GetRepository(), targetPoolProto.GetRepository()); err != nil {
			return fmt.Errorf("replicate object pool: %w", err)
		}

		targetPool, err = objectpool.FromProto(s.logger, s.locator, s.gitCmdFactory, s.catfileCache, s.txManager, s.housekeepingManager, targetPoolProto)
		if err != nil {
			return fmt.Errorf("get replicated object pool: %w", err)
		}
	case err != nil:
		return fmt.Errorf("get target object pool: %w", err)
	}

	// Link the replicated repository to the object pool.
	if err := targetPool.Link(ctx, targetRepo); err != nil {
		return fmt.Errorf("link target object pool: %w", err)
	}

	return nil
}

func (s *server) writeFile(ctx context.Context, path string, mode os.FileMode, reader io.Reader) (returnedErr error) {
	parentDir := filepath.Dir(path)
	if err := os.MkdirAll(parentDir, perm.SharedDir); err != nil {
		return err
	}

	lockedFile, err := safe.NewLockingFileWriter(path, safe.LockingFileWriterConfig{
		FileWriterConfig: safe.FileWriterConfig{
			FileMode: mode,
		},
	})
	if err != nil {
		return fmt.Errorf("creating file writer: %w", err)
	}
	defer func() {
		if err := lockedFile.Close(); err != nil && returnedErr == nil {
			returnedErr = err
		}
	}()

	if _, err := io.Copy(lockedFile, reader); err != nil {
		return err
	}

	if err := transaction.CommitLockedFile(ctx, s.txManager, lockedFile); err != nil {
		return err
	}

	return nil
}

// newRepoClient creates a new RepositoryClient that talks to the gitaly of the source repository
func (s *server) newRepoClient(ctx context.Context, storageName string) (gitalypb.RepositoryServiceClient, error) {
	gitalyServerInfo, err := storage.ExtractGitalyServer(ctx, storageName)
	if err != nil {
		return nil, err
	}

	conn, err := s.conns.Dial(ctx, gitalyServerInfo.Address, gitalyServerInfo.Token)
	if err != nil {
		return nil, err
	}

	return gitalypb.NewRepositoryServiceClient(conn), nil
}

// newObjectPoolClient create a new ObjectPoolClient that talks to the gitaly of the source repository.
func (s *server) newObjectPoolClient(ctx context.Context, storageName string) (gitalypb.ObjectPoolServiceClient, error) {
	serverInfo, err := storage.ExtractGitalyServer(ctx, storageName)
	if err != nil {
		return nil, err
	}

	conn, err := s.conns.Dial(ctx, serverInfo.Address, serverInfo.Token)
	if err != nil {
		return nil, err
	}

	return gitalypb.NewObjectPoolServiceClient(conn), nil
}
