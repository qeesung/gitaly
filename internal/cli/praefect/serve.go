package praefect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bootstrap"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bootstrap/starter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/sentry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/metrics"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/nodes/tracker"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/reconciler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/repocleaner"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/service/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/v16/internal/version"
	"gitlab.com/gitlab-org/labkit/monitoring"
	"gitlab.com/gitlab-org/labkit/tracing"
)

func newServeCommand() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "launch the server daemon",
		Description: `Launch the Praefect server daemon.

Example: praefect --config praefect.config.toml serve`,
		Action:          serveAction,
		HideHelpCommand: true,
		Before: func(context *cli.Context) error {
			if context.Args().Present() {
				return unexpectedPositionalArgsError{Command: context.Command.Name}
			}
			return nil
		},
	}
}

func serveAction(ctx *cli.Context) error {
	if ctx.Args().Present() {
		return unexpectedPositionalArgsError{Command: ctx.Command.Name}
	}

	// The ctx.Command.Name can't be used here because if `praefect -config FILE` is used
	// it will be set to 'praefect' instead of 'serve'.
	configPath := mustProvideConfigFlag(ctx, "serve")

	conf, err := readConfig(configPath)
	if err != nil {
		return err
	}

	logger, err := log.Configure(os.Stdout, conf.Logging.Format, conf.Logging.Level)
	if err != nil {
		return fmt.Errorf("configuring logger: %w", err)
	}

	logger.WithField("version", version.GetVersion()).Info("Starting Praefect")

	if !conf.Failover.Enabled && conf.Failover.ElectionStrategy != "" {
		logger.WithField("election_strategy", conf.Failover.ElectionStrategy).Warn(
			"ignoring configured election strategy as failover is disabled")
	}

	if err := run(conf, ctx.App.Name, logger); err != nil {
		logger.WithError(err).Error("Praefect shutdown")
		return cli.Exit("", 1)
	}

	logger.Info("Praefect shutdown")

	return nil
}

func run(conf config.Config, appName string, logger log.Logger) error {
	configure(logger, appName, conf)

	starterConfigs, err := getStarterConfigs(conf)
	if err != nil {
		return cli.Exit(err, 1)
	}

	promreg := prometheus.DefaultRegisterer
	b, err := bootstrap.New(logger, promauto.With(promreg).NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_praefect_connections_total",
			Help: "Total number of connections to Praefect",
		},
		[]string{"type"},
	))
	if err != nil {
		return cli.Exit(fmt.Errorf("unable to create a bootstrap: %w", err), 1)
	}

	dbPromRegistry := prometheus.NewRegistry()

	if err := server(starterConfigs, conf, logger, b, promreg, dbPromRegistry); err != nil {
		return cli.Exit(err, 1)
	}

	return nil
}

func readConfig(path string) (config.Config, error) {
	conf, err := config.FromFile(path)
	if err != nil {
		return conf, fmt.Errorf("error reading config file: %w", err)
	}

	if err := conf.Validate(); err != nil {
		return config.Config{}, fmt.Errorf("validating config: %w", err)
	}

	if !conf.AllowLegacyElectors {
		conf.Failover.ElectionStrategy = config.ElectionStrategyPerRepository
	}

	return conf, nil
}

func configure(logger log.Logger, appName string, conf config.Config) {
	tracing.Initialize(tracing.WithServiceName(appName))

	if conf.PrometheusListenAddr != "" {
		conf.Prometheus.Configure(logger)
	}

	sentry.ConfigureSentry(logger, version.GetVersion(), conf.Sentry)
}

func server(
	cfgs []starter.Config,
	conf config.Config,
	logger log.Logger,
	b bootstrap.Listener,
	promreg prometheus.Registerer,
	dbPromRegistry interface {
		prometheus.Registerer
		prometheus.Gatherer
	},
) error {
	nodeLatencyHistogram, err := metrics.RegisterNodeLatency(conf.Prometheus, promreg)
	if err != nil {
		return err
	}

	delayMetric, err := metrics.RegisterReplicationDelay(conf.Prometheus, promreg)
	if err != nil {
		return err
	}

	latencyMetric, err := metrics.RegisterReplicationLatency(conf.Prometheus, promreg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var db *sql.DB
	if conf.NeedsSQL() {
		logger.WithField("database_address", fmt.Sprintf("%s:%d", conf.DB.Host, conf.DB.Port)).Info("establishing database connection")
		dbConn, closedb, err := initDatabase(ctx, logger, conf)
		if err != nil {
			return err
		}
		defer closedb()
		db = dbConn
		logger.Info("database connection established")
	}

	var queue datastore.ReplicationEventQueue
	var rs datastore.RepositoryStore
	var csg datastore.ConsistentStoragesGetter
	var metricsCollectors []prometheus.Collector

	if conf.MemoryQueueEnabled {
		queue = datastore.NewMemoryReplicationEventQueue(conf)
		rs = datastore.MockRepositoryStore{}
		csg = rs
		logger.Info("reads distribution caching is disabled for in memory storage")
	} else {
		queue = datastore.NewPostgresReplicationEventQueue(db)
		rs = datastore.NewPostgresRepositoryStore(db, conf.StorageNames())

		dsn := glsql.DSN(conf.DB, true)
		if dsn == "" {
			csg = rs
			logger.Info("reads distribution caching is disabled because direct connection to Postgres is not set")
		} else {
			storagesCached, err := datastore.NewCachingConsistentStoragesGetter(logger, rs, conf.VirtualStorageNames())
			if err != nil {
				return fmt.Errorf("caching storage provider: %w", err)
			}

			resilientListenerTicker := helper.NewTimerTicker(5 * time.Second)
			notificationsListener := datastore.NewResilientListener(conf.DB, resilientListenerTicker, logger)
			go func() {
				err := notificationsListener.Listen(ctx, storagesCached, datastore.StorageRepositoriesUpdatesChannel, datastore.RepositoriesUpdatesChannel)
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.WithError(err).Error("notifications listener terminated")
				}
			}()

			metricsCollectors = append(metricsCollectors, storagesCached, notificationsListener)
			csg = storagesCached
			logger.Info("reads distribution caching is enabled by configuration")
		}
	}

	var errTracker tracker.ErrorTracker

	if conf.Failover.Enabled {
		thresholdsConfigured, err := conf.Failover.ErrorThresholdsConfigured()
		if err != nil {
			return err
		}

		if thresholdsConfigured {
			errorWindowFunction, err := tracker.NewErrorWindowFunction(conf.Failover)
			if err != nil {
				return err
			}

			errTracker, err = tracker.NewErrors(ctx, errorWindowFunction, conf.Failover.ReadErrorThresholdCount, conf.Failover.WriteErrorThresholdCount)
			if err != nil {
				return err
			}
		}
	}

	transactionManager := transactions.NewManager(conf, logger)
	sidechannelRegistry := sidechannel.NewRegistry()

	backchannelCfg := backchannel.DefaultConfiguration()
	backchannelCfg.AcceptBacklog = int(conf.Yamux.AcceptBacklog)
	backchannelCfg.MaximumStreamWindowSizeBytes = conf.Yamux.MaximumStreamWindowSizeBytes
	clientHandshaker := backchannel.NewClientHandshaker(
		logger,
		praefect.NewBackchannelServerFactory(logger, transaction.NewServer(transactionManager), sidechannelRegistry),
		backchannelCfg,
	)

	assignmentStore := praefect.NewDisabledAssignmentStore(conf.StorageNames())
	var (
		nodeManager   nodes.Manager
		healthChecker praefect.HealthChecker
		nodeSet       praefect.NodeSet
		router        praefect.Router
		primaryGetter praefect.PrimaryGetter
	)
	if conf.Failover.ElectionStrategy == config.ElectionStrategyPerRepository {
		nodeSet, err = praefect.DialNodes(
			ctx,
			conf.VirtualStorages,
			protoregistry.GitalyProtoPreregistered,
			errTracker,
			clientHandshaker,
			sidechannelRegistry,
			logger,
		)
		if err != nil {
			return fmt.Errorf("dial nodes: %w", err)
		}
		defer nodeSet.Close()

		healthManager := nodes.NewHealthManager(logger, db, nodes.GeneratePraefectName(conf, logger), nodeSet.HealthClients())
		go func() {
			if err := healthManager.Run(ctx, helper.NewTimerTicker(time.Second)); err != nil {
				logger.WithError(err).Error("health manager exited")
			}
		}()

		healthChecker = healthManager

		// Wait for the first health check to complete so the Praefect doesn't start serving RPC
		// before the router is ready with the health status of the nodes.
		<-healthManager.Updated()

		elector := nodes.NewPerRepositoryElector(logger, db)

		primaryGetter = elector
		assignmentStore = datastore.NewAssignmentStore(db, conf.StorageNames())

		router = praefect.NewPerRepositoryRouter(
			nodeSet.Connections(),
			elector,
			healthManager,
			praefect.NewLockedRandom(rand.New(rand.NewSource(time.Now().UnixNano()))),
			csg,
			assignmentStore,
			rs,
			conf.DefaultReplicationFactors(),
		)

		if conf.BackgroundVerification.VerificationInterval > 0 {
			logger.WithField("config", conf.BackgroundVerification).Info("background verifier started")
			verifier := praefect.NewMetadataVerifier(
				logger,
				db,
				nodeSet.Connections(),
				healthManager,
				conf.BackgroundVerification.VerificationInterval.Duration(),
				conf.BackgroundVerification.DeleteInvalidRecords,
			)
			promreg.MustRegister(verifier)

			go func() {
				if err := verifier.Run(ctx, helper.NewTimerTicker(2*time.Second)); err != nil {
					logger.WithError(err).Error("metadata verifier finished")
				}
			}()

			go func() {
				if err := verifier.RunExpiredLeaseReleaser(ctx, helper.NewTimerTicker(10*time.Second)); err != nil {
					logger.WithError(err).Error("expired verification lease releaser finished")
				}
			}()
		} else {
			logger.Info("background verifier is disabled")
		}
	} else {
		if conf.Failover.Enabled {
			logger.WithField("election_strategy", conf.Failover.ElectionStrategy).Warn(
				"Deprecated election strategy in use, migrate to repository specific primary nodes following https://docs.gitlab.com/ee/administration/gitaly/praefect.html#migrate-to-repository-specific-primary-gitaly-nodes. The other election strategies are scheduled for removal in GitLab 14.0.")
		}

		nodeMgr, err := nodes.NewManager(logger, conf, db, csg, nodeLatencyHistogram, protoregistry.GitalyProtoPreregistered, errTracker, clientHandshaker, sidechannelRegistry)
		if err != nil {
			return err
		}

		healthChecker = praefect.HealthChecker(nodeMgr)
		nodeSet = praefect.NodeSetFromNodeManager(nodeMgr)
		router = praefect.NewNodeManagerRouter(nodeMgr, rs)
		primaryGetter = nodeMgr
		nodeManager = nodeMgr

		nodeMgr.Start(conf.Failover.BootstrapInterval.Duration(), conf.Failover.MonitorInterval.Duration())
		defer nodeMgr.Stop()
	}

	logger.WithField("election_strategy", conf.Failover.ElectionStrategy).Info("election strategy")
	logger.Info("background started: gitaly nodes health monitoring")

	var (
		// top level server dependencies
		coordinator = praefect.NewCoordinator(
			logger,
			queue,
			rs,
			router,
			transactionManager,
			conf,
			protoregistry.GitalyProtoPreregistered,
		)

		repl = praefect.NewReplMgr(
			logger,
			conf.StorageNames(),
			queue,
			rs,
			healthChecker,
			nodeSet,
			praefect.WithDelayMetric(delayMetric),
			praefect.WithLatencyMetric(latencyMetric),
			praefect.WithDequeueBatchSize(conf.Replication.BatchSize),
			praefect.WithParallelStorageProcessingWorkers(conf.Replication.ParallelStorageProcessingWorkers),
		)
		srvFactory = praefect.NewServerFactory(&praefect.Dependencies{
			Config:          conf,
			Logger:          logger,
			Coordinator:     coordinator,
			Director:        coordinator.StreamDirector,
			NodeMgr:         nodeManager,
			TxMgr:           transactionManager,
			Queue:           queue,
			RepositoryStore: rs,
			AssignmentStore: assignmentStore,
			Router:          router,
			Registry:        protoregistry.GitalyProtoPreregistered,
			Conns:           nodeSet.Connections(),
			PrimaryGetter:   primaryGetter,
			Checks:          service.ReadinessChecks(),
		}, defaultServerOptions...)
	)
	metricsCollectors = append(metricsCollectors, transactionManager, coordinator, repl)
	if db != nil {
		dbMetricCollectors := []prometheus.Collector{
			datastore.NewRepositoryStoreCollector(logger, conf.VirtualStorageNames(), db, conf.Prometheus.ScrapeTimeout.Duration()),
			datastore.NewQueueDepthCollector(logger, db, conf.Prometheus.ScrapeTimeout.Duration()),
		}

		if conf.BackgroundVerification.VerificationInterval > 0 {
			dbMetricCollectors = append(dbMetricCollectors, datastore.NewVerificationQueueDepthCollector(
				logger,
				db,
				conf.Prometheus.ScrapeTimeout.Duration(),
				conf.BackgroundVerification.VerificationInterval.Duration(),
				conf.StorageNames(),
			))
		}

		// Database-related metrics are exported via a separate endpoint such that it's possible
		// to set a different scraping interval and thus to reduce database load.
		dbPromRegistry.MustRegister(dbMetricCollectors...)
	}
	promreg.MustRegister(metricsCollectors...)

	for _, cfg := range cfgs {
		srv, err := srvFactory.Create(cfg.IsSecure())
		if err != nil {
			return fmt.Errorf("create gRPC server: %w", err)
		}
		defer srv.Stop()

		b.RegisterStarter(starter.New(cfg, srv, logger))
	}

	if conf.PrometheusListenAddr != "" {
		logger.WithField("address", conf.PrometheusListenAddr).Info("Starting prometheus listener")

		b.RegisterStarter(func(listen bootstrap.ListenFunc, _ chan<- error, _ *prometheus.CounterVec) error {
			l, err := listen(starter.TCP, conf.PrometheusListenAddr)
			if err != nil {
				return err
			}

			serveMux := http.NewServeMux()
			serveMux.Handle("/db_metrics", promhttp.HandlerFor(dbPromRegistry, promhttp.HandlerOpts{}))

			go func() {
				opts := []monitoring.Option{
					monitoring.WithListener(l),
					monitoring.WithServeMux(serveMux),
				}

				if buildInfo, ok := debug.ReadBuildInfo(); ok {
					opts = append(opts, monitoring.WithGoBuildInformation(buildInfo))
				}

				if err := monitoring.Start(opts...); err != nil {
					logger.WithError(err).WithField("listen_address", conf.PrometheusListenAddr).Error("unable to start prometheus listener")
				}
			}()

			return nil
		})
	}

	if err := b.Start(); err != nil {
		return fmt.Errorf("unable to start the bootstrap: %w", err)
	}
	for _, cfg := range cfgs {
		logger.WithFields(log.Fields{"schema": cfg.Name, "address": cfg.Addr}).Info("listening")
	}

	go repl.ProcessBacklog(ctx, praefect.ExpBackoffFactory{Start: time.Second, Max: 5 * time.Second})

	staleTicker := helper.NewTimerTicker(30 * time.Second)
	defer staleTicker.Stop()

	logger.Info("background started: processing of the replication events")
	repl.ProcessStale(ctx, staleTicker, time.Minute)
	logger.Info("background started: processing of the stale replication events")

	if interval := conf.Reconciliation.SchedulingInterval.Duration(); interval > 0 {
		if conf.MemoryQueueEnabled {
			logger.Warn("Disabled automatic reconciliation as it is only implemented using SQL queue and in-memory queue is configured.")
		} else {
			r := reconciler.NewReconciler(
				logger,
				db,
				healthChecker,
				conf.StorageNames(),
				conf.Reconciliation.HistogramBuckets,
			)
			promreg.MustRegister(r)
			go func() {
				if err := r.Run(ctx, helper.NewTimerTicker(interval)); err != nil {
					logger.WithError(err).Error("reconciler finished execution")
				}
			}()
		}
	}

	if interval := conf.RepositoriesCleanup.RunInterval.Duration(); interval > 0 {
		if db != nil {
			go func() {
				storageSync := datastore.NewStorageCleanup(db)
				cfg := repocleaner.Cfg{
					RunInterval:         conf.RepositoriesCleanup.RunInterval.Duration(),
					LivenessInterval:    30 * time.Second,
					RepositoriesInBatch: int(conf.RepositoriesCleanup.RepositoriesInBatch),
				}
				repoCleaner := repocleaner.NewRunner(cfg, logger, healthChecker, nodeSet.Connections(), storageSync, storageSync, repocleaner.NewLogWarnAction(logger))
				if err := repoCleaner.Run(ctx, helper.NewTimerTicker(conf.RepositoriesCleanup.CheckInterval.Duration())); err != nil && !errors.Is(context.Canceled, err) {
					logger.WithError(err).Error("repository cleaner finished execution")
				} else {
					logger.Info("repository cleaner finished execution")
				}
			}()
		} else {
			logger.Warn("Repository cleanup background task disabled as there is no database connection configured.")
		}
	} else {
		logger.Warn(`Repository cleanup background task disabled as "repositories_cleanup.run_interval" is not set or 0.`)
	}

	gracefulStopTicker := helper.NewTimerTicker(conf.GracefulStopTimeout.Duration())
	defer gracefulStopTicker.Stop()

	return b.Wait(gracefulStopTicker, srvFactory.GracefulStop)
}

func getStarterConfigs(conf config.Config) ([]starter.Config, error) {
	var cfgs []starter.Config
	unique := map[string]struct{}{}
	for schema, addr := range map[string]string{
		starter.TCP:  conf.ListenAddr,
		starter.TLS:  conf.TLSListenAddr,
		starter.Unix: conf.SocketPath,
	} {
		if addr == "" {
			continue
		}

		addrConf, err := starter.ParseEndpoint(addr)
		if err != nil {
			// address doesn't include schema
			if !errors.Is(err, starter.ErrEmptySchema) {
				return nil, err
			}
			addrConf = starter.Config{Name: schema, Addr: addr}
		}
		addrConf.HandoverOnUpgrade = true

		if _, found := unique[addrConf.Addr]; found {
			return nil, fmt.Errorf("same address can't be used for different schemas %q", addr)
		}
		unique[addrConf.Addr] = struct{}{}

		cfgs = append(cfgs, addrConf)
	}

	if len(cfgs) == 0 {
		return nil, errors.New("no listening addresses were provided, unable to start")
	}

	return cfgs, nil
}

func initDatabase(ctx context.Context, logger log.Logger, conf config.Config) (*sql.DB, func(), error) {
	openDBCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := glsql.OpenDB(openDBCtx, conf.DB)
	if err != nil {
		logger.WithError(err).Error("SQL connection open failed")
		return nil, nil, err
	}

	closedb := func() {
		if err := db.Close(); err != nil {
			logger.WithError(err).Error("SQL connection close failed")
		}
	}

	if err := datastore.CheckPostgresVersion(db); err != nil {
		closedb()
		return nil, nil, err
	}

	return db, closedb, nil
}
