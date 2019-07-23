package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"gitlab.com/gitlab-org/gitaly/internal/praefect"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore/database"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/internal/version"
	"gitlab.com/gitlab-org/labkit/tracing"
)

var (
	flagConfig  = flag.String("config", "", "Location for the config.toml")
	flagVersion = flag.Bool("version", false, "Print version and exit")
	logger      = logrus.New()

	errNoConfigFile = errors.New("the config flag must be passed")
)

func main() {
	flag.Parse()

	// If invoked with -version
	if *flagVersion {
		fmt.Println(version.GetVersionString())
		os.Exit(0)
	}

	conf, err := configure()
	if err != nil {
		logger.Fatal(err)
	}

	listeners, err := getListeners(conf.SocketPath, conf.ListenAddr)
	if err != nil {
		logger.Fatalf("%s", err)
	}

	if err := run(listeners, conf); err != nil {
		logger.Fatalf("%v", err)
	}
}

func configure() (config.Config, error) {
	var conf config.Config

	if *flagConfig == "" {
		return conf, errNoConfigFile
	}

	conf, err := config.FromFile(*flagConfig)
	if err != nil {
		return conf, fmt.Errorf("error reading config file: %v", err)
	}

	if err := conf.Validate(); err != nil {
		return conf, err
	}

	logger = conf.ConfigureLogger()
	tracing.Initialize(tracing.WithServiceName("praefect"))

	if conf.PrometheusListenAddr != "" {
		logger.WithField("address", conf.PrometheusListenAddr).Info("Starting prometheus listener")
		promMux := http.NewServeMux()
		promMux.Handle("/metrics", promhttp.Handler())

		go func() {
			http.ListenAndServe(conf.PrometheusListenAddr, promMux)
		}()
	}

	registerServerVersionPromGauge()
	logger.WithField("version", praefect.GetVersionString()).Info("Starting Praefect")

	return conf, nil
}

func run(listeners []net.Listener, conf config.Config) error {

	sqlDatastore, err := database.NewSQLDatastore(
		os.Getenv("PRAEFECT_PG_USER"),
		os.Getenv("PRAEFECT_PG_PASSWORD"),
		os.Getenv("PRAEFECT_PG_ADDRESS"),
		os.Getenv("PRAEFECT_PG_DATABASE"),
		os.Getenv("PRAEFECT_PG_NOTIFY_CHANNEL"),
	)

	if err != nil {
		return fmt.Errorf("failed to create sql datastore: %v", err)
	}

	cachedDatastore := datastore.NewCachedDatastore(sqlDatastore, datastore.WithBustOnUpdate(sqlDatastore.Listener()))

	var (
		// top level server dependencies
		datastore   = datastore.NewMemoryDatastore()
		coordinator = praefect.NewCoordinator(logger, cachedDatastore, protoregistry.GitalyProtoFileDescriptors...)
		repl        = praefect.NewReplMgr("default", logger, cachedDatastore, datastore, coordinator, praefect.WithWhitelist(conf.Whitelist))
		srv         = praefect.NewServer(coordinator, repl, nil, logger)
		// signal related
		signals      = []os.Signal{syscall.SIGTERM, syscall.SIGINT}
		termCh       = make(chan os.Signal, len(signals))
		serverErrors = make(chan error, 1)
	)

	signal.Notify(termCh, signals...)

	for _, l := range listeners {
		go func(lis net.Listener) { serverErrors <- srv.Start(lis) }(l)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeStorages, err := sqlDatastore.GetNodeStorages()
	if err != nil {
		return fmt.Errorf("failed to get storage nodes from database: %v", err)
	}

	addresses := make(map[string]struct{})
	for _, nodeStorage := range nodeStorages {
		if _, ok := addresses[nodeStorage.Address]; ok {
			continue
		}
		if err := coordinator.RegisterNode(nodeStorage.Address); err != nil {
			return fmt.Errorf("failed to register %s: %s", nodeStorage.Address, err)
		}

		addresses[nodeStorage.Address] = struct{}{}

		logger.WithField("node_address", nodeStorage.Address).Info("registered gitaly node")
	}

	go func() { serverErrors <- repl.ProcessBacklog(ctx) }()

	go coordinator.FailoverRotation()

	select {
	case s := <-termCh:
		logger.WithField("signal", s).Warn("received signal, shutting down gracefully")
		cancel() // cancels the replicator job processing

		ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
		if shutdownErr := srv.Shutdown(ctx); shutdownErr != nil {
			logger.Warnf("error received during shutting down: %v", shutdownErr)
			return shutdownErr
		}
	case err := <-serverErrors:
		return err
	}

	return nil
}

func getListeners(socketPath, listenAddr string) ([]net.Listener, error) {
	var listeners []net.Listener

	if socketPath != "" {
		if err := os.RemoveAll(socketPath); err != nil {
			return nil, err
		}

		cleanPath := strings.TrimPrefix(socketPath, "unix:")
		l, err := net.Listen("unix", cleanPath)
		if err != nil {
			return nil, err
		}

		listeners = append(listeners, l)

		logger.WithField("address", socketPath).Info("listening on unix socket")
	}

	if listenAddr != "" {
		l, err := net.Listen("tcp", listenAddr)
		if err != nil {
			return nil, err
		}

		listeners = append(listeners, l)
		logger.WithField("address", listenAddr).Info("listening at tcp address")
	}

	return listeners, nil
}

// registerServerVersionPromGauge registers a label with the current server version
// making it easy to see what versions of Gitaly are running across a cluster
func registerServerVersionPromGauge() {
	gitlabBuildInfoGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "gitlab_build_info",
		Help: "Current build info for this GitLab Service",
		ConstLabels: prometheus.Labels{
			"version": praefect.GetVersion(),
			"built":   praefect.GetBuildTime(),
		},
	})

	prometheus.MustRegister(gitlabBuildInfoGauge)
	gitlabBuildInfoGauge.Set(1)
}
