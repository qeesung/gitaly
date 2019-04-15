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
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/praefect"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/labkit/tracing"
)

var (
	flagConfig = flag.String("config", "", "Location for the config.toml")
	logger     = logrus.New()

	errNoConfigFile = errors.New("the config flag must be passed")
)

func main() {
	flag.Parse()

	conf, err := configure()
	if err != nil {
		logger.Fatal(err)
	}

	listeners, err := getListeners(conf.SocketPath, conf.ListenAddr)
	if err != nil {
		logger.Fatalf("%s", err)
	}

	logger.Fatalf("%v", run(listeners, conf))
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

	return conf, nil
}

func run(listeners []net.Listener, conf config.Config) error {
	var (
		// top level server dependencies
		coordinator = praefect.NewCoordinator(logger, conf.PrimaryServer.Name)
		datastore   = praefect.NewMemoryDatastore(conf)
		repl        = praefect.NewReplMgr("default", logger, datastore, coordinator, praefect.WithWhitelist(conf.Whitelist))
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

	go func() { serverErrors <- repl.ProcessBacklog(ctx) }()

	allBackendServers := append(conf.SecondaryServers, conf.PrimaryServer)

	for _, gitaly := range allBackendServers {
		coordinator.RegisterNode(gitaly.Name, gitaly.ListenAddr)

		logger.WithField("gitaly listen addr", gitaly.ListenAddr).Info("registered gitaly node")
	}

	var err error
	select {
	case s := <-termCh:
		logger.WithField("signal", s).Warn("received signal, shutting down gracefully")
		cancel() // cancels the replicator job processing

		ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
		if shutdownErr := srv.Shutdown(ctx); shutdownErr != nil {
			logger.Warnf("error received during shutting down: %v", shutdownErr)
		}
		err = fmt.Errorf("received signal: %v", s)
	case err = <-serverErrors:
	}

	return err
}

func getListeners(socketPath, listenAddr string) ([]net.Listener, error) {
	var listeners []net.Listener

	if socketPath != "" {
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		l, err := net.Listen("unix", socketPath)
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
