package main

import (
	"fmt"
	"net"
	"net/http"
	"os"

	log "github.com/sirupsen/logrus"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/connectioncounter"
	"gitlab.com/gitlab-org/gitaly/internal/service"
	"gitlab.com/gitlab-org/gitaly/internal/service/middleware/loghandler"
	"gitlab.com/gitlab-org/gitaly/internal/service/middleware/panichandler"

	"github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/mwitkow/go-grpc-middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var version string

func loadConfig() {
	switch {
	case len(os.Args) >= 2:
		cfgFile, err := os.Open(os.Args[1])
		if err != nil {
			log.WithFields(log.Fields{
				"filename": os.Args[1],
				"error":    err,
			}).Warn("can not open file for reading")
			break
		}
		defer cfgFile.Close()
		if err = config.Load(cfgFile); err != nil {
			log.WithFields(log.Fields{
				"filename": os.Args[1],
				"error":    err,
			}).Warn("can not load configuration")
		}

	default:
		log.Warn("no configuration file given")
		if err := config.Load(nil); err != nil {
			log.WithError(err).Warn("can not load configuration")
		}
	}
}

func validateConfig() error {
	if config.Config.SocketPath == "" && config.Config.ListenAddr == "" {
		return fmt.Errorf("Must set $GITALY_SOCKET_PATH or $GITALY_LISTEN_ADDR")
	}

	return config.ValidateStorages()
}

func main() {
	log.WithField("version", version).Info("Starting Gitaly")

	loadConfig()

	if err := validateConfig(); err != nil {
		log.Fatal(err)
	}

	config.ConfigureLogging()

	var listeners []net.Listener

	if socketPath := config.Config.SocketPath; socketPath != "" {
		l, err := createUnixListener(socketPath)
		if err != nil {
			log.WithError(err).Fatal("configure unix listener")
		}
		log.WithField("address", socketPath).Info("listening on unix socket")
		listeners = append(listeners, l)
	}

	if addr := config.Config.ListenAddr; addr != "" {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			log.WithError(err).Fatal("configure tcp listener")
		}

		log.WithField("address", addr).Info("listening at tcp address")
		listeners = append(listeners, connectioncounter.New("tcp", l))
	}

	server := grpc.NewServer(
		grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
			panichandler.StreamPanicHandler,         // Panic Handler first: handle panics gracefully
			grpc_prometheus.StreamServerInterceptor, // Prometheus Metrics next: measure RPC times
			loghandler.StreamLogHandler,
		)),
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			panichandler.UnaryPanicHandler,         // Panic Handler first: handle panics gracefully
			grpc_prometheus.UnaryServerInterceptor, // Prometheus Metrics next: measure RPC times
			loghandler.UnaryLogHandler,
		)),
	)

	service.RegisterAll(server)
	reflection.Register(server)

	// After all your registrations, make sure all of the Prometheus metrics are initialized.
	grpc_prometheus.Register(server)

	serverError := make(chan error, len(listeners))
	for _, listener := range listeners {
		// Must pass the listener as a function argument because there is a race
		// between 'go' and 'for'.
		go func(l net.Listener) {
			serverError <- server.Serve(l)
		}(listener)
	}

	if config.Config.PrometheusListenAddr != "" {
		log.WithField("address", config.Config.PrometheusListenAddr).Info("Starting prometheus listener")
		promMux := http.NewServeMux()
		promMux.Handle("/metrics", promhttp.Handler())
		go func() {
			http.ListenAndServe(config.Config.PrometheusListenAddr, promMux)
		}()
	}

	log.Fatal(<-serverError)
}

func createUnixListener(socketPath string) (net.Listener, error) {
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.Listen("unix", socketPath)
	return connectioncounter.New("unix", l), err
}
