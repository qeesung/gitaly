package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/go-enry/go-license-detector/v4/licensedb"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/bootstrap"
	"gitlab.com/gitlab-org/gitaly/v15/internal/bootstrap/starter"
	"gitlab.com/gitlab-org/gitaly/v15/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v15/internal/cgroups"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/sentry"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/linguist"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/maintenance"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/server"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/env"
	glog "gitlab.com/gitlab-org/gitaly/v15/internal/log"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/limithandler"
	"gitlab.com/gitlab-org/gitaly/v15/internal/streamcache"
	"gitlab.com/gitlab-org/gitaly/v15/internal/tempdir"
	"gitlab.com/gitlab-org/gitaly/v15/internal/version"
	"gitlab.com/gitlab-org/labkit/fips"
	"gitlab.com/gitlab-org/labkit/monitoring"
	"gitlab.com/gitlab-org/labkit/tracing"
	"google.golang.org/grpc"
)

var flagVersion = flag.Bool("version", false, "Print version and exit")

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

func flagUsage() {
	fmt.Println(version.GetVersionString())
	fmt.Printf("Usage: %v [command] [options] <configfile>\n", os.Args[0])
	flag.PrintDefaults()
	fmt.Printf("\nThe commands are:\n\n\tcheck\tchecks accessability of internal Rails API\n")
}

func main() {
	// If invoked with subcommand check
	if len(os.Args) > 1 && os.Args[1] == "check" {
		execCheck()
	}

	flag.Usage = flagUsage
	flag.Parse()

	// If invoked with -version
	if *flagVersion {
		fmt.Println(version.GetVersionString())
		os.Exit(0)
	}

	if flag.NArg() != 1 || flag.Arg(0) == "" {
		flag.Usage()
		os.Exit(2)
	}

	log.Infof("Starting %s", version.GetVersionString())
	fips.Check()

	cfg, err := configure(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	log.WithError(run(cfg)).Error("shutting down")
	log.Info("Gitaly stopped")
}

func configure(configPath string) (config.Cfg, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return config.Cfg{}, fmt.Errorf("load config: config_path %q: %w", configPath, err)
	}

	glog.Configure(glog.Loggers, cfg.Logging.Format, cfg.Logging.Level)

	cgroupMgr := cgroups.NewManager(cfg.Cgroups)
	if err := cgroupMgr.Cleanup(); err != nil {
		if !errors.Is(err, cgroups.ErrProcessesExist) {
			log.WithError(err).Warn("error cleaning up cgroups")
		}
	}

	if err := cgroupMgr.Setup(); err != nil {
		return config.Cfg{}, fmt.Errorf("failed setting up cgroups: %w", err)
	}

	sentry.ConfigureSentry(version.GetVersion(), sentry.Config(cfg.Logging.Sentry))
	cfg.Prometheus.Configure()
	tracing.Initialize(tracing.WithServiceName("gitaly"))
	preloadLicenseDatabase()

	return cfg, nil
}

func preloadLicenseDatabase() {
	// the first call to `licensedb.Detect` could be too long
	// https://github.com/go-enry/go-license-detector/issues/13
	// this is why we're calling it here to preload license database
	// on server startup to avoid long initialization on gRPC
	// method handling.
	licensedb.Preload()
	log.Info("License database preloaded")
}

func run(cfg config.Cfg) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.RuntimeDir != "" {
		if err := config.PruneRuntimeDirectories(log.StandardLogger(), cfg.RuntimeDir); err != nil {
			return fmt.Errorf("prune runtime directories: %w", err)
		}
	}

	runtimeDir, err := config.SetupRuntimeDirectory(cfg, os.Getpid())
	if err != nil {
		return fmt.Errorf("setup runtime directory: %w", err)
	}
	cfg.RuntimeDir = runtimeDir

	defer func() {
		if err := os.RemoveAll(cfg.RuntimeDir); err != nil {
			log.Warn("could not clean up runtime dir")
		}
	}()

	if err := gitaly.UnpackAuxiliaryBinaries(cfg.RuntimeDir); err != nil {
		return fmt.Errorf("unpack auxiliary binaries: %w", err)
	}

	b, err := bootstrap.New(promauto.NewCounterVec(
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

	gitCmdFactory, cleanup, err := git.NewExecCommandFactory(cfg, commandFactoryOpts...)
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

	tempdir.StartCleaning(locator, cfg.Storages, time.Hour)

	prometheus.MustRegister(gitCmdFactory)

	if skipHooks {
		log.Warn("skipping GitLab API client creation since hooks are bypassed via GITALY_TESTING_NO_GIT_HOOKS")
	} else {
		gitlabClient, err := gitlab.NewHTTPClient(glog.Default(), cfg.Gitlab, cfg.TLS, cfg.Prometheus)
		if err != nil {
			return fmt.Errorf("could not create GitLab API client: %w", err)
		}
		prometheus.MustRegister(gitlabClient)

		hm := hook.NewManager(cfg, locator, gitCmdFactory, transactionManager, gitlabClient)

		hookManager = hm
	}

	conns := client.NewPoolWithOptions(
		client.WithDialer(client.HealthCheckDialer(client.DialContext)),
		client.WithDialOptions(client.FailOnNonTempDialError()...),
	)
	defer conns.Close()

	catfileCache := catfile.NewCache(cfg)
	defer catfileCache.Stop()
	prometheus.MustRegister(catfileCache)

	diskCache := cache.New(cfg, locator)
	prometheus.MustRegister(diskCache)
	if err := diskCache.StartWalkers(); err != nil {
		return fmt.Errorf("disk cache walkers: %w", err)
	}

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
	prometheus.MustRegister(concurrencyLimitHandler, rateLimitHandler)

	gitalyServerFactory := server.NewGitalyServerFactory(
		cfg,
		glog.Default(),
		registry,
		diskCache,
		[]*limithandler.LimiterMiddleware{concurrencyLimitHandler, rateLimitHandler},
	)
	defer gitalyServerFactory.Stop()

	ling, err := linguist.New(cfg, gitCmdFactory)
	if err != nil {
		return fmt.Errorf("linguist instance creation: %w", err)
	}

	git2goExecutor := git2go.NewExecutor(cfg, gitCmdFactory, locator)

	updaterWithHooks := updateref.NewUpdaterWithHooks(cfg, locator, hookManager, gitCmdFactory, catfileCache)

	rubySrv := rubyserver.New(cfg, gitCmdFactory)
	if err := rubySrv.Start(); err != nil {
		return fmt.Errorf("initialize gitaly-ruby: %v", err)
	}
	defer rubySrv.Stop()

	streamCache := streamcache.New(cfg.PackObjectsCache, glog.Default())
	concurrencyTracker := hook.NewConcurrencyTracker()
	prometheus.MustRegister(concurrencyTracker)

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
			Cfg:                           cfg,
			RubyServer:                    rubySrv,
			GitalyHookManager:             hookManager,
			TransactionManager:            transactionManager,
			StorageLocator:                locator,
			ClientPool:                    conns,
			GitCmdFactory:                 gitCmdFactory,
			Linguist:                      ling,
			CatfileCache:                  catfileCache,
			DiskCache:                     diskCache,
			PackObjectsCache:              streamCache,
			PackObjectsConcurrencyTracker: concurrencyTracker,
			Git2goExecutor:                git2goExecutor,
			UpdaterWithHooks:              updaterWithHooks,
			HousekeepingManager:           housekeepingManager,
		})
		b.RegisterStarter(starter.New(c, srv))
	}

	if addr := cfg.PrometheusListenAddr; addr != "" {
		b.RegisterStarter(func(listen bootstrap.ListenFunc, _ chan<- error, _ *prometheus.CounterVec) error {
			l, err := listen("tcp", addr)
			if err != nil {
				return err
			}

			log.WithField("address", addr).Info("starting prometheus listener")

			go func() {
				if err := monitoring.Start(
					monitoring.WithListener(l),
					monitoring.WithBuildInformation(
						version.GetVersion(),
						version.GetBuildTime()),
					monitoring.WithBuildExtraLabels(
						map[string]string{"git_version": gitVersion.String()},
					)); err != nil {
					log.WithError(err).Error("Unable to serve prometheus")
				}
			}()

			return nil
		})
	}

	for _, shard := range cfg.Storages {
		if err := storage.WriteMetadataFile(shard.Path); err != nil {
			// TODO should this be a return? https://gitlab.com/gitlab-org/gitaly/issues/1893
			log.WithError(err).Error("Unable to write gitaly metadata file")
		}
	}

	if err := b.Start(); err != nil {
		return fmt.Errorf("unable to start the bootstrap: %v", err)
	}

	shutdownWorkers, err := maintenance.StartWorkers(
		ctx,
		glog.Default(),
		maintenance.DailyOptimizationWorker(cfg, maintenance.OptimizerFunc(func(ctx context.Context, repo repository.GitRepo) error {
			return housekeepingManager.OptimizeRepository(ctx, localrepo.New(locator, gitCmdFactory, catfileCache, repo))
		})),
	)
	if err != nil {
		return fmt.Errorf("initialize auxiliary workers: %v", err)
	}
	defer shutdownWorkers()

	gracefulStopTicker := helper.NewTimerTicker(cfg.GracefulRestartTimeout.Duration())
	defer gracefulStopTicker.Stop()

	return b.Wait(gracefulStopTicker, gitalyServerFactory.GracefulStop)
}
