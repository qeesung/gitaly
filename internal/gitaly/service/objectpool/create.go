package objectpool

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errInvalidPoolDir = helper.ErrInvalidArgument(objectpool.ErrInvalidPoolDir)

	// errMissingOriginRepository is returned when the request is missing the
	// origin repository.
	errMissingOriginRepository = helper.ErrInvalidArgumentf("no origin repository")

	// errMissingPool is returned when the request is missing the object pool.
	errMissingPool = helper.ErrInvalidArgumentf("no object pool repository")
)

func (s *server) CreateObjectPool(ctx context.Context, in *gitalypb.CreateObjectPoolRequest) (*gitalypb.CreateObjectPoolResponse, error) {
	if in.GetOrigin() == nil {
		return nil, errMissingOriginRepository
	}

	pool, err := s.poolForRequest(in)
	if err != nil {
		return nil, err
	}

	origin := s.localrepo(in.GetOrigin())

	if pool.Exists() {
		return nil, status.Errorf(codes.FailedPrecondition, "pool already exists at: %v", pool.GetRelativePath())
	}

	if err := pool.Create(ctx, origin); err != nil {
		return nil, err
	}

	return &gitalypb.CreateObjectPoolResponse{}, nil
}

func (s *server) DeleteObjectPool(ctx context.Context, in *gitalypb.DeleteObjectPoolRequest) (*gitalypb.DeleteObjectPoolResponse, error) {
	pool, err := s.poolForRequest(in)
	if err != nil {
		return nil, err
	}

	if err := pool.Remove(ctx); err != nil {
		return nil, err
	}

	return &gitalypb.DeleteObjectPoolResponse{}, nil
}

// PoolRequest is the interface of a gRPC request that carries an object pool.
type PoolRequest interface {
	GetObjectPool() *gitalypb.ObjectPool
}

// ExtractPool returns the pool repository from the request or an error if the
// request did no contain a pool.
func ExtractPool(req PoolRequest) (*gitalypb.Repository, error) {
	poolRepo := req.GetObjectPool().GetRepository()
	if poolRepo == nil {
		return nil, errMissingPool
	}

	return poolRepo, nil
}

func (s *server) poolForRequest(req PoolRequest) (*objectpool.ObjectPool, error) {
	poolRepo, err := ExtractPool(req)
	if err != nil {
		return nil, err
	}

	pool, err := objectpool.NewObjectPool(s.locator, s.gitCmdFactory, s.catfileCache, s.txManager, s.housekeepingManager, poolRepo.GetStorageName(), poolRepo.GetRelativePath())
	if err != nil {
		if err == objectpool.ErrInvalidPoolDir {
			return nil, errInvalidPoolDir
		}

		return nil, helper.ErrInternal(err)
	}

	return pool, nil
}
