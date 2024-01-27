package server

import (
	"crypto/tls"
	"fmt"
	"time"

	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/server/auth"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/grpcstats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/listenmux"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/cache"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/customfieldshandler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/panichandler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/requestinfohandler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/sentryhandler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/statushandler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	gitalylog "gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	grpccorrelation "gitlab.com/gitlab-org/labkit/correlation/grpc"
	grpctracing "gitlab.com/gitlab-org/labkit/tracing/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type serverConfig struct {
	unaryInterceptors  []grpc.UnaryServerInterceptor
	streamInterceptors []grpc.StreamServerInterceptor
}

// Option is an option that can be passed to `New()`.
type Option func(*serverConfig)

// WithUnaryInterceptor adds another interceptor that shall be executed for unary RPC calls.
func WithUnaryInterceptor(interceptor grpc.UnaryServerInterceptor) Option {
	return func(cfg *serverConfig) {
		cfg.unaryInterceptors = append(cfg.unaryInterceptors, interceptor)
	}
}

// WithStreamInterceptor adds another interceptor that shall be executed for streaming RPC calls.
func WithStreamInterceptor(interceptor grpc.StreamServerInterceptor) Option {
	return func(cfg *serverConfig) {
		cfg.streamInterceptors = append(cfg.streamInterceptors, interceptor)
	}
}

// New returns a GRPC server instance with a set of interceptors configured.
func (s *GitalyServerFactory) New(external, secure bool, opts ...Option) (*grpc.Server, error) {
	var cfg serverConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	transportCredentials := insecure.NewCredentials()
	// If tls config is specified attempt to extract tls options and use it
	// as a grpc.ServerOption
	if secure {
		cert, err := s.cfg.TLS.Certificate()
		if err != nil {
			return nil, fmt.Errorf("error reading certificate and key paths: %w", err)
		}

		// The Go language maintains a list of cipher suites that do not have known security issues.
		// This list of cipher suites should be used instead of the default list.
		var secureCiphers []uint16
		for _, cipher := range tls.CipherSuites() {
			secureCiphers = append(secureCiphers, cipher.ID)
		}

		transportCredentials = credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
			CipherSuites: secureCiphers,
		})
	}

	lm := listenmux.New(transportCredentials)
	lm.Register(backchannel.NewServerHandshaker(
		s.logger,
		s.registry,
		[]grpc.DialOption{client.UnaryInterceptor()},
	))

	loggerFunc := gitalylog.PropagationMessageProducer(gitalylog.DefaultInterceptorLogger(s.logger))

	streamServerInterceptors := []grpc.StreamServerInterceptor{
		grpccorrelation.StreamServerCorrelationInterceptor(), // Must be above the metadata handler
		requestinfohandler.StreamInterceptor,
		grpcprometheus.StreamServerInterceptor,
		customfieldshandler.StreamInterceptor,
		s.logger.WithField("component", "gitaly.StreamServerInterceptor").StreamServerInterceptor(
			loggerFunc,
			gitalylog.WithMatcher(gitalylog.DeciderMatcher()),
			gitalylog.WithTimestampFormat(gitalylog.LogTimestampFormat),
			gitalylog.WithFiledProducers(
				customfieldshandler.FieldsProducer,
				grpcstats.FieldsProducer,
				featureflag.FieldsProducer,
				structerr.FieldsProducer,
			),
		),
		gitalylog.StreamLogDataCatcherServerInterceptor(),
		sentryhandler.StreamLogHandler(),
		statushandler.Stream, // Should be below LogHandler
		auth.StreamServerInterceptor(s.cfg.Auth),
	}
	unaryServerInterceptors := []grpc.UnaryServerInterceptor{
		grpccorrelation.UnaryServerCorrelationInterceptor(), // Must be above the metadata handler
		requestinfohandler.UnaryInterceptor,
		grpcprometheus.UnaryServerInterceptor,
		customfieldshandler.UnaryInterceptor,
		s.logger.WithField("component", "gitaly.StreamServerInterceptor").UnaryServerInterceptor(
			loggerFunc,
			gitalylog.WithMatcher(gitalylog.DeciderMatcher()),
			gitalylog.WithTimestampFormat(gitalylog.LogTimestampFormat),
			gitalylog.WithFiledProducers(
				customfieldshandler.FieldsProducer,
				grpcstats.FieldsProducer,
				featureflag.FieldsProducer,
				structerr.FieldsProducer,
			),
		),
		gitalylog.UnaryLogDataCatcherServerInterceptor(),
		sentryhandler.UnaryLogHandler(),
		statushandler.Unary, // Should be below LogHandler
		auth.UnaryServerInterceptor(s.cfg.Auth),
	}
	// Should be below auth handler to prevent v2 hmac tokens from timing out while queued
	for _, limitHandler := range s.limitHandlers {
		streamServerInterceptors = append(streamServerInterceptors, limitHandler.StreamInterceptor())
		unaryServerInterceptors = append(unaryServerInterceptors, limitHandler.UnaryInterceptor())
	}

	streamServerInterceptors = append(streamServerInterceptors,
		grpctracing.StreamServerTracingInterceptor(),
		cache.StreamInvalidator(s.cacheInvalidator, protoregistry.GitalyProtoPreregistered, s.logger),
		// Panic handler should remain last so that application panics will be
		// converted to errors and logged
		panichandler.StreamPanicHandler(s.logger),
	)

	unaryServerInterceptors = append(unaryServerInterceptors,
		grpctracing.UnaryServerTracingInterceptor(),
		cache.UnaryInvalidator(s.cacheInvalidator, protoregistry.GitalyProtoPreregistered, s.logger),
		// Panic handler should remain last so that application panics will be
		// converted to errors and logged
		panichandler.UnaryPanicHandler(s.logger),
	)

	streamServerInterceptors = append(streamServerInterceptors, cfg.streamInterceptors...)
	unaryServerInterceptors = append(unaryServerInterceptors, cfg.unaryInterceptors...)

	// Only requests coming through the external API need to be ran transactionalized. Only the HookService calls
	// should arrive through the internal socket. Requests coming from there would already be running in a
	// transaction as the external request that led to the internal socket call would have been transactionalized
	// already.
	if external {
		if s.txMiddleware.UnaryInterceptor != nil {
			unaryServerInterceptors = append(unaryServerInterceptors, s.txMiddleware.UnaryInterceptor)
		}
		if s.txMiddleware.StreamInterceptor != nil {
			streamServerInterceptors = append(streamServerInterceptors, s.txMiddleware.StreamInterceptor)
		}
	}

	serverOptions := []grpc.ServerOption{
		grpc.StatsHandler(gitalylog.PerRPCLogHandler{
			Underlying:     &grpcstats.PayloadBytes{},
			FieldProducers: []gitalylog.FieldsProducer{grpcstats.FieldsProducer},
		}),
		grpc.Creds(lm),
		grpc.ChainStreamInterceptor(streamServerInterceptors...),
		grpc.ChainUnaryInterceptor(unaryServerInterceptors...),
		// We deliberately set the server MinTime to significantly less than the client interval of 20
		// seconds to allow for network jitter. We can afford to be forgiving as the maximum number of
		// concurrent clients for a Gitaly server is typically in the hundreds and this volume of
		// keepalives won't add significant load.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time: 5 * time.Minute,
		}),
	}

	return grpc.NewServer(serverOptions...), nil
}
