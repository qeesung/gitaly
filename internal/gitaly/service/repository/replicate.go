package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/v14/internal/command"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service/remote"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v14/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v14/internal/tempdir"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v14/streamio"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrInvalidSourceRepository is returned when attempting to replicate from an invalid source repository.
var ErrInvalidSourceRepository = status.Error(codes.NotFound, "invalid source repository")

func (s *server) ReplicateRepository(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest) (*gitalypb.ReplicateRepositoryResponse, error) {
	if err := validateReplicateRepository(in); err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	repoPath, err := s.locator.GetPath(in.GetRepository())
	if err != nil {
		return nil, helper.ErrInternal(err)
	}

	if !storage.IsGitDirectory(repoPath) {
		if err = s.create(ctx, in, repoPath); err != nil {
			if errors.Is(err, ErrInvalidSourceRepository) {
				return nil, ErrInvalidSourceRepository
			}

			return nil, helper.ErrInternal(err)
		}
	}

	repoClient, err := s.newRepoClient(ctx, in.GetSource().GetStorageName())
	if err != nil {
		return nil, helper.ErrInternalf("new client: %w", err)
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
		return nil, helper.ErrInternalf("checking for repo existence: %w", err)
	}
	if !request.GetExists() {
		return nil, helper.ErrNotFoundf("source repository does not exist")
	}

	// We're not using the context of the errgroup here, as an error
	// returned by either of the called functions would cancel the
	// respective other function. Given that we're doing RPC calls in
	// them, cancellation of the calls would mean that the remote side
	// may still modify the repository even though the local side has
	// returned already.
	g, _ := errgroup.WithContext(ctx)
	outgoingCtx := metadata.IncomingToOutgoing(ctx)

	syncFuncs := []func(context.Context, *gitalypb.ReplicateRepositoryRequest) error{
		s.syncGitconfig,
		s.syncInfoAttributes,
		s.syncRepository,
	}

	for _, f := range syncFuncs {
		f := f // rescoping f
		g.Go(func() error { return f(outgoingCtx, in) })
	}

	if err := g.Wait(); err != nil {
		return nil, helper.ErrInternal(err)
	}

	return &gitalypb.ReplicateRepositoryResponse{}, nil
}

func validateReplicateRepository(in *gitalypb.ReplicateRepositoryRequest) error {
	if in.GetRepository() == nil {
		return errors.New("repository cannot be empty")
	}

	if in.GetSource() == nil {
		return errors.New("source repository cannot be empty")
	}

	if in.GetRepository().GetRelativePath() != in.GetSource().GetRelativePath() {
		return errors.New("both source and repository should have the same relative path")
	}

	if in.GetRepository().GetStorageName() == in.GetSource().GetStorageName() {
		return errors.New("repository and source have the same storage")
	}

	return nil
}

func (s *server) create(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest, repoPath string) error {
	// if the directory exists, remove it
	if _, err := os.Stat(repoPath); err == nil {
		tempDir, err := tempdir.NewWithoutContext(in.GetRepository().GetStorageName(), s.locator)
		if err != nil {
			return err
		}

		if err = os.Rename(repoPath, filepath.Join(tempDir.Path(), filepath.Base(repoPath))); err != nil {
			return fmt.Errorf("error deleting invalid repo: %v", err)
		}

		ctxlogrus.Extract(ctx).WithField("repo_path", repoPath).Warn("removed invalid repository")
	}

	if err := s.createFromSnapshot(ctx, in); err != nil {
		return fmt.Errorf("could not create repository from snapshot: %w", err)
	}

	return nil
}

func (s *server) createFromSnapshot(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest) error {
	if err := s.createRepository(ctx, in.GetRepository(), func(repo *gitalypb.Repository) error {
		if err := s.extractSnapshot(ctx, in.GetSource(), repo); err != nil {
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
		if st, ok := status.FromError(err); ok {
			if st.Code() == codes.NotFound && strings.HasPrefix(st.Message(), "GetRepoPath: not a git repository:") {
				return ErrInvalidSourceRepository
			}
		}

		return fmt.Errorf("first snapshot read: %w", err)
	}

	snapshotReader := io.MultiReader(
		bytes.NewReader(firstBytes.GetData()),
		streamio.NewReader(func() ([]byte, error) {
			resp, err := stream.Recv()
			return resp.GetData(), err
		}),
	)

	targetPath, err := s.locator.GetPath(target)
	if err != nil {
		return fmt.Errorf("target path: %w", err)
	}

	stderr := &bytes.Buffer{}
	cmd, err := command.New(ctx, exec.Command("tar", "-C", targetPath, "-xvf", "-"), snapshotReader, nil, stderr)
	if err != nil {
		return fmt.Errorf("create tar command: %w", err)
	}

	if err = cmd.Wait(); err != nil {
		return fmt.Errorf("wait for tar, stderr: %q, err: %w", stderr, err)
	}

	return nil
}

func (s *server) syncRepository(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest) error {
	repo := s.localrepo(in.GetRepository())

	if err := remote.FetchInternalRemote(ctx, s.cfg, s.conns, repo, in.GetSource()); err != nil {
		return fmt.Errorf("fetch internal remote: %w", err)
	}

	return nil
}

func (s *server) syncGitconfig(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest) error {
	repoClient, err := s.newRepoClient(ctx, in.GetSource().GetStorageName())
	if err != nil {
		return err
	}

	repoPath, err := s.locator.GetRepoPath(in.GetRepository())
	if err != nil {
		return err
	}

	stream, err := repoClient.GetConfig(ctx, &gitalypb.GetConfigRequest{
		Repository: in.GetSource(),
	})
	if err != nil {
		return err
	}

	configPath := filepath.Join(repoPath, "config")
	if err := s.writeFile(ctx, configPath, 0o644, streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetData(), err
	})); err != nil {
		return err
	}

	return nil
}

func (s *server) syncInfoAttributes(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest) error {
	repoClient, err := s.newRepoClient(ctx, in.GetSource().GetStorageName())
	if err != nil {
		return err
	}

	repoPath, err := s.locator.GetRepoPath(in.GetRepository())
	if err != nil {
		return err
	}

	stream, err := repoClient.GetInfoAttributes(ctx, &gitalypb.GetInfoAttributesRequest{
		Repository: in.GetSource(),
	})
	if err != nil {
		return err
	}

	attributesPath := filepath.Join(repoPath, "info", "attributes")
	if err := s.writeFile(ctx, attributesPath, attributesFileMode, streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetAttributes(), err
	})); err != nil {
		return err
	}

	return nil
}

func (s *server) writeFile(ctx context.Context, path string, mode os.FileMode, reader io.Reader) (returnedErr error) {
	parentDir := filepath.Dir(path)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
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
