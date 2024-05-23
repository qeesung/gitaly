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
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/remoterepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/repoutil"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
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

	// The partitioning hint should not be forwarded to other Gitaly nodes as the path is irrelevant for them.
	outgoingCtx := storagectx.RemovePartitioningHintFromIncomingContext(ctx)
	outgoingCtx = metadata.IncomingToOutgoing(outgoingCtx)

	if err := s.replicateRepository(outgoingCtx, in.GetSource(), in.GetRepository()); err != nil {
		return nil, structerr.NewInternal("replicating repository: %w", err)
	}

	return &gitalypb.ReplicateRepositoryResponse{}, nil
}

func (s *server) replicateRepository(ctx context.Context, source, target *gitalypb.Repository) error {
	if err := s.syncGitconfig(ctx, source, target); err != nil {
		return fmt.Errorf("synchronizing gitconfig: %w", err)
	}

	// In git 2.43.0+, gitattributes supports reading from HEAD:.gitattributes,
	// so info/attributes is no longer needed. To make sure info/attributes file is cleaned up,
	// we delete it if it exists when reading from HEAD:.gitattributes is called.
	// This logic can be removed when ApplyGitattributes and GetInfoAttributes RPC are totally removed from
	// the code base.
	if target != nil {
		repoPath, err := s.locator.GetRepoPath(target)
		if err != nil {
			return structerr.NewInternal("get repo path: %w", err)
		}
		if deletionErr := deleteInfoAttributesFile(repoPath); deletionErr != nil {
			return structerr.NewInternal("delete info/gitattributes file: %w", deletionErr).WithMetadata("path", repoPath)
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
