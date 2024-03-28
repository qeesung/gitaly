package storagemgr

import (
	"context"
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tracing"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// MetadataKeySnapshotRelativePath is the header key that contains the snapshot's relative path. Rails relays
// the relative path in the RPC calls performed as part of access checks.
const MetadataKeySnapshotRelativePath = "relative-path-bin"

var (
	// ErrQuarantineConfiguredOnMutator is returned when a mutator request is received with a quarantine configured.
	ErrQuarantineConfiguredOnMutator = errors.New("quarantine configured on a mutator request")
	// ErrQuarantineWithoutSnapshotRelativePath is returned when a request is configured with a quarantine but the snapshot's
	// relative path was not sent in a header.
	ErrQuarantineWithoutSnapshotRelativePath = errors.New("quarantined request did not contain snapshot relative path")
	// ErrRepositoriesInDifferentStorages is returned when trying to access two repositories in different storages in the
	// same transaction.
	ErrRepositoriesInDifferentStorages = structerr.NewInvalidArgument("additional and target repositories are in different storages")
)

// NonTransactionalRPCs are the RPCs that do not support transactions.
var NonTransactionalRPCs = map[string]struct{}{
	// This isn't registered in protoregistry so mark it here as non-transactional.
	"/grpc.health.v1.Health/Check": {},

	// These are missing annotations. We don't have a suitable scope for them
	// so mark these as non-transactional here.
	"/gitaly.ServerService/DiskStatistics": {},
	"/gitaly.ServerService/ServerInfo":     {},
	"/gitaly.ServerService/ClockSynced":    {},
	"/gitaly.ServerService/ReadinessCheck": {},

	// ReplicateRepository is replicating the attributes and config which the
	// WAL won't support. This is pending removal of their replication.
	//
	// ReplicateRepository may also create a repository which is not yet supported
	// through the WAL.
	"/gitaly.RepositoryService/ReplicateRepository": {},

	// Below RPCs implement functionality which isn't going to be supported by WAL.
	// Handle these as non-transactional. Their usage must be removed prior to enabling WAL.
	//
	// Attributes are going to be read from HEAD. Writing out a separate attributes file
	// won't be supported.
	"/gitaly.RepositoryService/ApplyGitattributes": {},
	// SetFullPath writes the full path into git config and is the last RPC that writes into the
	// git config. Writing into the config won't be supported.
	"/gitaly.RepositoryService/SetFullPath": {},
}

// NewUnaryInterceptor returns an unary interceptor that manages a unary RPC's transaction. It starts a transaction
// on the target repository of the request and rewrites the request to point to the transaction's snapshot repository.
// The transaction is committed if the handler doesn't return an error and rolled back otherwise.
func NewUnaryInterceptor(logger log.Logger, registry *protoregistry.Registry, txRegistry *TransactionRegistry, mgr *PartitionManager, locator storage.Locator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (_ interface{}, returnedErr error) {
		if _, ok := NonTransactionalRPCs[info.FullMethod]; ok {
			return handler(ctx, req)
		}

		methodInfo, err := registry.LookupMethod(info.FullMethod)
		if err != nil {
			return nil, fmt.Errorf("lookup method: %w", err)
		}

		txReq, err := transactionalizeRequest(ctx, logger, txRegistry, mgr, locator, methodInfo, req.(proto.Message))
		if err != nil {
			return nil, err
		}
		defer func() { returnedErr = txReq.finishTransaction(returnedErr) }()

		return handler(txReq.ctx, txReq.firstMessage)
	}
}

// peekedStream allows for peeking the first message of ServerStream. Reading the first message would leave
// handler unable to read the first message as it was already consumed. peekedStream allows for restoring the
// stream so the RPC handler can read the first message as usual. It additionally supports overriding the
// context of the stream.
type peekedStream struct {
	context      context.Context
	firstMessage proto.Message
	firstError   error
	grpc.ServerStream
}

func (ps *peekedStream) Context() context.Context {
	return ps.context
}

func (ps *peekedStream) RecvMsg(dst interface{}) error {
	if ps.firstError != nil {
		firstError := ps.firstError
		ps.firstError = nil
		return firstError
	}

	if ps.firstMessage != nil {
		marshaled, err := proto.Marshal(ps.firstMessage)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}

		if err := proto.Unmarshal(marshaled, dst.(proto.Message)); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}

		ps.firstMessage = nil
		return nil
	}

	return ps.ServerStream.RecvMsg(dst)
}

// NewStreamInterceptor returns a stream interceptor that manages a streaming RPC's transaction. It starts a transaction
// on the target repository of the first request and rewrites the request to point to the transaction's snapshot repository.
// The transaction is committed if the handler doesn't return an error and rolled back otherwise.
func NewStreamInterceptor(logger log.Logger, registry *protoregistry.Registry, txRegistry *TransactionRegistry, mgr *PartitionManager, locator storage.Locator) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (returnedErr error) {
		if _, ok := NonTransactionalRPCs[info.FullMethod]; ok {
			return handler(srv, ss)
		}

		methodInfo, err := registry.LookupMethod(info.FullMethod)
		if err != nil {
			return fmt.Errorf("lookup method: %w", err)
		}

		req := methodInfo.NewRequest()
		if err := ss.RecvMsg(req); err != nil {
			// All of the repository scoped streaming RPCs send the repository in the first message.
			// Generally it should be fine to error out in all cases if there is no message sent.
			// To maintain compatibility with tests, we instead invoke the handler to let them return
			// the asserted error messages. Once the transaction management is on by default, we should
			// error out here directly and amend the failing test cases.
			return handler(srv, &peekedStream{
				context:      ss.Context(),
				firstError:   err,
				ServerStream: ss,
			})
		}

		txReq, err := transactionalizeRequest(ss.Context(), logger, txRegistry, mgr, locator, methodInfo, req)
		if err != nil {
			return err
		}
		defer func() { returnedErr = txReq.finishTransaction(returnedErr) }()

		return handler(srv, &peekedStream{
			context:      txReq.ctx,
			firstMessage: txReq.firstMessage,
			ServerStream: ss,
		})
	}
}

// transactionalizedRequest contains the context and the first request to pass into the RPC handler to
// run it correctly against the transaction.
type transactionalizedRequest struct {
	// ctx is the request's context with the transaction added into it.
	ctx context.Context
	// firstMessage is the message to pass to the RPC as the first message. The target repository
	// in it has been rewritten to point to the snapshot repository.
	firstMessage proto.Message
	// finishTransaction takes in the error returned from the handler and returns the error
	// that should be returned to the client. If the handler error is nil, the transaction is committed.
	// If the handler error is not nil, the transaction is rolled back.
	finishTransaction func(error) error
}

// nonTransactionalRequest returns a no-op transactionalizedRequest that configures the RPC handler to be
// run as normal without a transaction.
func nonTransactionalRequest(ctx context.Context, firstMessage proto.Message) transactionalizedRequest {
	return transactionalizedRequest{
		ctx:               ctx,
		firstMessage:      firstMessage,
		finishTransaction: func(err error) error { return err },
	}
}

// transactionalizeRequest begins a transaction for the repository targeted in the request. It returns the context and the request that the handler should
// be invoked with. In addition, it returns a function that must be called with the error returned from the handler to either commit or rollback the
// transaction. The returned values are valid even if the request should not run transactionally.
func transactionalizeRequest(ctx context.Context, logger log.Logger, txRegistry *TransactionRegistry, mgr *PartitionManager, locator storage.Locator, methodInfo protoregistry.MethodInfo, req proto.Message) (_ transactionalizedRequest, returnedErr error) {
	if methodInfo.Scope != protoregistry.ScopeRepository {
		return nonTransactionalRequest(ctx, req), nil
	}

	targetRepo, err := methodInfo.TargetRepo(req)
	if err != nil {
		if errors.Is(err, protoregistry.ErrRepositoryFieldNotFound) {
			// The above error is returned when the repository field is not set in the request.
			// Return instead the error many tests are asserting to be returned from the handlers.
			return transactionalizedRequest{}, structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet)
		}

		return transactionalizedRequest{}, fmt.Errorf("extract target repository: %w", err)
	}

	if methodInfo.Operation != protoregistry.OpAccessor && methodInfo.Operation != protoregistry.OpMutator {
		// Transactions support only accessors and mutators.
		return nonTransactionalRequest(ctx, req), nil
	}

	if targetRepo.GitObjectDirectory != "" || len(targetRepo.GitAlternateObjectDirectories) > 0 {
		// The object directories should only be configured on a repository coming from a request that
		// was already configured with a quarantine directory and is being looped back to Gitaly from Rails'
		// authorization checks. If that's the case, the request should already be running in scope of a
		// transaction and the repository rewritten to point to the snapshot repository. We thus don't start
		// a new transaction if we encounter this.
		//
		// This property is violated in tests which manually configure the object directory or the alternate
		// object directory. This allows for circumventing the transaction management by configuring the either
		// of the object directories. We'll leave this unaddressed for now and later address this by removing
		// the options to configure object directories and alternates in a request.

		if methodInfo.Operation == protoregistry.OpMutator {
			// Accessor requests may come with quarantine configured from Rails' access checks. Since the
			// RPC that triggered these access checks would already run in a transaction and target a
			// snapshot, we won't start another one. Mutators however are rejected to prevent writes
			// unintentionally targeting the main repository.
			return transactionalizedRequest{}, ErrQuarantineConfiguredOnMutator
		}

		rewrittenReq, err := restoreSnapshotRelativePath(ctx, methodInfo, req)
		if err != nil {
			return transactionalizedRequest{}, fmt.Errorf("restore snapshot relative path: %w", err)
		}

		return nonTransactionalRequest(ctx, rewrittenReq), nil
	}

	// While the PartitionManager already verifies the repository's storage and relative path, it does not
	// return the exact same error messages as some RPCs are testing for at the moment. In order to maintain
	// compatibility with said tests, validate the repository here ahead of time and return the possible error
	// as is.
	if err := locator.ValidateRepository(targetRepo, storage.WithSkipRepositoryExistenceCheck()); err != nil {
		return transactionalizedRequest{}, err
	}

	// Object pools need to be placed in the same partition as their members. Below we figure out which repository,
	// if any, the target repository of the RPC must be partitioned with. We figure this out using two strategies:
	//
	// The general case is handled by extracting the additional repository from the RPC, and paritioning the target
	// repository of the RPC with the additional repository. Many of the ObjectPoolService's RPCs operate on two
	// repositories. Depending on the RPC, the additional repository is either the object pool itself or a member
	// of the pool.
	//
	// CreateFork is special cased. The fork must partitioned with the source repository in order to successfully
	// link it with the object pool later. The source repository is not tagged as additional repository in the
	// CreateForkRequest. If the request is CreateForkRequest, we extract the source repository and partition the
	// fork with it.
	var additionalRepo *gitalypb.Repository
	if additionalRepo, err = methodInfo.AdditionalRepo(req); err != nil {
		if !errors.Is(err, protoregistry.ErrRepositoryFieldNotFound) {
			return transactionalizedRequest{}, fmt.Errorf("extract additional repository: %w", err)
		}

		// There was no additional repository.
	}

	if req, ok := req.(*gitalypb.CreateForkRequest); ok {
		additionalRepo = req.GetSourceRepository()
	}

	if additionalRepo != nil && additionalRepo.StorageName != targetRepo.StorageName {
		return transactionalizedRequest{}, ErrRepositoriesInDifferentStorages
	}

	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.transactionalizeRequest", nil)

	tx, err := mgr.Begin(ctx, targetRepo.StorageName, targetRepo.RelativePath, additionalRepo.GetRelativePath(), methodInfo.Operation == protoregistry.OpAccessor)
	if err != nil {
		return transactionalizedRequest{}, fmt.Errorf("begin transaction: %w", err)
	}
	ctx = storagectx.ContextWithTransaction(ctx, tx)

	txID := txRegistry.register(tx.Transaction)
	ctx = storage.ContextWithTransactionID(ctx, txID)

	finishTX := func(handlerErr error) error {
		defer span.Finish()
		defer txRegistry.unregister(txID)

		if handlerErr != nil {
			if err := tx.Rollback(); err != nil {
				logger.WithError(err).ErrorContext(ctx, "failed rolling back transaction")
			}

			return handlerErr
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit: %w", err)
		}

		return nil
	}

	defer func() {
		if returnedErr != nil {
			returnedErr = finishTX(returnedErr)
		}
	}()

	rewrittenReq, err := rewriteRequest(tx, methodInfo, req)
	if err != nil {
		return transactionalizedRequest{}, fmt.Errorf("rewrite request: %w", err)
	}

	return transactionalizedRequest{
		ctx:               ctx,
		firstMessage:      rewrittenReq,
		finishTransaction: finishTX,
	}, nil
}

func rewritableRequest(methodInfo protoregistry.MethodInfo, req proto.Message) (proto.Message, *gitalypb.Repository, error) {
	// Clone the request in order to not rewrite the request in the earlier interceptors.
	rewrittenReq := proto.Clone(req)
	targetRepo, err := methodInfo.TargetRepo(rewrittenReq)
	if err != nil {
		return nil, nil, fmt.Errorf("extract target repository: %w", err)
	}

	return rewrittenReq, targetRepo, nil
}

func restoreSnapshotRelativePath(ctx context.Context, methodInfo protoregistry.MethodInfo, req proto.Message) (proto.Message, error) {
	// Rails sends RPCs from its access checks with quarantine applied. The quarantine paths are relative to the
	// snapshot repository of the original transaction. While the relative path of the request is the original relative
	// path of the repository before snapshotting, Rails sends the snapshot repository's path in a header. For the
	// quarantine paths to apply correctly, we must thus rewrite the request to point to the snapshot repository here.
	snapshotRelativePath := metadata.GetValue(ctx, MetadataKeySnapshotRelativePath)
	if snapshotRelativePath == "" {
		return nil, ErrQuarantineWithoutSnapshotRelativePath
	}

	rewrittenReq, targetRepo, err := rewritableRequest(methodInfo, req)
	if err != nil {
		return nil, fmt.Errorf("rewritable request: %w", err)
	}

	targetRepo.RelativePath = snapshotRelativePath

	return rewrittenReq, nil
}

func rewriteRequest(tx *finalizableTransaction, methodInfo protoregistry.MethodInfo, req proto.Message) (proto.Message, error) {
	rewrittenReq, targetRepo, err := rewritableRequest(methodInfo, req)
	if err != nil {
		return nil, fmt.Errorf("rewritable request: %w", err)
	}

	*targetRepo = *tx.RewriteRepository(targetRepo)

	if additionalRepo, err := methodInfo.AdditionalRepo(rewrittenReq); err != nil {
		if !errors.Is(err, protoregistry.ErrRepositoryFieldNotFound) {
			return nil, fmt.Errorf("extract additional repository: %w", err)
		}

		// There was no additional repository.
	} else {
		*additionalRepo = *tx.RewriteRepository(additionalRepo)
	}

	return rewrittenReq, nil
}
