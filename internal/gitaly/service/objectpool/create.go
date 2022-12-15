package objectpool

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/repoutil"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

// errMissingOriginRepository is returned when the request is missing the
// origin repository.
var errMissingOriginRepository = structerr.NewInvalidArgument("no origin repository")

func (s *server) CreateObjectPool(ctx context.Context, in *gitalypb.CreateObjectPoolRequest) (*gitalypb.CreateObjectPoolResponse, error) {
	if in.GetOrigin() == nil {
		return nil, errMissingOriginRepository
	}

	poolRepo := in.GetObjectPool().GetRepository()
	if poolRepo == nil {
		return nil, errMissingPool
	}

	if !housekeeping.IsPoolRepository(poolRepo) {
		return nil, errInvalidPoolDir
	}

	if featureflag.AtomicCreateObjectPool.IsEnabled(ctx) {
		if err := repoutil.Create(ctx, s.locator, s.gitCmdFactory, s.txManager, poolRepo, func(poolRepo *gitalypb.Repository) error {
			if _, err := objectpool.Create(
				ctx,
				s.locator,
				s.gitCmdFactory,
				s.catfileCache,
				s.txManager,
				s.housekeepingManager,
				&gitalypb.ObjectPool{
					Repository: poolRepo,
				},
				s.localrepo(in.GetOrigin()),
			); err != nil {
				return err
			}

			return nil
		}, repoutil.WithSkipInit()); err != nil {
			return nil, structerr.New("creating object pool: %w", err)
		}
	} else {
		if _, err := objectpool.Create(
			ctx,
			s.locator,
			s.gitCmdFactory,
			s.catfileCache,
			s.txManager,
			s.housekeepingManager,
			&gitalypb.ObjectPool{
				Repository: poolRepo,
			},
			s.localrepo(in.GetOrigin()),
		); err != nil {
			return nil, structerr.New("creating object pool: %w", err)
		}
	}

	return &gitalypb.CreateObjectPoolResponse{}, nil
}
