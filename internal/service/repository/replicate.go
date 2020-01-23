package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/safe"
	"gitlab.com/gitlab-org/gitaly/internal/tempdir"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
	"golang.org/x/sync/errgroup"
)

func (s *server) ReplicateRepository(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest) (*gitalypb.ReplicateRepositoryResponse, error) {
	if err := validateReplicateRepository(in); err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	syncFuncs := []func(context.Context, *gitalypb.ReplicateRepositoryRequest) error{
		syncInfoAttributes,
	}

	repoPath, err := helper.GetPath(in.GetRepository())
	if err != nil {
		return nil, helper.ErrInternal(err)
	}

	if helper.IsGitDirectory(repoPath) {
		syncFuncs = append(syncFuncs, syncRepository)
	} else {
		if err = s.create(ctx, in, repoPath); err != nil {
			return nil, helper.ErrInternal(err)
		}
	}

	g, ctx := errgroup.WithContext(ctx)
	outgoingCtx := helper.IncomingToOutgoing(ctx)

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
		tempDir, err := tempdir.ForDeleteAllRepositories(in.GetRepository().GetStorageName())
		if err != nil {
			return err
		}

		if err = os.Rename(repoPath, filepath.Join(tempDir, filepath.Base(repoPath))); err != nil {
			return fmt.Errorf("error deleting invalid repo: %v", err)
		}

		ctxlogrus.Extract(ctx).WithField("repo_path", repoPath).Warn("removed invalid repository")
	}

	if err := s.createFromSnapshot(ctx, in); err != nil {
		return fmt.Errorf("could not create repository from snapshot: %v", err)
	}

	return nil
}

func (s *server) createFromSnapshot(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest) error {
	tempRepo, tempPath, err := tempdir.NewAsRepository(ctx, in.GetRepository())
	if err != nil {
		return err
	}

	if _, err := s.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{
		Repository: tempRepo,
	}); err != nil {
		return err
	}

	repoClient, err := newRepoClient(ctx, in.GetSource().GetStorageName())
	if err != nil {
		return err
	}

	stream, err := repoClient.GetSnapshot(ctx, &gitalypb.GetSnapshotRequest{Repository: in.GetSource()})
	if err != nil {
		return err
	}

	snapshotReader := streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetData(), err
	})

	cmd, err := command.New(ctx, exec.Command("tar", "-C", tempPath, "-xvf", "-"), snapshotReader, nil, nil)
	if err != nil {
		return err
	}

	if err = cmd.Wait(); err != nil {
		return err
	}

	targetPath, err := helper.GetPath(in.GetRepository())
	if err != nil {
		return err
	}

	if err = os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

	if err := os.Rename(tempPath, targetPath); err != nil {
		return err
	}

	return nil
}

func syncRepository(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest) error {
	remoteClient, err := newRemoteClient()
	if err != nil {
		return err
	}

	if _, err = remoteClient.FetchInternalRemote(ctx, &gitalypb.FetchInternalRemoteRequest{
		Repository:       in.GetRepository(),
		RemoteRepository: in.GetSource(),
	}); err != nil {
		return err
	}

	return nil
}

func syncInfoAttributes(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest) error {
	repoClient, err := newRepoClient(ctx, in.GetSource().GetStorageName())
	if err != nil {
		return err
	}

	repoPath, err := helper.GetRepoPath(in.GetRepository())
	if err != nil {
		return err
	}

	infoPath := filepath.Join(repoPath, "info")
	attributesPath := filepath.Join(infoPath, "attributes")

	if err := os.MkdirAll(infoPath, 0755); err != nil {
		return err
	}

	fw, err := safe.CreateFileWriter(attributesPath)
	if err != nil {
		return err
	}
	defer fw.Close()

	stream, err := repoClient.GetInfoAttributes(ctx, &gitalypb.GetInfoAttributesRequest{
		Repository: in.GetSource(),
	})
	if err != nil {
		return err
	}

	if _, err := io.Copy(fw, streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetAttributes(), err
	})); err != nil {
		return err
	}

	if err = fw.Commit(); err != nil {
		return err
	}

	if err := os.Chmod(attributesPath, attributesFileMode); err != nil {
		return err
	}

	return os.Rename(attributesPath, attributesPath)
}

// newRemoteClient creates a new RemoteClient that talks to the same gitaly server
func newRemoteClient() (gitalypb.RemoteServiceClient, error) {
	conn, err := client.Dial(fmt.Sprintf("unix:%s", config.GitalyInternalSocketPath()), nil)
	if err != nil {
		return nil, fmt.Errorf("could not dial source: %v", err)
	}

	return gitalypb.NewRemoteServiceClient(conn), nil
}

// newRepoClient creates a new RepositoryClient that talks to the gitaly of the source repository
func newRepoClient(ctx context.Context, storageName string) (gitalypb.RepositoryServiceClient, error) {
	conn, err := helper.ClientConnection(ctx, storageName)
	if err != nil {
		return nil, err
	}

	return gitalypb.NewRepositoryServiceClient(conn), nil
}
