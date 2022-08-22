// Command praefect provides a reverse-proxy server with high-availability
// specific features for Gitaly.
//
// Additionally, praefect has subcommands for common tasks:
//
// SQL Ping
//
// The subcommand "sql-ping" checks if the database configured in the config
// file is reachable:
//
//     praefect -config PATH_TO_CONFIG sql-ping
//
// SQL Migrate
//
// The subcommand "sql-migrate" will apply any outstanding SQL migrations.
//
//     praefect -config PATH_TO_CONFIG sql-migrate [-ignore-unknown=true|false]
//
// By default, the migration will ignore any unknown migrations that are
// not known by the Praefect binary.
//
// "-ignore-unknown=false" will disable this behavior.
//
// The subcommand "sql-migrate-status" will show which SQL migrations have
// been applied and which ones have not:
//
//     praefect -config PATH_TO_CONFIG sql-migrate-status
//
// Dial Nodes
//
// The subcommand "dial-nodes" helps diagnose connection problems to Gitaly or
// Praefect. The subcommand works by sourcing the connection information from
// the config file, and then dialing and health checking the remote nodes.
//
//     praefect -config PATH_TO_CONFIG dial-nodes
//
// Dataloss
//
// The subcommand "dataloss" identifies Gitaly nodes which are missing data from the
// previous write-enabled primary node. It does so by looking through incomplete
// replication jobs. This is useful for identifying potential data loss from a failover
// event.
//
//     praefect -config PATH_TO_CONFIG dataloss [-virtual-storage <virtual-storage>]
//
// "-virtual-storage" specifies which virtual storage to check for data loss. If not specified,
// the check is performed for every configured virtual storage.
//
// Accept Dataloss
//
// The subcommand "accept-dataloss" allows for accepting data loss in a repository to enable it for
// writing again. The current version of the repository on the authoritative storage is set to be
// the latest version and replications to other nodes are scheduled in order to bring them consistent
// with the new authoritative version.
//
//     praefect -config PATH_TO_CONFIG accept-dataloss -virtual-storage <virtual-storage> -relative-path <relative-path> -authoritative-storage <authoritative-storage>
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/bootstrap"
	"gitlab.com/gitlab-org/gitaly/v15/internal/bootstrap/starter"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/sentry"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/log"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/metrics"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/nodes/tracker"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/reconciler"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/repocleaner"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/v15/internal/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/version"
	"gitlab.com/gitlab-org/labkit/monitoring"
	"gitlab.com/gitlab-org/labkit/tracing"
)

var (
	flagConfig  = flag.String("config", "", "Location for the config.toml")
	flagVersion = flag.Bool("version", false, "Print version and exit")
	logger      = log.Default()

	errNoConfigFile = errors.New("the config flag must be passed")
)

const progname = "praefect"

func main() {
	flag.Usage = func() {
		cmds := []string{}
		for k := range subcommands {
			cmds = append(cmds, k)
		}

		printfErr("Usage of %s:\n", progname)
		flag.PrintDefaults()
		printfErr("  subcommand (optional)\n")
		printfErr("\tOne of %s\n", strings.Join(cmds, ", "))
	}
	flag.Parse()

	// If invoked with -version
	if *flagVersion {
		fmt.Println(praefect.GetVersionString())
		os.Exit(0)
	}

	conf, err := initConfig()
	if err != nil {
		printfErr("%s: configuration error: %v\n", progname, err)
		os.Exit(1)
	}

	conf.ConfigureLogger()

	if args := flag.Args(); len(args) > 0 {
		os.Exit(subCommand(conf, args[0], args[1:]))
	}

	configure(conf)

	logger.WithField("version", praefect.GetVersionString()).Info("Starting " + progname)

	starterConfigs, err := getStarterConfigs(conf)
	if err != nil {
		logger.Fatalf("%s", err)
	}

	promreg := prometheus.DefaultRegisterer
	b, err := bootstrap.New(promauto.With(promreg).NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_praefect_connections_total",
			Help: "Total number of connections to Praefect",
		},
		[]string{"type"},
	))
	if err != nil {
		logger.Fatalf("unable to create a bootstrap: %v", err)
	}

	dbPromRegistry := prometheus.NewRegistry()

	if err := run(starterConfigs, conf, b, promreg, dbPromRegistry); err != nil {
		logger.Fatalf("%v", err)
	}
}

func initConfig() (config.Config, error) {
	var conf config.Config

	if *flagConfig == "" {
		return conf, errNoConfigFile
	}

	conf, err := config.FromFile(*flagConfig)
	if err != nil {
		return conf, fmt.Errorf("error reading config file: %v", err)
	}

	if err := conf.Validate(); err != nil {
		return config.Config{}, err
	}

	if !conf.AllowLegacyElectors {
		conf.Failover.ElectionStrategy = config.ElectionStrategyPerRepository
	}

	if !conf.Failover.Enabled && conf.Failover.ElectionStrategy != "" {
		logger.WithField("election_strategy", conf.Failover.ElectionStrategy).Warn(
			"ignoring configured election strategy as failover is disabled")
	}

	return conf, nil
}

func configure(conf config.Config) {
	tracing.Initialize(tracing.WithServiceName(progname))

	if conf.PrometheusListenAddr != "" {
		conf.Prometheus.Configure()
	}

	sentry.ConfigureSentry(version.GetVersion(), conf.Sentry)
}

func run(
	cfgs []starter.Config,
	conf config.Config,
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
		logger.Infof("establishing database connection to %s:%d ...", conf.DB.Host, conf.DB.Port)
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

	transactionManager := transactions.NewManager(conf)
	sidechannelRegistry := sidechannel.NewRegistry()
	clientHandshaker := backchannel.NewClientHandshaker(logger, praefect.NewBackchannelServerFactory(logger, transaction.NewServer(transactionManager), sidechannelRegistry))
	assignmentStore := praefect.NewDisabledAssignmentStore(conf.StorageNames())
	var (
		nodeManager   nodes.Manager
		healthChecker praefect.HealthChecker
		nodeSet       praefect.NodeSet
		router        praefect.Router
		primaryGetter praefect.PrimaryGetter
	)
	if conf.Failover.ElectionStrategy == config.ElectionStrategyPerRepository {
		nodeSet, err = praefect.DialNodes(ctx, conf.VirtualStorages, protoregistry.GitalyProtoPreregistered, errTracker, clientHandshaker, sidechannelRegistry)
		if err != nil {
			return fmt.Errorf("dial nodes: %w", err)
		}
		defer nodeSet.Close()

		hm := nodes.NewHealthManager(logger, db, nodes.GeneratePraefectName(conf, logger), nodeSet.HealthClients())
		go func() {
			if err := hm.Run(ctx, helper.NewTimerTicker(time.Second)); err != nil {
				logger.WithError(err).Error("health manager exited")
			}
		}()
		healthChecker = hm

		// Wait for the first health check to complete so the Praefect doesn't start serving RPC
		// before the router is ready with the health status of the nodes.
		<-hm.Updated()

		elector := nodes.NewPerRepositoryElector(db)

		primaryGetter = elector
		assignmentStore = datastore.NewAssignmentStore(db, conf.StorageNames())

		router = praefect.NewPerRepositoryRouter(
			nodeSet.Connections(),
			elector,
			hm,
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
				hm,
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
				"Deprecated election stategy in use, migrate to repository specific primary nodes following https://docs.gitlab.com/ee/administration/gitaly/praefect.html#migrate-to-repository-specific-primary-gitaly-nodes. The other election strategies are scheduled for removal in GitLab 14.0.")
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

	logger.Infof("election strategy: %q", conf.Failover.ElectionStrategy)
	logger.Info("background started: gitaly nodes health monitoring")

	var (
		// top level server dependencies
		coordinator = praefect.NewCoordinator(
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
		srvFactory = praefect.NewServerFactory(
			conf,
			logger,
			coordinator.StreamDirector,
			nodeManager,
			transactionManager,
			queue,
			rs,
			assignmentStore,
			protoregistry.GitalyProtoPreregistered,
			nodeSet.Connections(),
			primaryGetter,
			service.AllChecks(),
		)
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

		// Eventually, database-related metrics will always be exported via a separate
		// endpoint such that it's possible to set a different scraping interval and thus to
		// reduce database load. For now though, we register the metrics twice, once for the
		// standard and once for the database-specific endpoint. This is done to ensure a
		// transitory period where deployments can be moved to the new endpoint without
		// causing breakage if they still use the old endpoint.
		dbPromRegistry.MustRegister(dbMetricCollectors...)
		if !conf.PrometheusExcludeDatabaseFromDefaultMetrics {
			promreg.MustRegister(dbMetricCollectors...)
		}
	}
	promreg.MustRegister(metricsCollectors...)

	for _, cfg := range cfgs {
		srv, err := srvFactory.Create(cfg.IsSecure())
		if err != nil {
			return fmt.Errorf("create gRPC server: %w", err)
		}
		defer srv.Stop()

		b.RegisterStarter(starter.New(cfg, srv))
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
				if err := monitoring.Start(
					monitoring.WithListener(l),
					monitoring.WithServeMux(serveMux),
					monitoring.WithBuildInformation(praefect.GetVersion(), praefect.GetBuildTime())); err != nil {
					logger.WithError(err).Errorf("Unable to start prometheus listener: %v", conf.PrometheusListenAddr)
				}
			}()

			return nil
		})
	}

	if err := b.Start(); err != nil {
		return fmt.Errorf("unable to start the bootstrap: %v", err)
	}
	for _, cfg := range cfgs {
		logger.WithFields(logrus.Fields{"schema": cfg.Name, "address": cfg.Addr}).Info("listening")
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
					RepositoriesInBatch: conf.RepositoriesCleanup.RepositoriesInBatch,
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

func initDatabase(ctx context.Context, logger *logrus.Entry, conf config.Config) (*sql.DB, func(), error) {
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
