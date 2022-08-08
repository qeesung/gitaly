package praefect

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	gitalycfgauth "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/server/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/log"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/mock"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/promtest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
	correlation "gitlab.com/gitlab-org/labkit/correlation/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// BuildOptions is a set of configurations options that can be set to configure praefect service.
type BuildOptions struct {
	// WithQueue sets an implementation of the replication queue to use by praefect service.
	WithQueue datastore.ReplicationEventQueue
	// WithTxMgr sets the transaction manager to use by praefect service.
	WithTxMgr *transactions.Manager
	// WithBackends sets a callback that is triggered during initialization.
	WithBackends func([]*config.VirtualStorage) []testhelper.Cleanup
	// WithAnnotations sets a proto-registry to use by praefect service.
	WithAnnotations *protoregistry.Registry
	// WithLogger sets a logger to use by praefect service.
	WithLogger *logrus.Entry
	// WithNodeMgr sets an implementation of the node manager to use by praefect service.
	WithNodeMgr nodes.Manager
	// WithRepoStore sets an implementation of the repositories store to use by praefect service.
	WithRepoStore datastore.RepositoryStore
	// WithAssignmentStore sets an implementation of the repositories store to use by praefect service.
	WithAssignmentStore AssignmentStore
	// WithConnections sets a set of connections to gitalies.
	WithConnections Connections
	// WithPrimaryGetter sets an implementation of the primary node getter to use by praefect service.
	WithPrimaryGetter PrimaryGetter
	// WithRouter sets an implementation of the request router to use by praefect service.
	WithRouter Router
	// WithChecks sets a list of check to run when ReadinessCheck RPC is called.
	WithChecks []service.CheckFunc
}

// WithMockBackends mocks backends with a set of passed in stubs.
func WithMockBackends(t testing.TB, backends map[string]mock.SimpleServiceServer) func([]*config.VirtualStorage) []testhelper.Cleanup {
	return func(virtualStorages []*config.VirtualStorage) []testhelper.Cleanup {
		var cleanups []testhelper.Cleanup

		for _, vs := range virtualStorages {
			require.Equal(t, len(backends), len(vs.Nodes),
				"mock server count doesn't match config nodes")

			for i, node := range vs.Nodes {
				backend, ok := backends[node.Storage]
				require.True(t, ok, "missing backend server for node %s", node.Storage)

				backendAddr, cleanup := newMockDownstream(t, node.Token, backend)
				cleanups = append(cleanups, cleanup)

				node.Address = backendAddr
				vs.Nodes[i] = node
			}
		}

		return cleanups
	}
}

func defaultQueue(t testing.TB) datastore.ReplicationEventQueue {
	return datastore.NewPostgresReplicationEventQueue(testdb.New(t))
}

func defaultTxMgr(conf config.Config) *transactions.Manager {
	return transactions.NewManager(conf)
}

func defaultNodeMgr(t testing.TB, conf config.Config, rs datastore.RepositoryStore) nodes.Manager {
	nodeMgr, err := nodes.NewManager(testhelper.NewDiscardingLogEntry(t), conf, nil, rs, promtest.NewMockHistogramVec(), protoregistry.GitalyProtoPreregistered, nil, nil, nil)
	require.NoError(t, err)
	nodeMgr.Start(0, time.Hour)
	t.Cleanup(nodeMgr.Stop)
	return nodeMgr
}

func defaultRepoStore(conf config.Config) datastore.RepositoryStore {
	return datastore.MockRepositoryStore{}
}

func listenAvailPort(tb testing.TB) (net.Listener, int) {
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(tb, err)

	return listener, listener.Addr().(*net.TCPAddr).Port
}

func dialLocalPort(tb testing.TB, port int, backend bool) *grpc.ClientConn {
	opts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithUnaryInterceptor(correlation.UnaryClientCorrelationInterceptor()),
		grpc.WithStreamInterceptor(correlation.StreamClientCorrelationInterceptor()),
	}
	if backend {
		opts = append(
			opts,
			grpc.WithDefaultCallOptions(grpc.ForceCodec(proxy.NewCodec())),
		)
	}

	cc, err := client.Dial(
		fmt.Sprintf("tcp://localhost:%d", port),
		opts,
	)
	require.NoError(tb, err)

	return cc
}

func newMockDownstream(tb testing.TB, token string, m mock.SimpleServiceServer) (string, func()) {
	srv := grpc.NewServer(grpc.UnaryInterceptor(auth.UnaryServerInterceptor(gitalycfgauth.Config{Token: token})))
	mock.RegisterSimpleServiceServer(srv, m)
	healthpb.RegisterHealthServer(srv, health.NewServer())

	// client to backend service
	lis, port := listenAvailPort(tb)

	errQ := make(chan error)

	go func() {
		errQ <- srv.Serve(lis)
	}()

	cleanup := func() {
		srv.GracefulStop()
		lis.Close()

		// If the server is shutdown before Serve() is called on it
		// the Serve() calls will return the ErrServerStopped
		if err := <-errQ; err != nil && err != grpc.ErrServerStopped {
			require.NoError(tb, err)
		}
	}

	return fmt.Sprintf("tcp://localhost:%d", port), cleanup
}

type noopBackoffFactory struct{}

func (noopBackoffFactory) Create() (Backoff, BackoffReset) {
	return func() time.Duration {
		return 0
	}, func() {}
}

func startProcessBacklog(ctx context.Context, replMgr ReplMgr) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		replMgr.ProcessBacklog(ctx, noopBackoffFactory{})
	}()
	return done
}

// RunPraefectServer starts praefect service based on the passed in configuration and options.
// The caller is responsible to call returned testhelper.Cleanup in order to stop the service
// and release all acquired resources.
// The function should be used only for testing purposes and not as part of the production code.
//
//nolint:revive
func RunPraefectServer(
	t testing.TB,
	ctx context.Context,
	conf config.Config,
	opt BuildOptions,
) (*grpc.ClientConn, *grpc.Server, testhelper.Cleanup) {
	var cleanups []testhelper.Cleanup

	if opt.WithQueue == nil {
		opt.WithQueue = defaultQueue(t)
	}
	if opt.WithRepoStore == nil {
		opt.WithRepoStore = defaultRepoStore(conf)
	}
	if opt.WithTxMgr == nil {
		opt.WithTxMgr = defaultTxMgr(conf)
	}
	if opt.WithBackends != nil {
		cleanups = append(cleanups, opt.WithBackends(conf.VirtualStorages)...)
	}
	if opt.WithAnnotations == nil {
		opt.WithAnnotations = protoregistry.GitalyProtoPreregistered
	}
	if opt.WithLogger == nil {
		opt.WithLogger = log.Default()
	}
	if opt.WithNodeMgr == nil {
		opt.WithNodeMgr = defaultNodeMgr(t, conf, opt.WithRepoStore)
	}
	if opt.WithAssignmentStore == nil {
		opt.WithAssignmentStore = NewDisabledAssignmentStore(conf.StorageNames())
	}
	if opt.WithRouter == nil {
		opt.WithRouter = NewNodeManagerRouter(opt.WithNodeMgr, opt.WithRepoStore)
	}
	if opt.WithChecks == nil {
		opt.WithChecks = service.AllChecks()
	}

	coordinator := NewCoordinator(
		opt.WithQueue,
		opt.WithRepoStore,
		opt.WithRouter,
		opt.WithTxMgr,
		conf,
		opt.WithAnnotations,
	)

	// TODO: run a replmgr for EVERY virtual storage
	replmgr := NewReplMgr(
		opt.WithLogger,
		conf.StorageNames(),
		opt.WithQueue,
		opt.WithRepoStore,
		opt.WithNodeMgr,
		NodeSetFromNodeManager(opt.WithNodeMgr),
	)

	prf := NewGRPCServer(
		conf,
		opt.WithLogger,
		protoregistry.GitalyProtoPreregistered,
		coordinator.StreamDirector,
		opt.WithTxMgr,
		opt.WithRepoStore,
		opt.WithAssignmentStore,
		opt.WithConnections,
		opt.WithPrimaryGetter,
		nil,
		opt.WithChecks,
	)

	listener, port := listenAvailPort(t)

	errQ := make(chan error)
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		errQ <- prf.Serve(listener)
		close(errQ)
	}()
	replMgrDone := startProcessBacklog(ctx, replmgr)

	// dial client to praefect
	cc := dialLocalPort(t, port, false)

	cleanup := func() {
		cc.Close()

		for _, cu := range cleanups {
			cu()
		}

		prf.Stop()

		cancel()
		<-replMgrDone
		require.NoError(t, <-errQ)
	}

	return cc, prf, cleanup
}
