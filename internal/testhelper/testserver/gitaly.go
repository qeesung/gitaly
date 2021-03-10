package testserver

import (
	"errors"
	"net"
	"os"
	"path/filepath"
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
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
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

	srv, err := server.New(cfg.TLS.CertPath != "" && cfg.TLS.KeyPath != "", cfg, gsd.logger.WithField("test", t.Name()))
	require.NoError(t, err)
	deferrer.Add(func() { srv.Stop() })

	registrar(srv, deps)
	if _, found := srv.GetServiceInfo()["grpc.health.v1.Health"]; !found {
		// we should register health service as it is used for the health checks
		// praefect service executes periodically (and on the bootstrap step)
		healthpb.RegisterHealthServer(srv, health.NewServer())
	}

	// listen on internal socket
	if cfg.InternalSocketDir != "" {
		internalSocketDir := filepath.Dir(cfg.GitalyInternalSocketPath())
		sds, err := os.Stat(internalSocketDir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				require.NoError(t, os.MkdirAll(internalSocketDir, 0700))
				deferrer.Add(func() { os.RemoveAll(internalSocketDir) })
			}
			require.FailNow(t, err.Error())
		} else {
			require.True(t, sds.IsDir())
		}

		internalListener, err := net.Listen("unix", cfg.GitalyInternalSocketPath())
		require.NoError(t, err)
		deferrer.Add(func() { internalListener.Close() })
		go srv.Serve(internalListener)
	}

	var listener net.Listener
	var addr string
	switch {
	case cfg.TLSListenAddr != "":
		listener, err = net.Listen("tcp", cfg.TLSListenAddr)
		require.NoError(t, err)
		_, port, err := net.SplitHostPort(listener.Addr().String())
		require.NoError(t, err)
		addr = "tls://localhost:" + port
	case cfg.ListenAddr != "":
		listener, err = net.Listen("tcp", cfg.ListenAddr)
		require.NoError(t, err)
		addr = "tcp://" + listener.Addr().String()
	default:
		serverSocketPath := testhelper.GetTemporaryGitalySocketFileName(t)
		listener, err = net.Listen("unix", serverSocketPath)
		require.NoError(t, err)
		addr = "unix://" + serverSocketPath
	}

	deferrer.Add(func() { listener.Close() })
	go srv.Serve(listener)

	cleaner := deferrer.Relocate()
	return addr, cleaner.Call
	/*
		praefectBinPath, ok := os.LookupEnv("GITALY_TEST_PRAEFECT_BIN")
		if !ok {
			p.socket = p.listen(t)
			return
		}

		tempDir, cleanup := TempDir(t)
		defer cleanup()

		praefectServerSocketPath := GetTemporaryGitalySocketFileName(t)

		c := praefectconfig.Config{
			SocketPath: praefectServerSocketPath,
			Auth: auth.Config{
				Token: p.token,
			},
			MemoryQueueEnabled: true,
			Failover: praefectconfig.Failover{
				Enabled:           true,
				ElectionStrategy:  praefectconfig.ElectionStrategyLocal,
				BootstrapInterval: config.Duration(time.Microsecond),
				MonitorInterval:   config.Duration(time.Second),
			},
			Replication: praefectconfig.DefaultReplicationConfig(),
			Logging: gitalylog.Config{
				Format: "json",
				Level:  "panic",
			},
		}

		for _, storage := range p.storages {
			gitalyServerSocketPath := p.listen(t)

			c.VirtualStorages = append(c.VirtualStorages, &praefectconfig.VirtualStorage{
				Name: storage,
				Nodes: []*praefectconfig.Node{
					{
						Storage: storage,
						Address: "unix:/" + gitalyServerSocketPath,
						Token:   p.token,
					},
				},
			})
		}

		configFilePath := filepath.Join(tempDir, "config.toml")
		configFile, err := os.Create(configFilePath)
		require.NoError(t, err)
		defer MustClose(t, configFile)

		require.NoError(t, toml.NewEncoder(configFile).Encode(&c))
		require.NoError(t, configFile.Sync())

		cmd := exec.Command(praefectBinPath, "-config", configFilePath)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout

		p.socket = praefectServerSocketPath

		require.NoError(t, cmd.Start())

		p.waitCh = make(chan struct{})
		go func() {
			cmd.Wait()
			close(p.waitCh)
		}()

		opts := []grpc.DialOption{grpc.WithInsecure()}
		if p.token != "" {
			opts = append(opts, grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(p.token)))
		}

		conn, err := grpc.Dial("unix://"+praefectServerSocketPath, opts...)
		require.NoError(t, err)
		defer MustClose(t, conn)

		waitHealthy(t, conn, 3, time.Second)

		p.process = cmd.Process
	*/
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

// WithGitLabAPI sets hook.GitlabAPI instance that will be used for gitaly services initialisation.
func WithGitLabAPI(gitlabAPI hook.GitlabAPI) GitalyServerOpt {
	return func(deps gitalyServerDeps) gitalyServerDeps {
		deps.gitlabAPI = gitlabAPI
		return deps
	}
}
