package gitaly

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/go-enry/go-license-detector/v4/licensedb"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v16"
	"gitlab.com/gitlab-org/gitaly/v16/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bootstrap"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bootstrap/starter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v16/internal/cgroups"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/sentry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/maintenance"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/server"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	internalclient "gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/limithandler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/env"
	"gitlab.com/gitlab-org/gitaly/v16/internal/limiter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/streamcache"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tempdir"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tracing"
	"gitlab.com/gitlab-org/gitaly/v16/internal/version"
	"gitlab.com/gitlab-org/labkit/fips"
	"gitlab.com/gitlab-org/labkit/monitoring"
	labkittracing "gitlab.com/gitlab-org/labkit/tracing"
	"google.golang.org/grpc"
)

func newServeCommand() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "launch the server daemon",
		Description: `Launch the Gitaly server daemon.

Example: gitaly serve gitaly.config.toml`,
		ArgsUsage:       "<configfile>",
		Action:          serveAction,
		HideHelpCommand: true,
	}
}

func loadConfig(configPath string) (config.Cfg, error) {
	cfgFile, err := os.Open(configPath)
	if err != nil {
		return config.Cfg{}, err
	}
	defer cfgFile.Close()

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return config.Cfg{}, err
	}

	if err := cfg.Validate(); err != nil {
		return config.Cfg{}, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

func serveAction(ctx *cli.Context) error {
	if ctx.NArg() != 1 || ctx.Args().First() == "" {
		cli.ShowSubcommandHelpAndExit(ctx, 2)
	}

	cfg, logger, err := configure(ctx.Args().First())
	if err != nil {
		return cli.Exit(err, 1)
	}

	if cfg.Auth.Transitioning && len(cfg.Auth.Token) > 0 {
		logger.Warn("Authentication is enabled but not enforced because transitioning=true. Gitaly will accept unauthenticated requests.")
	}

	logger.Infof("Starting %s", version.GetVersionString("Gitaly"))
	fips.Check()

	if err := run(cfg, logger); err != nil {
		return cli.Exit(fmt.Errorf("unclean Gitaly shutdown: %w", err), 1)
	}

	logger.Info("Gitaly shutdown")

	return nil
}

func configure(configPath string) (config.Cfg, logrus.FieldLogger, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return config.Cfg{}, nil, fmt.Errorf("load config: config_path %q: %w", configPath, err)
	}

	urlSanitizer := log.NewURLSanitizerHook()
	urlSanitizer.AddPossibleGrpcMethod(
		"CreateRepositoryFromURL",
		"FetchRemote",
		"UpdateRemoteMirror",
	)

	logger, err := log.Configure(os.Stdout, cfg.Logging.Format, cfg.Logging.Level, urlSanitizer)
	if err != nil {
		return config.Cfg{}, nil, fmt.Errorf("configuring logger failed: %w", err)
	}

	sentry.ConfigureSentry(logger, version.GetVersion(), sentry.Config(cfg.Logging.Sentry))
	cfg.Prometheus.Configure(logger)
	labkittracing.Initialize(labkittracing.WithServiceName("gitaly"))
	preloadLicenseDatabase(logger)

	return cfg, logger, nil
}

func preloadLicenseDatabase(logger logrus.FieldLogger) {
	// the first call to `licensedb.Detect` could be too long
	// https://github.com/go-enry/go-license-detector/issues/13
	// this is why we're calling it here to preload license database
	// on server startup to avoid long initialization on gRPC
	// method handling.
	licensedb.Preload()
	logger.Info("License database preloaded")
}

func run(cfg config.Cfg, logger logrus.FieldLogger) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bootstrapSpan, ctx := tracing.StartSpan(ctx, "gitaly-bootstrap", nil)
	defer bootstrapSpan.Finish()

	if cfg.RuntimeDir != "" {
		if err := config.PruneOldGitalyProcessDirectories(logger, cfg.RuntimeDir); err != nil {
			return fmt.Errorf("prune runtime directories: %w", err)
		}
	}

	runtimeDir, err := config.SetupRuntimeDirectory(cfg, os.Getpid())
	if err != nil {
		return fmt.Errorf("setup runtime directory: %w", err)
	}
	cfg.RuntimeDir = runtimeDir

	// When cgroups are configured, we create a directory structure each
	// time a gitaly process is spawned. Look through the hierarchy root
	// to find any cgroup directories that belong to old gitaly processes
	// and remove them.
	cgroups.PruneOldCgroups(cfg.Cgroups, logger)
	cgroupMgr := cgroups.NewManager(cfg.Cgroups, logger, os.Getpid())

	if err := cgroupMgr.Setup(); err != nil {
		return fmt.Errorf("failed setting up cgroups: %w", err)
	}

	defer func() {
		if err := cgroupMgr.Cleanup(); err != nil {
			logger.WithError(err).Warn("error cleaning up cgroups")
		}
	}()

	defer func() {
		if err := os.RemoveAll(cfg.RuntimeDir); err != nil {
			logger.Warn("could not clean up runtime dir")
		}
	}()

	if err := gitaly.UnpackAuxiliaryBinaries(cfg.RuntimeDir); err != nil {
		return fmt.Errorf("unpack auxiliary binaries: %w", err)
	}

	b, err := bootstrap.New(logger, promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_connections_total",
			Help: "Total number of connections to Gitaly",
		},
		[]string{"type"},
	))
	if err != nil {
		return fmt.Errorf("init bootstrap: %w", err)
	}

	skipHooks, _ := env.GetBool("GITALY_TESTING_NO_GIT_HOOKS", false)
	var commandFactoryOpts []git.ExecCommandFactoryOption
	if skipHooks {
		commandFactoryOpts = append(commandFactoryOpts, git.WithSkipHooks())
	}

	gitCmdFactory, cleanup, err := git.NewExecCommandFactory(cfg, logger, commandFactoryOpts...)
	if err != nil {
		return fmt.Errorf("creating Git command factory: %w", err)
	}
	defer cleanup()

	gitVersion, err := gitCmdFactory.GitVersion(ctx)
	if err != nil {
		return fmt.Errorf("git version detection: %w", err)
	}

	if !gitVersion.IsSupported() {
		return fmt.Errorf("unsupported Git version: %q", gitVersion)
	}

	registry := backchannel.NewRegistry()
	transactionManager := transaction.NewManager(cfg, registry)
	prometheus.MustRegister(transactionManager)

	housekeepingManager := housekeeping.NewManager(cfg.Prometheus, transactionManager)
	prometheus.MustRegister(housekeepingManager)

	hookManager := hook.Manager(hook.DisabledManager{})

	locator := config.NewLocator(cfg)

	repoCounter := counter.NewRepositoryCounter(cfg.Storages)
	prometheus.MustRegister(repoCounter)
	repoCounter.StartCountingRepositories(ctx, locator, logger)

	tempdir.StartCleaning(logger, locator, cfg.Storages, time.Hour)

	prometheus.MustRegister(gitCmdFactory)

	if skipHooks {
		logger.Warn("skipping GitLab API client creation since hooks are bypassed via GITALY_TESTING_NO_GIT_HOOKS")
	} else {
		gitlabClient, err := gitlab.NewHTTPClient(logger, cfg.Gitlab, cfg.TLS, cfg.Prometheus)
		if err != nil {
			return fmt.Errorf("could not create GitLab API client: %w", err)
		}
		prometheus.MustRegister(gitlabClient)

		hm := hook.NewManager(cfg, locator, gitCmdFactory, transactionManager, gitlabClient)

		hookManager = hm
	}

	conns := client.NewPoolWithOptions(
		client.WithDialer(client.HealthCheckDialer(client.DialContext)),
		client.WithDialOptions(append(
			client.FailOnNonTempDialError(),
			internalclient.UnaryInterceptor(),
			internalclient.StreamInterceptor())...,
		),
	)
	defer conns.Close()

	catfileCache := catfile.NewCache(cfg)
	defer catfileCache.Stop()
	prometheus.MustRegister(catfileCache)

	diskCache := cache.New(cfg, locator, logger)
	prometheus.MustRegister(diskCache)
	if err := diskCache.StartWalkers(); err != nil {
		return fmt.Errorf("disk cache walkers: %w", err)
	}

	//  The pack-objects limit below is static at this stage. It's always equal to the initial limit, which uses
	//  MaxConcurrency config.
	packObjectLimit := limiter.NewAdaptiveLimit("packObjects", limiter.AdaptiveSetting{
		Initial: cfg.PackObjectsLimiting.MaxConcurrency,
	})
	concurrencyLimitHandler := limithandler.New(
		cfg,
		limithandler.LimitConcurrencyByRepo,
		limithandler.WithConcurrencyLimiters,
	)

	rateLimitHandler := limithandler.New(
		cfg,
		limithandler.LimitConcurrencyByRepo,
		limithandler.WithRateLimiters(ctx),
	)

	packObjectsMonitor := limiter.NewPackObjectsConcurrencyMonitor(
		cfg.Prometheus.GRPCLatencyBuckets,
	)
	newTickerFunc := func() helper.Ticker {
		return helper.NewManualTicker()
	}
	if cfg.PackObjectsLimiting.MaxQueueWait > 0 {
		newTickerFunc = func() helper.Ticker {
			return helper.NewTimerTicker(cfg.PackObjectsLimiting.MaxQueueWait.Duration())
		}
	}
	packObjectsLimiter := limiter.NewConcurrencyLimiter(
		packObjectLimit,
		cfg.PackObjectsLimiting.MaxQueueLength,
		newTickerFunc,
		packObjectsMonitor,
	)

	prometheus.MustRegister(concurrencyLimitHandler, rateLimitHandler)
	prometheus.MustRegister(packObjectsMonitor)

	gitalyServerFactory := server.NewGitalyServerFactory(
		cfg,
		logger,
		registry,
		diskCache,
		[]*limithandler.LimiterMiddleware{concurrencyLimitHandler, rateLimitHandler},
	)
	defer gitalyServerFactory.Stop()

	updaterWithHooks := updateref.NewUpdaterWithHooks(cfg, locator, hookManager, gitCmdFactory, catfileCache)

	streamCache := streamcache.New(cfg.PackObjectsCache, logger)

	var backupSink backup.Sink
	var backupLocator backup.Locator
	if cfg.Backup.GoCloudURL != "" {
		var err error
		backupSink, err = backup.ResolveSink(ctx, cfg.Backup.GoCloudURL)
		if err != nil {
			return fmt.Errorf("resolve backup sink: %w", err)
		}
		backupLocator, err = backup.ResolveLocator(cfg.Backup.Layout, backupSink)
		if err != nil {
			return fmt.Errorf("resolve backup locator: %w", err)
		}
	}

	for _, c := range []starter.Config{
		{Name: starter.Unix, Addr: cfg.SocketPath, HandoverOnUpgrade: true},
		{Name: starter.Unix, Addr: cfg.InternalSocketPath(), HandoverOnUpgrade: false},
		{Name: starter.TCP, Addr: cfg.ListenAddr, HandoverOnUpgrade: true},
		{Name: starter.TLS, Addr: cfg.TLSListenAddr, HandoverOnUpgrade: true},
	} {
		if c.Addr == "" {
			continue
		}

		var srv *grpc.Server
		if c.HandoverOnUpgrade {
			srv, err = gitalyServerFactory.CreateExternal(c.IsSecure())
			if err != nil {
				return fmt.Errorf("create external gRPC server: %w", err)
			}
		} else {
			srv, err = gitalyServerFactory.CreateInternal()
			if err != nil {
				return fmt.Errorf("create internal gRPC server: %w", err)
			}
		}

		setup.RegisterAll(srv, &service.Dependencies{
			Cfg:                 cfg,
			GitalyHookManager:   hookManager,
			TransactionManager:  transactionManager,
			StorageLocator:      locator,
			ClientPool:          conns,
			GitCmdFactory:       gitCmdFactory,
			CatfileCache:        catfileCache,
			DiskCache:           diskCache,
			PackObjectsCache:    streamCache,
			PackObjectsLimiter:  packObjectsLimiter,
			RepositoryCounter:   repoCounter,
			UpdaterWithHooks:    updaterWithHooks,
			HousekeepingManager: housekeepingManager,
			BackupSink:          backupSink,
			BackupLocator:       backupLocator,
		})
		b.RegisterStarter(starter.New(c, srv, logger))
	}

	if addr := cfg.PrometheusListenAddr; addr != "" {
		b.RegisterStarter(func(listen bootstrap.ListenFunc, _ chan<- error, _ *prometheus.CounterVec) error {
			l, err := listen("tcp", addr)
			if err != nil {
				return err
			}

			logger.WithField("address", addr).Info("starting prometheus listener")

			go func() {
				opts := []monitoring.Option{
					monitoring.WithListener(l),
					monitoring.WithBuildExtraLabels(
						map[string]string{"git_version": gitVersion.String()},
					),
				}

				if buildInfo, ok := debug.ReadBuildInfo(); ok {
					opts = append(opts, monitoring.WithGoBuildInformation(buildInfo))
				}

				if err := monitoring.Start(opts...); err != nil {
					logger.WithError(err).Error("Unable to serve prometheus")
				}
			}()

			return nil
		})
	}

	for _, shard := range cfg.Storages {
		if err := storage.WriteMetadataFile(shard.Path); err != nil {
			// TODO should this be a return? https://gitlab.com/gitlab-org/gitaly/issues/1893
			logger.WithError(err).Error("Unable to write gitaly metadata file")
		}
	}

	if err := b.Start(); err != nil {
		return fmt.Errorf("unable to start the bootstrap: %w", err)
	}
	bootstrapSpan.Finish()

	shutdownWorkers, err := maintenance.StartWorkers(
		ctx,
		logger,
		maintenance.DailyOptimizationWorker(cfg, maintenance.OptimizerFunc(func(ctx context.Context, repo storage.Repository) error {
			return housekeepingManager.OptimizeRepository(ctx, localrepo.New(locator, gitCmdFactory, catfileCache, repo))
		})),
	)
	if err != nil {
		return fmt.Errorf("initialize auxiliary workers: %w", err)
	}
	defer shutdownWorkers()

	gracefulStopTicker := helper.NewTimerTicker(cfg.GracefulRestartTimeout.Duration())
	defer gracefulStopTicker.Stop()

	return b.Wait(gracefulStopTicker, gitalyServerFactory.GracefulStop)
}
