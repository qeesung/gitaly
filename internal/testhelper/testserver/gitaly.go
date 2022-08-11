package testserver

import (
	"context"
	"net"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v15/auth"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/auth"
	gitalylog "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/log"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/linguist"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/server"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/limithandler"
	praefectconfig "gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/streamcache"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// RunGitalyServer starts gitaly server based on the provided cfg and returns a connection address.
// It accepts addition Registrar to register all required service instead of
// calling service.RegisterAll explicitly because it creates a circular dependency
// when the function is used in on of internal/gitaly/service/... packages.
func RunGitalyServer(tb testing.TB, cfg config.Cfg, rubyServer *rubyserver.Server, registrar func(srv *grpc.Server, deps *service.Dependencies), opts ...GitalyServerOpt) string {
	return StartGitalyServer(tb, cfg, rubyServer, registrar, opts...).Address()
}

// StartGitalyServer creates and runs gitaly (and praefect as a proxy) server.
func StartGitalyServer(tb testing.TB, cfg config.Cfg, rubyServer *rubyserver.Server, registrar func(srv *grpc.Server, deps *service.Dependencies), opts ...GitalyServerOpt) GitalyServer {
	gitalySrv, gitalyAddr, disablePraefect := runGitaly(tb, cfg, rubyServer, registrar, opts...)

	if !testhelper.IsPraefectEnabled() || disablePraefect {
		return GitalyServer{
			Server:   gitalySrv,
			shutdown: gitalySrv.Stop,
			address:  gitalyAddr,
		}
	}

	praefectServer := runPraefectProxy(tb, cfg, gitalyAddr)
	return GitalyServer{
		Server: gitalySrv,
		shutdown: func() {
			praefectServer.Shutdown()
			gitalySrv.Stop()
		},
		address: praefectServer.Address(),
	}
}

func runPraefectProxy(tb testing.TB, gitalyCfg config.Cfg, gitalyAddr string) PraefectServer {
	return StartPraefect(tb, praefectconfig.Config{
		SocketPath: testhelper.GetTemporaryGitalySocketFileName(tb),
		Auth: auth.Config{
			Token: gitalyCfg.Auth.Token,
		},
		DB: testdb.GetConfig(tb, testdb.New(tb).Name),
		Failover: praefectconfig.Failover{
			Enabled:          true,
			ElectionStrategy: praefectconfig.ElectionStrategyLocal,
		},
		Replication: praefectconfig.DefaultReplicationConfig(),
		Logging: gitalylog.Config{
			Format: "json",
			Level:  "info",
		},
		VirtualStorages: []*praefectconfig.VirtualStorage{
			{
				// Only single storage will be served by the Praefect instance. We
				// can't include more as it is invalid to use same address for
				// different storages.
				Name: gitalyCfg.Storages[0].Name,
				Nodes: []*praefectconfig.Node{
					{
						Storage: gitalyCfg.Storages[0].Name,
						Address: gitalyAddr,
						Token:   gitalyCfg.Auth.Token,
					},
				},
			},
		},
	})
}

// GitalyServer is a helper that carries additional info and
// functionality about gitaly (+praefect) server.
type GitalyServer struct {
	Server   *grpc.Server
	shutdown func()
	address  string
}

// Shutdown terminates running gitaly (+praefect) server.
func (gs GitalyServer) Shutdown() {
	gs.shutdown()
}

// Address returns address of the running gitaly (or praefect) service.
func (gs GitalyServer) Address() string {
	return gs.address
}

// waitHealthy waits until the server hosted at address becomes healthy.
func waitHealthy(ctx context.Context, tb testing.TB, addr string, authToken string) {
	grpcOpts := []grpc.DialOption{
		grpc.WithBlock(),
	}
	if authToken != "" {
		grpcOpts = append(grpcOpts, grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(authToken)))
	}

	conn, err := client.DialContext(ctx, addr, grpcOpts)
	require.NoError(tb, err)
	defer testhelper.MustClose(tb, conn)

	healthClient := healthpb.NewHealthClient(conn)

	resp, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{}, grpc.WaitForReady(true))
	require.NoError(tb, err)
	require.Equal(tb, healthpb.HealthCheckResponse_SERVING, resp.Status, "server not yet ready to serve")
}

func runGitaly(tb testing.TB, cfg config.Cfg, rubyServer *rubyserver.Server, registrar func(srv *grpc.Server, deps *service.Dependencies), opts ...GitalyServerOpt) (*grpc.Server, string, bool) {
	tb.Helper()

	var gsd gitalyServerDeps
	for _, opt := range opts {
		gsd = opt(gsd)
	}

	deps := gsd.createDependencies(tb, cfg, rubyServer)
	tb.Cleanup(func() { gsd.conns.Close() })

	serverFactory := server.NewGitalyServerFactory(
		cfg,
		gsd.logger.WithField("test", tb.Name()),
		deps.GetBackchannelRegistry(),
		deps.GetDiskCache(),
		[]*limithandler.LimiterMiddleware{deps.GetLimitHandler()},
	)

	if cfg.RuntimeDir != "" {
		internalServer, err := serverFactory.CreateInternal()
		require.NoError(tb, err)
		tb.Cleanup(internalServer.Stop)

		registrar(internalServer, deps)
		registerHealthServerIfNotRegistered(internalServer)

		require.NoError(tb, os.MkdirAll(cfg.InternalSocketDir(), 0o700))
		tb.Cleanup(func() { require.NoError(tb, os.RemoveAll(cfg.InternalSocketDir())) })

		internalListener, err := net.Listen("unix", cfg.InternalSocketPath())
		require.NoError(tb, err)
		go func() {
			assert.NoError(tb, internalServer.Serve(internalListener), "failure to serve internal gRPC")
		}()

		ctx := testhelper.Context(tb)
		waitHealthy(ctx, tb, "unix://"+internalListener.Addr().String(), cfg.Auth.Token)
	}

	externalServer, err := serverFactory.CreateExternal(cfg.TLS.CertPath != "" && cfg.TLS.KeyPath != "")
	require.NoError(tb, err)
	tb.Cleanup(externalServer.Stop)

	registrar(externalServer, deps)
	registerHealthServerIfNotRegistered(externalServer)

	var listener net.Listener
	var addr string
	switch {
	case cfg.TLSListenAddr != "":
		listener, err = net.Listen("tcp", cfg.TLSListenAddr)
		require.NoError(tb, err)
		_, port, err := net.SplitHostPort(listener.Addr().String())
		require.NoError(tb, err)
		addr = "tls://localhost:" + port
	case cfg.ListenAddr != "":
		listener, err = net.Listen("tcp", cfg.ListenAddr)
		require.NoError(tb, err)
		addr = "tcp://" + listener.Addr().String()
	default:
		serverSocketPath := testhelper.GetTemporaryGitalySocketFileName(tb)
		listener, err = net.Listen("unix", serverSocketPath)
		require.NoError(tb, err)
		addr = "unix://" + serverSocketPath
	}

	go func() {
		assert.NoError(tb, externalServer.Serve(listener), "failure to serve external gRPC")
	}()

	ctx := testhelper.Context(tb)
	waitHealthy(ctx, tb, addr, cfg.Auth.Token)

	return externalServer, addr, gsd.disablePraefect
}

func registerHealthServerIfNotRegistered(srv *grpc.Server) {
	if _, found := srv.GetServiceInfo()["grpc.health.v1.Health"]; !found {
		// we should register health service as it is used for the health checks
		// praefect service executes periodically (and on the bootstrap step)
		healthpb.RegisterHealthServer(srv, health.NewServer())
	}
}

type gitalyServerDeps struct {
	disablePraefect               bool
	logger                        *logrus.Logger
	conns                         *client.Pool
	locator                       storage.Locator
	txMgr                         transaction.Manager
	hookMgr                       hook.Manager
	gitlabClient                  gitlab.Client
	gitCmdFactory                 git.CommandFactory
	linguist                      *linguist.Instance
	backchannelReg                *backchannel.Registry
	catfileCache                  catfile.Cache
	diskCache                     cache.Cache
	packObjectsCache              streamcache.Cache
	packObjectsConcurrencyTracker *hook.ConcurrencyTracker
	limitHandler                  *limithandler.LimiterMiddleware
	git2goExecutor                *git2go.Executor
	updaterWithHooks              *updateref.UpdaterWithHooks
	housekeepingManager           housekeeping.Manager
}

func (gsd *gitalyServerDeps) createDependencies(tb testing.TB, cfg config.Cfg, rubyServer *rubyserver.Server) *service.Dependencies {
	if gsd.logger == nil {
		gsd.logger = testhelper.NewDiscardingLogger(tb)
	}

	if gsd.conns == nil {
		gsd.conns = client.NewPool()
	}

	if gsd.locator == nil {
		gsd.locator = config.NewLocator(cfg)
	}

	if gsd.gitlabClient == nil {
		gsd.gitlabClient = gitlab.NewMockClient(
			tb, gitlab.MockAllowed, gitlab.MockPreReceive, gitlab.MockPostReceive,
		)
	}

	if gsd.backchannelReg == nil {
		gsd.backchannelReg = backchannel.NewRegistry()
	}

	if gsd.txMgr == nil {
		gsd.txMgr = transaction.NewManager(cfg, gsd.backchannelReg)
	}

	if gsd.gitCmdFactory == nil {
		gsd.gitCmdFactory = gittest.NewCommandFactory(tb, cfg)
	}

	if gsd.hookMgr == nil {
		gsd.hookMgr = hook.NewManager(cfg, gsd.locator, gsd.gitCmdFactory, gsd.txMgr, gsd.gitlabClient)
	}

	if gsd.linguist == nil {
		var err error
		gsd.linguist, err = linguist.New(cfg, gsd.gitCmdFactory)
		require.NoError(tb, err)
	}

	if gsd.catfileCache == nil {
		cache := catfile.NewCache(cfg)
		gsd.catfileCache = cache
		tb.Cleanup(cache.Stop)
	}

	if gsd.diskCache == nil {
		gsd.diskCache = cache.New(cfg, gsd.locator)
	}

	if gsd.packObjectsCache == nil {
		gsd.packObjectsCache = streamcache.New(cfg.PackObjectsCache, gsd.logger)
		tb.Cleanup(gsd.packObjectsCache.Stop)
	}

	if gsd.packObjectsConcurrencyTracker == nil {
		gsd.packObjectsConcurrencyTracker = hook.NewConcurrencyTracker()
	}

	if gsd.limitHandler == nil {
		gsd.limitHandler = limithandler.New(cfg, limithandler.LimitConcurrencyByRepo, limithandler.WithConcurrencyLimiters)
	}

	if gsd.git2goExecutor == nil {
		gsd.git2goExecutor = git2go.NewExecutor(cfg, gsd.gitCmdFactory, gsd.locator)
	}

	if gsd.updaterWithHooks == nil {
		gsd.updaterWithHooks = updateref.NewUpdaterWithHooks(cfg, gsd.locator, gsd.hookMgr, gsd.gitCmdFactory, gsd.catfileCache)
	}

	if gsd.housekeepingManager == nil {
		gsd.housekeepingManager = housekeeping.NewManager(cfg.Prometheus, gsd.txMgr)
	}

	return &service.Dependencies{
		Cfg:                           cfg,
		RubyServer:                    rubyServer,
		ClientPool:                    gsd.conns,
		StorageLocator:                gsd.locator,
		TransactionManager:            gsd.txMgr,
		GitalyHookManager:             gsd.hookMgr,
		GitCmdFactory:                 gsd.gitCmdFactory,
		Linguist:                      gsd.linguist,
		BackchannelRegistry:           gsd.backchannelReg,
		GitlabClient:                  gsd.gitlabClient,
		CatfileCache:                  gsd.catfileCache,
		DiskCache:                     gsd.diskCache,
		PackObjectsCache:              gsd.packObjectsCache,
		PackObjectsConcurrencyTracker: gsd.packObjectsConcurrencyTracker,
		LimitHandler:                  gsd.limitHandler,
		Git2goExecutor:                gsd.git2goExecutor,
		UpdaterWithHooks:              gsd.updaterWithHooks,
		HousekeepingManager:           gsd.housekeepingManager,
	}
}

// GitalyServerOpt is a helper type to shorten declarations.
type GitalyServerOpt func(gitalyServerDeps) gitalyServerDeps

// WithLogger sets a logrus.Logger instance that will be used for gitaly services initialisation.
func WithLogger(logger *logrus.Logger) GitalyServerOpt {
	return func(deps gitalyServerDeps) gitalyServerDeps {
		deps.logger = logger
		return deps
	}
}

// WithLocator sets a storage.Locator instance that will be used for gitaly services initialisation.
func WithLocator(locator storage.Locator) GitalyServerOpt {
	return func(deps gitalyServerDeps) gitalyServerDeps {
		deps.locator = locator
		return deps
	}
}

// WithGitCommandFactory sets a git.CommandFactory instance that will be used for gitaly services
// initialisation.
func WithGitCommandFactory(gitCmdFactory git.CommandFactory) GitalyServerOpt {
	return func(deps gitalyServerDeps) gitalyServerDeps {
		deps.gitCmdFactory = gitCmdFactory
		return deps
	}
}

// WithGitLabClient sets gitlab.Client instance that will be used for gitaly services initialisation.
func WithGitLabClient(gitlabClient gitlab.Client) GitalyServerOpt {
	return func(deps gitalyServerDeps) gitalyServerDeps {
		deps.gitlabClient = gitlabClient
		return deps
	}
}

// WithHookManager sets hook.Manager instance that will be used for gitaly services initialisation.
func WithHookManager(hookMgr hook.Manager) GitalyServerOpt {
	return func(deps gitalyServerDeps) gitalyServerDeps {
		deps.hookMgr = hookMgr
		return deps
	}
}

// WithTransactionManager sets transaction.Manager instance that will be used for gitaly services initialisation.
func WithTransactionManager(txMgr transaction.Manager) GitalyServerOpt {
	return func(deps gitalyServerDeps) gitalyServerDeps {
		deps.txMgr = txMgr
		return deps
	}
}

// WithDisablePraefect disables setup and usage of the praefect as a proxy before the gitaly service.
func WithDisablePraefect() GitalyServerOpt {
	return func(deps gitalyServerDeps) gitalyServerDeps {
		deps.disablePraefect = true
		return deps
	}
}

// WithBackchannelRegistry sets backchannel.Registry instance that will be used for gitaly services initialisation.
func WithBackchannelRegistry(backchannelReg *backchannel.Registry) GitalyServerOpt {
	return func(deps gitalyServerDeps) gitalyServerDeps {
		deps.backchannelReg = backchannelReg
		return deps
	}
}

// WithDiskCache sets the cache.Cache instance that will be used for gitaly services initialisation.
func WithDiskCache(diskCache cache.Cache) GitalyServerOpt {
	return func(deps gitalyServerDeps) gitalyServerDeps {
		deps.diskCache = diskCache
		return deps
	}
}

// WithConcurrencyTracker sets the PackObjectsConcurrencyTracker that will be
// used for gitaly services initialization.
func WithConcurrencyTracker(tracker *hook.ConcurrencyTracker) GitalyServerOpt {
	return func(deps gitalyServerDeps) gitalyServerDeps {
		deps.packObjectsConcurrencyTracker = tracker
		return deps
	}
}
