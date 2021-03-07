package testserver

import (
	"net"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/server"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/internal/storage"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"google.golang.org/grpc"
)

// RunGitalyServer starts gitaly server based on the provided cfg.
// Returns connection address and a cleanup function.
// It accepts addition Registrar to register all required service instead of
// calling service.RegisterAll explicitly because it creates a circular dependency
// when the function is used in on of internal/gitaly/service/... packages.
func RunGitalyServer(t *testing.T, cfg config.Cfg, rubyServer *rubyserver.Server, registrar func(srv *grpc.Server, deps *service.Dependencies), opts ...GitalyServerOpt) (string, testhelper.Cleanup) {
	t.Helper()

	var deferrer testhelper.Deferrer
	defer deferrer.Call()

	var gsd gitalyServerDeps
	for _, opt := range opts {
		gsd = opt(gsd)
	}

	deps := gsd.createDependencies(t, cfg, rubyServer)
	deferrer.Add(func() { gsd.conns.Close() })

	srv, err := server.New(cfg.TLS.CertPath != "", cfg, gsd.logger.WithField("test", t.Name()))
	require.NoError(t, err)
	deferrer.Add(func() { srv.Stop() })

	registrar(srv, deps)

	// listen on internal socket
	internalListener, err := net.Listen("unix", cfg.GitalyInternalSocketPath())
	require.NoError(t, err)
	deferrer.Add(func() { internalListener.Close() })
	go srv.Serve(internalListener)

	// listen on external socket
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName(t)
	listener, err := net.Listen("unix", serverSocketPath)
	require.NoError(t, err)
	deferrer.Add(func() { listener.Close() })
	go srv.Serve(listener)

	cleaner := deferrer.Relocate()
	return "unix://" + serverSocketPath, cleaner.Call
}

type gitalyServerDeps struct {
	logger        *logrus.Logger
	conns         *client.Pool
	locator       storage.Locator
	txMgr         transaction.Manager
	hookMgr       hook.Manager
	gitlabAPI     hook.GitlabAPI
	gitCmdFactory git.CommandFactory
}

func (gsd *gitalyServerDeps) createDependencies(t testing.TB, cfg config.Cfg, rubyServer *rubyserver.Server) *service.Dependencies {
	if gsd.logger == nil {
		gsd.logger = testhelper.DiscardTestLogger(t)
	}

	if gsd.conns == nil {
		gsd.conns = client.NewPool()
	}

	if gsd.locator == nil {
		gsd.locator = config.NewLocator(cfg)
	}

	if gsd.gitlabAPI == nil {
		gsd.gitlabAPI = hook.GitlabAPIStub
	}

	if gsd.txMgr == nil {
		gsd.txMgr = transaction.NewManager(cfg)
	}

	if gsd.hookMgr == nil {
		gsd.hookMgr = hook.NewManager(gsd.locator, gsd.txMgr, gsd.gitlabAPI, cfg)
	}

	if gsd.gitCmdFactory == nil {
		gsd.gitCmdFactory = git.NewExecCommandFactory(cfg)
	}

	return &service.Dependencies{
		Cfg:                cfg,
		RubyServer:         rubyServer,
		ClientPool:         gsd.conns,
		StorageLocator:     gsd.locator,
		TransactionManager: gsd.txMgr,
		GitalyHookManager:  gsd.hookMgr,
		GitCmdFactory:      gsd.gitCmdFactory,
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
