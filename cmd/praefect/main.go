package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/config/sentry"
	"gitlab.com/gitlab-org/gitaly/internal/log"
	"gitlab.com/gitlab-org/gitaly/internal/praefect"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/conn"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/internal/version"
	"gitlab.com/gitlab-org/labkit/monitoring"
	"gitlab.com/gitlab-org/labkit/tracing"
)

var (
	flagConfig  = flag.String("config", "", "Location for the config.toml")
	flagVersion = flag.Bool("version", false, "Print version and exit")
	logger      = log.Default()

	errNoConfigFile = errors.New("the config flag must be passed")
)

func main() {
	flag.Parse()

	// If invoked with -version
	if *flagVersion {
		fmt.Println(praefect.GetVersionString())
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

		go func() {
			if err := monitoring.Serve(
				monitoring.WithListenerAddress(conf.PrometheusListenAddr),
				monitoring.WithBuildInformation(praefect.GetVersion(), praefect.GetBuildTime())); err != nil {
				logger.WithError(err).Errorf("Unable to start healthcheck listener: %v", conf.PrometheusListenAddr)
			}
		}()
	}

	logger.WithField("version", praefect.GetVersionString()).Info("Starting Praefect")

	sentry.ConfigureSentry(version.GetVersion(), conf.Sentry)

	return conf, nil
}

func run(listeners []net.Listener, conf config.Config) error {
	clientConnections := conn.NewClientConnections()

	for _, node := range conf.Nodes {
		if err := clientConnections.RegisterNode(node.Storage, node.Address, node.Token); err != nil {
			return fmt.Errorf("failed to register %s: %s", node.Address, err)
		}

		logger.WithField("node_address", node.Address).Info("registered gitaly node")
	}

	var (
		// top level server dependencies
		ds          = datastore.NewInMemory(conf)
		coordinator = praefect.NewCoordinator(logger, ds, clientConnections, conf, protoregistry.GitalyProtoFileDescriptors...)
		repl        = praefect.NewReplMgr("default", logger, ds, clientConnections)
		srv         = praefect.NewServer(coordinator, repl, nil, logger, clientConnections, conf)
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
