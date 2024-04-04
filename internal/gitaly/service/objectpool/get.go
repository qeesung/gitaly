package objectpool

import (
	"context"
	"errors"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) GetObjectPool(ctx context.Context, in *gitalypb.GetObjectPoolRequest) (*gitalypb.GetObjectPoolResponse, error) {
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(repository)

	objectPool, err := objectpool.FromRepo(s.logger, s.locator, s.gitCmdFactory, s.catfileCache, s.txManager, s.housekeepingManager, repo)
	if err != nil && !errors.Is(err, objectpool.ErrAlternateObjectDirNotExist) {
		s.logger.
			WithError(err).
			WithField("storage", repository.GetStorageName()).
			WithField("relative_path", repository.GetRelativePath()).
			WarnContext(ctx, "alternates file does not point to valid git repository")
	}

	if objectPool == nil {
		return &gitalypb.GetObjectPoolResponse{}, nil
	}

	objectPoolProto := objectPool.ToProto()
	storagectx.RunWithTransaction(ctx, func(tx storagectx.Transaction) {
		// The object pool's relative path is pointing to the transaction's snapshot. Return
		// the original relative path in the response.
		objectPoolProto.Repository = tx.OriginalRepository(objectPoolProto.Repository)
	})

	return &gitalypb.GetObjectPoolResponse{
		ObjectPool: objectPoolProto,
	}, nil
}
