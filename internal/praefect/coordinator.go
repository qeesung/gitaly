package praefect

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/middleware/metadatahandler"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/metadata"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/metrics"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/labkit/correlation"
	"golang.org/x/sync/errgroup"
)

type ReadOnlyStorageError string

func (storage ReadOnlyStorageError) Error() string {
	return fmt.Sprintf("storage %q is in read-only mode", string(storage))
}

// getReplicationDetails determines the type of job and additional details based on the method name and incoming message
func getReplicationDetails(methodName string, m proto.Message) (datastore.ChangeType, datastore.Params, error) {
	switch methodName {
	case "/gitaly.RepositoryService/RemoveRepository":
		return datastore.DeleteRepo, nil, nil
	case "/gitaly.RepositoryService/RenameRepository":
		req, ok := m.(*gitalypb.RenameRepositoryRequest)
		if !ok {
			return "", nil, fmt.Errorf("protocol changed: for method %q expected  message type '%T', got '%T'", methodName, req, m)
		}
		return datastore.RenameRepo, datastore.Params{"RelativePath": req.RelativePath}, nil
	case "/gitaly.RepositoryService/GarbageCollect":
		req, ok := m.(*gitalypb.GarbageCollectRequest)
		if !ok {
			return "", nil, fmt.Errorf("protocol changed: for method %q expected  message type '%T', got '%T'", methodName, req, m)
		}
		return datastore.GarbageCollect, datastore.Params{"CreateBitmap": req.GetCreateBitmap()}, nil
	case "/gitaly.RepositoryService/RepackFull":
		req, ok := m.(*gitalypb.RepackFullRequest)
		if !ok {
			return "", nil, fmt.Errorf("protocol changed: for method %q expected  message type '%T', got '%T'", methodName, req, m)
		}
		return datastore.RepackFull, datastore.Params{"CreateBitmap": req.GetCreateBitmap()}, nil
	case "/gitaly.RepositoryService/RepackIncremental":
		req, ok := m.(*gitalypb.RepackIncrementalRequest)
		if !ok {
			return "", nil, fmt.Errorf("protocol changed: for method %q expected  message type '%T', got '%T'", methodName, req, m)
		}
		return datastore.RepackIncremental, nil, nil
	default:
		return datastore.UpdateRepo, nil, nil
	}
}

// grpcCall is a wrapper to assemble a set of parameters that represents an gRPC call method.
type grpcCall struct {
	fullMethodName string
	methodInfo     protoregistry.MethodInfo
	msg            proto.Message
	targetRepo     *gitalypb.Repository
}

// Coordinator takes care of directing client requests to the appropriate
// downstream server. The coordinator is thread safe; concurrent calls to
// register nodes are safe.
type Coordinator struct {
	nodeMgr  nodes.Manager
	txMgr    *transactions.Manager
	queue    datastore.ReplicationEventQueue
	rs       datastore.RepositoryStore
	registry *protoregistry.Registry
	conf     config.Config
}

// NewCoordinator returns a new Coordinator that utilizes the provided logger
func NewCoordinator(
	queue datastore.ReplicationEventQueue,
	rs datastore.RepositoryStore,
	nodeMgr nodes.Manager,
	txMgr *transactions.Manager,
	conf config.Config,
	r *protoregistry.Registry,
) *Coordinator {
	return &Coordinator{
		queue:    queue,
		rs:       rs,
		registry: r,
		nodeMgr:  nodeMgr,
		txMgr:    txMgr,
		conf:     conf,
	}
}

func (c *Coordinator) directRepositoryScopedMessage(ctx context.Context, call grpcCall) (*proxy.StreamParameters, error) {
	targetRepo, err := call.methodInfo.TargetRepo(call.msg)
	if err != nil {
		return nil, helper.ErrInvalidArgument(fmt.Errorf("repo scoped: %w", err))
	}

	ctxlogrus.AddFields(ctx, logrus.Fields{
		"virtual_storage": call.targetRepo.StorageName,
		"relative_path":   call.targetRepo.RelativePath,
	})

	if targetRepo.StorageName == "" || targetRepo.RelativePath == "" {
		return nil, helper.ErrInvalidArgumentf("repo scoped: target repo is invalid")
	}

	if ctx, err = metadata.InjectPraefectServer(ctx, c.conf); err != nil {
		return nil, fmt.Errorf("repo scoped: could not inject Praefect server: %w", err)
	}

	var ps *proxy.StreamParameters
	switch call.methodInfo.Operation {
	case protoregistry.OpAccessor:
		ps, err = c.accessorStreamParameters(ctx, call, targetRepo)
	case protoregistry.OpMutator:
		ps, err = c.mutatorStreamParameters(ctx, call, targetRepo)
	default:
		err = fmt.Errorf("unknown operation type: %v", call.methodInfo.Operation)
	}

	if err != nil {
		return nil, err
	}

	return ps, nil
}

func (c *Coordinator) accessorStreamParameters(ctx context.Context, call grpcCall, targetRepo *gitalypb.Repository) (*proxy.StreamParameters, error) {
	repoPath := targetRepo.GetRelativePath()
	virtualStorage := targetRepo.StorageName

	node, err := c.nodeMgr.GetSyncedNode(ctx, virtualStorage, repoPath)
	if err != nil {
		return nil, fmt.Errorf("accessor call: get synced: %w", err)
	}

	storage := node.GetStorage()
	b, err := rewrittenRepositoryMessage(call.methodInfo, call.msg, storage)
	if err != nil {
		return nil, fmt.Errorf("accessor call: rewrite storage: %w", err)
	}

	metrics.ReadDistribution.WithLabelValues(virtualStorage, storage).Inc()

	return proxy.NewStreamParameters(proxy.Destination{
		Ctx:  helper.IncomingToOutgoing(ctx),
		Conn: node.GetConnection(),
		Msg:  b,
	}, nil, nil, nil), nil
}

var transactionRPCs = map[string]featureflag.FeatureFlag{
	"/gitaly.OperationService/UserCreateBranch": featureflag.ReferenceTransactionsOperationService,
	"/gitaly.OperationService/UserCreateTag":    featureflag.ReferenceTransactionsOperationService,
	"/gitaly.OperationService/UserDeleteBranch": featureflag.ReferenceTransactionsOperationService,
	"/gitaly.OperationService/UserDeleteTag":    featureflag.ReferenceTransactionsOperationService,
	"/gitaly.OperationService/UserUpdateBranch": featureflag.ReferenceTransactionsOperationService,
	"/gitaly.SSHService/SSHReceivePack":         featureflag.ReferenceTransactionsSSHService,
	"/gitaly.SmartHTTPService/PostReceivePack":  featureflag.ReferenceTransactionsSmartHTTPService,
}

func shouldUseTransaction(ctx context.Context, method string) bool {
	if !featureflag.IsEnabled(ctx, featureflag.ReferenceTransactions) {
		return false
	}

	flag, ok := transactionRPCs[method]
	if !ok {
		return false
	}

	if !featureflag.IsEnabled(ctx, flag) {
		return false
	}

	return true
}

func (c *Coordinator) registerTransaction(ctx context.Context, primary nodes.Node, secondaries []nodes.Node) (*transactions.Transaction, transactions.CancelFunc, error) {
	var voters []transactions.Voter
	var threshold uint

	if featureflag.IsEnabled(ctx, featureflag.ReferenceTransactionsPrimaryWins) {
		// This voting strategy ensures that transactions always go ahead as long as
		// the primary doesn't fail because of unrelated reasons. Secondaries' votes do
		// not matter.

		voters = append(voters, transactions.Voter{
			Name:  primary.GetStorage(),
			Votes: 1,
		})
		threshold = 1

		for _, node := range secondaries {
			voters = append(voters, transactions.Voter{
				Name:  node.GetStorage(),
				Votes: 0,
			})
		}
	} else {
		// This voting strategy ensures strong consistency: all nodes will agree on the
		// same result, but any failed node will abort the transaction.
		for _, node := range append(secondaries, primary) {
			voters = append(voters, transactions.Voter{
				Name:  node.GetStorage(),
				Votes: 1,
			})
			threshold += 1
		}
	}

	return c.txMgr.RegisterTransaction(ctx, voters, threshold)
}

func (c *Coordinator) mutatorStreamParameters(ctx context.Context, call grpcCall, targetRepo *gitalypb.Repository) (*proxy.StreamParameters, error) {
	virtualStorage := targetRepo.StorageName

	shard, err := c.nodeMgr.GetShard(virtualStorage)
	if err != nil {
		return nil, fmt.Errorf("mutator call: get shard: %w", err)
	}

	if c.conf.Failover.ReadOnlyAfterFailover && shard.IsReadOnly {
		return nil, helper.ErrPreconditionFailed(ReadOnlyStorageError(call.targetRepo.GetStorageName()))
	}

	primaryMessage, err := rewrittenRepositoryMessage(call.methodInfo, call.msg, shard.Primary.GetStorage())
	if err != nil {
		return nil, fmt.Errorf("mutator call: rewrite storage: %w", err)
	}

	change, params, err := getReplicationDetails(call.fullMethodName, call.msg)
	if err != nil {
		return nil, fmt.Errorf("mutator call: replication details: %w", err)
	}

	var finalizers []func() error

	primaryDest := proxy.Destination{
		Ctx:  helper.IncomingToOutgoing(ctx),
		Conn: shard.Primary.GetConnection(),
		Msg:  primaryMessage,
	}

	var secondaryDests []proxy.Destination

	if shouldUseTransaction(ctx, call.fullMethodName) {
		// Only healthy secondaries which are consistent with the primary are allowed to take
		// part in the transaction. Unhealthy nodes would block the transaction until they come back.
		// Inconsistent nodes will anyway need repair so including them doesn't make sense. They
		// also might vote to abort which might unnecessarily fail the transaction.
		consistentSecondaries, err := c.rs.GetConsistentSecondaries(ctx, virtualStorage, targetRepo.RelativePath, shard.Primary.GetStorage())
		if err != nil {
			return nil, err
		}

		participatingSecondaries := make([]nodes.Node, 0, len(consistentSecondaries))
		for _, secondary := range shard.GetHealthySecondaries() {
			if _, ok := consistentSecondaries[secondary.GetStorage()]; ok {
				participatingSecondaries = append(participatingSecondaries, secondary)
			}
		}

		transaction, transactionCleanup, err := c.registerTransaction(ctx, shard.Primary, participatingSecondaries)
		if err != nil {
			return nil, err
		}

		injectedCtx, err := metadata.InjectTransaction(ctx, transaction.ID(), shard.Primary.GetStorage(), true)
		if err != nil {
			return nil, err
		}
		primaryDest.Ctx = helper.IncomingToOutgoing(injectedCtx)

		for _, secondary := range participatingSecondaries {
			secondaryMsg, err := rewrittenRepositoryMessage(call.methodInfo, call.msg, secondary.GetStorage())
			if err != nil {
				return nil, err
			}

			injectedCtx, err := metadata.InjectTransaction(ctx, transaction.ID(), secondary.GetStorage(), false)
			if err != nil {
				return nil, err
			}

			secondaryDests = append(secondaryDests, proxy.Destination{
				Ctx:  helper.IncomingToOutgoing(injectedCtx),
				Conn: secondary.GetConnection(),
				Msg:  secondaryMsg,
			})
		}

		finalizers = append(finalizers,
			transactionCleanup, c.createTransactionFinalizer(ctx, transaction, shard, virtualStorage, call.targetRepo, change, params),
		)
	} else {
		finalizers = append(finalizers,
			c.newRequestFinalizer(
				ctx,
				virtualStorage,
				call.targetRepo,
				shard.Primary.GetStorage(),
				nil,
				nodesToStorages(shard.Secondaries),
				change,
				params,
			))
	}

	reqFinalizer := func() error {
		var firstErr error
		for _, finalizer := range finalizers {
			err := finalizer()
			if err == nil {
				continue
			}

			if firstErr == nil {
				firstErr = err
				continue
			}

			ctxlogrus.
				Extract(ctx).
				WithError(err).
				Error("coordinator proxy stream finalizer failure")
		}
		return firstErr
	}
	return proxy.NewStreamParameters(primaryDest, secondaryDests, reqFinalizer, nil), nil
}

// streamDirector determines which downstream servers receive requests
func (c *Coordinator) StreamDirector(ctx context.Context, fullMethodName string, peeker proxy.StreamPeeker) (*proxy.StreamParameters, error) {
	// For phase 1, we need to route messages based on the storage location
	// to the appropriate Gitaly node.
	ctxlogrus.Extract(ctx).Debugf("Stream director received method %s", fullMethodName)

	mi, err := c.registry.LookupMethod(fullMethodName)
	if err != nil {
		return nil, err
	}

	payload, err := peeker.Peek()
	if err != nil {
		return nil, err
	}

	m, err := protoMessage(mi, payload)
	if err != nil {
		return nil, err
	}

	if mi.Scope == protoregistry.ScopeRepository {
		targetRepo, err := mi.TargetRepo(m)
		if err != nil {
			return nil, helper.ErrInvalidArgument(fmt.Errorf("repo scoped: %w", err))
		}

		if targetRepo.StorageName == "" || targetRepo.RelativePath == "" {
			return nil, helper.ErrInvalidArgumentf("repo scoped: target repo is invalid")
		}

		sp, err := c.directRepositoryScopedMessage(ctx, grpcCall{
			fullMethodName: fullMethodName,
			methodInfo:     mi,
			msg:            m,
			targetRepo:     targetRepo,
		},
		)
		if err != nil {
			if errors.Is(err, nodes.ErrVirtualStorageNotExist) {
				return nil, helper.ErrInvalidArgument(err)
			}
			return nil, err
		}
		return sp, nil
	}

	if mi.Scope == protoregistry.ScopeStorage {
		return c.directStorageScopedMessage(ctx, mi, m)
	}

	// TODO: please refer to https://gitlab.com/gitlab-org/gitaly/-/issues/2974
	if mi.Scope == protoregistry.ScopeServer {
		shard, err := c.nodeMgr.GetShard(c.conf.VirtualStorages[0].Name)
		if err != nil {
			if errors.Is(err, nodes.ErrVirtualStorageNotExist) {
				return nil, helper.ErrInvalidArgument(err)
			}
			return nil, err
		}

		return proxy.NewStreamParameters(proxy.Destination{
			Ctx:  helper.IncomingToOutgoing(ctx),
			Conn: shard.Primary.GetConnection(),
			Msg:  payload,
		}, nil, func() error { return nil }, nil), nil
	}

	return nil, helper.ErrInternalf("rpc with undefined scope %q", mi.Scope)
}

func (c *Coordinator) directStorageScopedMessage(ctx context.Context, mi protoregistry.MethodInfo, msg proto.Message) (*proxy.StreamParameters, error) {
	virtualStorage, err := mi.Storage(msg)
	if err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	if virtualStorage == "" {
		return nil, helper.ErrInvalidArgumentf("storage scoped: target storage is invalid")
	}

	var ps *proxy.StreamParameters
	switch mi.Operation {
	case protoregistry.OpAccessor:
		ps, err = c.accessorStorageStreamParameters(ctx, mi, msg, virtualStorage)
	case protoregistry.OpMutator:
		ps, err = c.mutatorStorageStreamParameters(ctx, mi, msg, virtualStorage)
	default:
		err = fmt.Errorf("storage scope: unknown operation type: %v", mi.Operation)
	}
	return ps, err
}

func (c *Coordinator) accessorStorageStreamParameters(ctx context.Context, mi protoregistry.MethodInfo, msg proto.Message, virtualStorage string) (*proxy.StreamParameters, error) {
	shard, err := c.nodeMgr.GetShard(virtualStorage)
	if err != nil {
		if errors.Is(err, nodes.ErrVirtualStorageNotExist) {
			return nil, helper.ErrInvalidArgument(err)
		}
		return nil, helper.ErrInternalf("accessor storage scoped: get shard %q: %w", virtualStorage, err)
	}

	primaryStorage := shard.Primary.GetStorage()

	b, err := rewrittenStorageMessage(mi, msg, primaryStorage)
	if err != nil {
		return nil, helper.ErrInvalidArgument(fmt.Errorf("accessor storage scoped: %w", err))
	}

	// As this is a read operation it could be routed to another storage (not only primary) if it meets constraints
	// such as: it is healthy, it belongs to the same virtual storage bundle, etc.
	// https://gitlab.com/gitlab-org/gitaly/-/issues/2972
	primaryDest := proxy.Destination{
		Ctx:  ctx,
		Conn: shard.Primary.GetConnection(),
		Msg:  b,
	}

	return proxy.NewStreamParameters(primaryDest, nil, func() error { return nil }, nil), nil
}

func (c *Coordinator) mutatorStorageStreamParameters(ctx context.Context, mi protoregistry.MethodInfo, msg proto.Message, virtualStorage string) (*proxy.StreamParameters, error) {
	shard, err := c.nodeMgr.GetShard(virtualStorage)
	if err != nil {
		if errors.Is(err, nodes.ErrVirtualStorageNotExist) {
			return nil, helper.ErrInvalidArgument(err)
		}
		return nil, helper.ErrInternalf("mutator storage scoped: get shard %q: %w", virtualStorage, err)
	}

	primaryStorage := shard.Primary.GetStorage()

	b, err := rewrittenStorageMessage(mi, msg, primaryStorage)
	if err != nil {
		return nil, helper.ErrInvalidArgument(fmt.Errorf("mutator storage scoped: %w", err))
	}

	primaryDest := proxy.Destination{
		Ctx:  ctx,
		Conn: shard.Primary.GetConnection(),
		Msg:  b,
	}

	secondaries := shard.GetHealthySecondaries()
	secondaryDests := make([]proxy.Destination, len(secondaries))
	for i, secondary := range secondaries {
		b, err := rewrittenStorageMessage(mi, msg, secondary.GetStorage())
		if err != nil {
			return nil, helper.ErrInvalidArgument(fmt.Errorf("mutator storage scoped: %w", err))
		}
		secondaryDests[i] = proxy.Destination{Ctx: ctx, Conn: secondary.GetConnection(), Msg: b}
	}

	return proxy.NewStreamParameters(primaryDest, secondaryDests, func() error { return nil }, nil), nil
}

func rewrittenRepositoryMessage(mi protoregistry.MethodInfo, m proto.Message, storage string) ([]byte, error) {
	targetRepo, err := mi.TargetRepo(m)
	if err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	// rewrite storage name
	targetRepo.StorageName = storage

	additionalRepo, ok, err := mi.AdditionalRepo(m)
	if err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	if ok {
		additionalRepo.StorageName = storage
	}

	b, err := proxy.NewCodec().Marshal(m)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func rewrittenStorageMessage(mi protoregistry.MethodInfo, m proto.Message, storage string) ([]byte, error) {
	if err := mi.SetStorage(m, storage); err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	return proxy.NewCodec().Marshal(m)
}

func protoMessage(mi protoregistry.MethodInfo, frame []byte) (proto.Message, error) {
	m, err := mi.UnmarshalRequestProto(frame)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func (c *Coordinator) createTransactionFinalizer(
	ctx context.Context,
	transaction *transactions.Transaction,
	shard nodes.Shard,
	virtualStorage string,
	targetRepo *gitalypb.Repository,
	change datastore.ChangeType,
	params datastore.Params,
) func() error {
	return func() error {
		successByNode := transaction.State()

		// If the primary node failed the transaction, then
		// there's no sense in trying to replicate from primary
		// to secondaries.
		if !successByNode[shard.Primary.GetStorage()] {
			return fmt.Errorf("transaction: primary failed vote")
		}
		delete(successByNode, shard.Primary.GetStorage())

		updatedSecondaries := make([]string, 0, len(successByNode))
		var outdatedSecondaries []string

		for node, success := range successByNode {
			if success {
				updatedSecondaries = append(updatedSecondaries, node)
				continue
			}

			outdatedSecondaries = append(outdatedSecondaries, node)
		}

		return c.newRequestFinalizer(
			ctx, virtualStorage, targetRepo, shard.Primary.GetStorage(),
			updatedSecondaries, outdatedSecondaries, change, params)()
	}
}

func nodesToStorages(nodes []nodes.Node) []string {
	storages := make([]string, len(nodes))
	for i, n := range nodes {
		storages[i] = n.GetStorage()
	}
	return storages
}

func (c *Coordinator) newRequestFinalizer(
	ctx context.Context,
	virtualStorage string,
	targetRepo *gitalypb.Repository,
	primary string,
	updatedSecondaries []string,
	outdatedSecondaries []string,
	change datastore.ChangeType,
	params datastore.Params,
) func() error {
	return func() error {
		switch change {
		case datastore.UpdateRepo:
			// If this fails, the primary might have changes on it that are not recorded in the database. The secondaries will appear
			// consistent with the primary but might serve different stale data. Follow-up mutator calls will solve this state although
			// the primary will be a later generation in the mean while.
			if err := c.rs.IncrementGeneration(ctx, virtualStorage, targetRepo.GetRelativePath(), primary, updatedSecondaries); err != nil {
				return fmt.Errorf("increment generation: %w", err)
			}
		case datastore.RenameRepo:
			// Renaming a repository is not idempotent on Gitaly's side. This combined with a failure here results in a problematic state,
			// where the client receives an error but can't retry the call as the repository has already been moved on the primary.
			// Ideally the rename RPC should copy the repository instead of moving so the client can retry if this failed.
			if err := c.rs.RenameRepository(ctx, virtualStorage, targetRepo.GetRelativePath(), primary, params["RelativePath"].(string)); err != nil {
				if !errors.Is(err, datastore.RepositoryNotExistsError{}) {
					return fmt.Errorf("rename repository: %w", err)
				}

				ctxlogrus.Extract(ctx).WithError(err).Info("renamed repository does not have a store entry")
			}
		case datastore.DeleteRepo:
			// If this fails, the repository was already deleted from the primary but we end up still having a record of it in the db.
			// Ideally we would delete the record from the db first and schedule the repository for deletion later in order to avoid
			// this problem. Client can reattempt this as deleting a repository is idempotent.
			if err := c.rs.DeleteRepository(ctx, virtualStorage, targetRepo.GetRelativePath(), primary); err != nil {
				if !errors.Is(err, datastore.RepositoryNotExistsError{}) {
					return fmt.Errorf("delete repository: %w", err)
				}

				ctxlogrus.Extract(ctx).WithError(err).Info("deleted repository does not have a store entry")
			}
		}

		correlationID := c.ensureCorrelationID(ctx, targetRepo)

		g, ctx := errgroup.WithContext(ctx)
		for _, secondary := range outdatedSecondaries {
			event := datastore.ReplicationEvent{
				Job: datastore.ReplicationJob{
					Change:            change,
					RelativePath:      targetRepo.GetRelativePath(),
					VirtualStorage:    virtualStorage,
					SourceNodeStorage: primary,
					TargetNodeStorage: secondary,
					Params:            params,
				},
				Meta: datastore.Params{metadatahandler.CorrelationIDKey: correlationID},
			}

			g.Go(func() error {
				if _, err := c.queue.Enqueue(ctx, event); err != nil {
					return fmt.Errorf("enqueue replication event: %w", err)
				}
				return nil
			})
		}
		return g.Wait()
	}
}

func (c *Coordinator) ensureCorrelationID(ctx context.Context, targetRepo *gitalypb.Repository) string {
	corrID := correlation.ExtractFromContext(ctx)
	if corrID == "" {
		var err error
		corrID, err = correlation.RandomID()
		if err != nil {
			corrID = generatePseudorandomCorrelationID(targetRepo)
		}
	}
	return corrID
}
