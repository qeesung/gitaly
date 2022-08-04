package server

import (
	"crypto/tls"
	"fmt"
	"time"

	grpcmw "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcmwlogrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	grpcmwtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	diskcache "gitlab.com/gitlab-org/gitaly/v15/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/server/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/grpcstats"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/fieldextractors"
	"gitlab.com/gitlab-org/gitaly/v15/internal/listenmux"
	gitalylog "gitlab.com/gitlab-org/gitaly/v15/internal/log"
	"gitlab.com/gitlab-org/gitaly/v15/internal/logsanitizer"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/cache"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/cancelhandler"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/commandstatshandler"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/limithandler"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/metadatahandler"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/panichandler"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/sentryhandler"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/protoregistry"
	grpccorrelation "gitlab.com/gitlab-org/labkit/correlation/grpc"
	grpctracing "gitlab.com/gitlab-org/labkit/tracing/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

func init() {
	for _, l := range gitalylog.Loggers {
		urlSanitizer := logsanitizer.NewURLSanitizerHook()
		urlSanitizer.AddPossibleGrpcMethod(
			"CreateRepositoryFromURL",
			"FetchRemote",
			"UpdateRemoteMirror",
		)
		l.Hooks.Add(urlSanitizer)
	}

	// grpc-go gets a custom logger; it is too chatty
	grpcmwlogrus.ReplaceGrpcLogger(gitalylog.GrpcGo())
}

// New returns a GRPC server instance with a set of interceptors configured.
// If logrusEntry is nil the default logger will be used.
func New(
	secure bool,
	cfg config.Cfg,
	logrusEntry *log.Entry,
	registry *backchannel.Registry,
	cacheInvalidator diskcache.Invalidator,
	limitHandlers []*limithandler.LimiterMiddleware,
) (*grpc.Server, error) {
	ctxTagOpts := []grpcmwtags.Option{
		grpcmwtags.WithFieldExtractorForInitialReq(fieldextractors.FieldExtractor),
	}

	transportCredentials := insecure.NewCredentials()
	// If tls config is specified attempt to extract tls options and use it
	// as a grpc.ServerOption
	if secure {
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertPath, cfg.TLS.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("error reading certificate and key paths: %v", err)
		}

		transportCredentials = credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})
	}

	lm := listenmux.New(transportCredentials)
	lm.Register(backchannel.NewServerHandshaker(
		logrusEntry,
		registry,
		[]grpc.DialOption{client.UnaryInterceptor()},
	))

	logMsgProducer := grpcmwlogrus.WithMessageProducer(
		gitalylog.MessageProducer(
			gitalylog.PropagationMessageProducer(grpcmwlogrus.DefaultMessageProducer),
			commandstatshandler.FieldsProducer,
			grpcstats.FieldsProducer,
		),
	)

	streamServerInterceptors := []grpc.StreamServerInterceptor{
		grpcmwtags.StreamServerInterceptor(ctxTagOpts...),
		grpccorrelation.StreamServerCorrelationInterceptor(), // Must be above the metadata handler
		metadatahandler.StreamInterceptor,
		grpcprometheus.StreamServerInterceptor,
		commandstatshandler.StreamInterceptor,
		grpcmwlogrus.StreamServerInterceptor(logrusEntry,
			grpcmwlogrus.WithTimestampFormat(gitalylog.LogTimestampFormat),
			logMsgProducer,
			gitalylog.DeciderOption(),
		),
		gitalylog.StreamLogDataCatcherServerInterceptor(),
		sentryhandler.StreamLogHandler,
		cancelhandler.Stream, // Should be below LogHandler
		auth.StreamServerInterceptor(cfg.Auth),
	}
	unaryServerInterceptors := []grpc.UnaryServerInterceptor{
		grpcmwtags.UnaryServerInterceptor(ctxTagOpts...),
		grpccorrelation.UnaryServerCorrelationInterceptor(), // Must be above the metadata handler
		metadatahandler.UnaryInterceptor,
		grpcprometheus.UnaryServerInterceptor,
		commandstatshandler.UnaryInterceptor,
		grpcmwlogrus.UnaryServerInterceptor(logrusEntry,
			grpcmwlogrus.WithTimestampFormat(gitalylog.LogTimestampFormat),
			logMsgProducer,
			gitalylog.DeciderOption(),
		),
		gitalylog.UnaryLogDataCatcherServerInterceptor(),
		sentryhandler.UnaryLogHandler,
		cancelhandler.Unary, // Should be below LogHandler
		auth.UnaryServerInterceptor(cfg.Auth),
	}
	// Should be below auth handler to prevent v2 hmac tokens from timing out while queued
	for _, limitHandler := range limitHandlers {
		streamServerInterceptors = append(streamServerInterceptors, limitHandler.StreamInterceptor())
		unaryServerInterceptors = append(unaryServerInterceptors, limitHandler.UnaryInterceptor())
	}

	streamServerInterceptors = append(streamServerInterceptors,
		grpctracing.StreamServerTracingInterceptor(),
		cache.StreamInvalidator(cacheInvalidator, protoregistry.GitalyProtoPreregistered),
		// Panic handler should remain last so that application panics will be
		// converted to errors and logged
		panichandler.StreamPanicHandler,
	)

	unaryServerInterceptors = append(unaryServerInterceptors,
		cache.UnaryInvalidator(cacheInvalidator, protoregistry.GitalyProtoPreregistered),
		// Panic handler should remain last so that application panics will be
		// converted to errors and logged
		panichandler.UnaryPanicHandler,
	)

	opts := []grpc.ServerOption{
		grpc.StatsHandler(gitalylog.PerRPCLogHandler{
			Underlying:     &grpcstats.PayloadBytes{},
			FieldProducers: []gitalylog.FieldsProducer{grpcstats.FieldsProducer},
		}),
		grpc.Creds(lm),
		grpc.StreamInterceptor(grpcmw.ChainStreamServer(streamServerInterceptors...)),
		grpc.UnaryInterceptor(grpcmw.ChainUnaryServer(unaryServerInterceptors...)),
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

	return grpc.NewServer(opts...), nil
}
