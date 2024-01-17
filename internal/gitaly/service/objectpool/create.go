package objectpool

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/repoutil"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
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

	if !storage.IsPoolRepository(poolRepo) {
		return nil, errInvalidPoolDir
	}

	// repoutil.Create creates the repositories in a temporary directory. This means the repository is not created in the location
	// expected by the transaction manager. This makes sense without transactions, but with transactions, there's no real point in
	// doing so given a failed transaction's state is anyway removed. Creating the repository in a temporary directory is problematic
	// as the reference transaction hook is invoked for the repository from unexpected location, causing the transaction to fail to
	// associate the reference updates with the repository.
	//
	// Run the repository creation without the transaction in the context. The transactions reads the created repository's state from
	// the disk when committing it, so it's not necessary to capture the updates from the reference-transaction hook. This avoids the
	// problem for now, and later with transactions enabled by default we can stop creating repositories in unexpected locations.
	ctxWithoutTransaction := storage.ContextWithTransactionID(ctx, 0)
	if err := repoutil.Create(ctxWithoutTransaction, s.logger, s.locator, s.gitCmdFactory, s.txManager, s.repositoryCounter, poolRepo, func(poolRepo *gitalypb.Repository) error {
		if _, err := objectpool.Create(
			ctxWithoutTransaction,
			s.logger,
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
		err := structerr.New("creating object pool: %w", err)

		// This is really ugly: Rails expects a FailedPrecondition error code in its
		// rspecs, but `repoutil.Create()` returns `AlreadyExists` instead. So in
		// case we see that error code we override it. We should eventually fix this
		// and instead use something like structured errors.
		if err.Code() == codes.AlreadyExists {
			err = err.WithGRPCCode(codes.FailedPrecondition)
		}

		return nil, err
	}

	return &gitalypb.CreateObjectPoolResponse{}, nil
}
