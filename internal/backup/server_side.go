package backup

import (
	"context"
	"errors"
	"fmt"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ServerSideAdapter allows calling the server-side backup RPCs `BackupRepository`
// and `RestoreRepository` through `backup.Strategy` such that server-side
// backups can be used with `backup.Pipeline`.
type ServerSideAdapter struct {
	pool *client.Pool
}

// NewServerSideAdapter creates and returns initialized *ServerSideAdapter instance.
func NewServerSideAdapter(pool *client.Pool) *ServerSideAdapter {
	return &ServerSideAdapter{
		pool: pool,
	}
}

// Create calls the BackupRepository RPC.
func (ss ServerSideAdapter) Create(ctx context.Context, req *CreateRequest) error {
	if err := setContextServerInfo(ctx, &req.Server, req.Repository.GetStorageName()); err != nil {
		return fmt.Errorf("server-side create: %w", err)
	}

	client, err := ss.newRepoClient(ctx, req.Server)
	if err != nil {
		return fmt.Errorf("server-side create: %w", err)
	}

	_, err = client.BackupRepository(ctx, &gitalypb.BackupRepositoryRequest{
		Repository:       req.Repository,
		VanityRepository: req.VanityRepository,
		BackupId:         req.BackupID,
		Incremental:      req.Incremental,
	})
	if err != nil {
		st := status.Convert(err)
		if st.Code() == codes.NotFound {
			return fmt.Errorf("server-side create: %w: %s", ErrSkipped, err.Error())
		}
		for _, detail := range st.Details() {
			switch detail.(type) {
			case *gitalypb.BackupRepositoryResponse_SkippedError:
				return fmt.Errorf("server-side create: %w: %s", ErrSkipped, err.Error())
			}
		}

		return fmt.Errorf("server-side create: %w", err)
	}

	return nil
}

// Restore calls the RestoreRepository RPC.
func (ss ServerSideAdapter) Restore(ctx context.Context, req *RestoreRequest) error {
	if err := setContextServerInfo(ctx, &req.Server, req.Repository.GetStorageName()); err != nil {
		return fmt.Errorf("server-side restore: %w", err)
	}

	client, err := ss.newRepoClient(ctx, req.Server)
	if err != nil {
		return fmt.Errorf("server-side restore: %w", err)
	}

	_, err = client.RestoreRepository(ctx, &gitalypb.RestoreRepositoryRequest{
		Repository:       req.Repository,
		VanityRepository: req.VanityRepository,
		AlwaysCreate:     req.AlwaysCreate,
		BackupId:         req.BackupID,
	})
	if err != nil {
		st := status.Convert(err)
		for _, detail := range st.Details() {
			switch detail.(type) {
			case *gitalypb.RestoreRepositoryResponse_SkippedError:
				return fmt.Errorf("server-side restore: %w: %s", ErrSkipped, err.Error())
			}
		}

		return structerr.New("server-side restore: %w", err)
	}

	return nil
}

// RemoveAllRepositories removes all repositories in the specified storage name.
func (ss ServerSideAdapter) RemoveAllRepositories(ctx context.Context, req *RemoveAllRepositoriesRequest) error {
	if err := setContextServerInfo(ctx, &req.Server, req.StorageName); err != nil {
		return fmt.Errorf("server-side remove all: %w", err)
	}

	repoClient, err := ss.newRepoClient(ctx, req.Server)
	if err != nil {
		return fmt.Errorf("server-side remove all: %w", err)
	}

	_, err = repoClient.RemoveAll(ctx, &gitalypb.RemoveAllRequest{StorageName: req.StorageName})
	if err != nil {
		return fmt.Errorf("server-side remove all: %w", err)
	}

	return nil
}

// RemoveRepository removes the specified repository from its storage.
func (ss ServerSideAdapter) RemoveRepository(ctx context.Context, req *RemoveRepositoryRequest) error {
	if err := setContextServerInfo(ctx, &req.Server, req.Repo.StorageName); err != nil {
		return fmt.Errorf("server-side remove repo: set context: %w", err)
	}

	repoClient, err := ss.newRepoClient(ctx, req.Server)
	if err != nil {
		return fmt.Errorf("server-side remove repo: create client: %w", err)
	}

	_, err = repoClient.RemoveRepository(ctx, &gitalypb.RemoveRepositoryRequest{Repository: req.Repo})
	if err != nil {
		return fmt.Errorf("server-side remove repo: remove: %w", err)
	}

	return nil
}

// ListRepositories returns a list of repositories found in the given storage.
func (ss ServerSideAdapter) ListRepositories(ctx context.Context, req *ListRepositoriesRequest) (repos []*gitalypb.Repository, err error) {
	if err := setContextServerInfo(ctx, &req.Server, req.StorageName); err != nil {
		return nil, fmt.Errorf("server-side list repos: set context: %w", err)
	}

	internalClient, err := ss.newInternalClient(ctx, req.Server)
	if err != nil {
		return nil, fmt.Errorf("server-side list repos: create client: %w", err)
	}

	stream, err := internalClient.WalkRepos(ctx, &gitalypb.WalkReposRequest{StorageName: req.StorageName})
	if err != nil {
		return nil, fmt.Errorf("server-side list repos: walk: %w", err)
	}

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		repos = append(repos, &gitalypb.Repository{RelativePath: resp.RelativePath, StorageName: req.StorageName})
	}

	return repos, nil
}

func (ss ServerSideAdapter) newRepoClient(ctx context.Context, server storage.ServerInfo) (gitalypb.RepositoryServiceClient, error) {
	conn, err := ss.pool.Dial(ctx, server.Address, server.Token)
	if err != nil {
		return nil, err
	}

	return gitalypb.NewRepositoryServiceClient(conn), nil
}

func (ss ServerSideAdapter) newInternalClient(ctx context.Context, server storage.ServerInfo) (gitalypb.InternalGitalyClient, error) {
	conn, err := ss.pool.Dial(ctx, server.Address, server.Token)
	if err != nil {
		return nil, err
	}

	return gitalypb.NewInternalGitalyClient(conn), nil
}
