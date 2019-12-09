package repository

import (
	"context"
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func (s *server) ReplicateRepository(ctx context.Context, in *gitalypb.ReplicateRepositoryRequest) (*gitalypb.ReplicateRepositoryResponse, error) {
	if err := validateReplicateRepository(in); err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	if _, err := s.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{
		Repository: in.GetRepository(),
	}); err != nil {
		return nil, helper.ErrInternal(err)
	}

	outCtx := helper.IncomingToOutgoing(ctx)

	if err := syncRepository(outCtx, in); err != nil {
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

// newRemoteClient creates a new RemoteClient that talks to the same gitaly server
func newRemoteClient() (gitalypb.RemoteServiceClient, error) {
	conn, err := client.Dial(fmt.Sprintf("unix:%s", config.GitalyInternalSocketPath()), nil)
	if err != nil {
		return nil, fmt.Errorf("could not dial source: %v", err)
	}

	return gitalypb.NewRemoteServiceClient(conn), nil
}
