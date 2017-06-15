package server

import (
	log "github.com/Sirupsen/logrus"

	"gitlab.com/gitlab-org/gitaly/internal/helper/fieldextractors"
	"gitlab.com/gitlab-org/gitaly/internal/service"
	"gitlab.com/gitlab-org/gitaly/internal/service/middleware/panichandler"
	"gitlab.com/gitlab-org/gitaly/internal/service/middleware/sentryhandler"

	"github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-prometheus"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// New returns a GRPC server with all Gitaly services and interceptors set up.
func New() *grpc.Server {
	logrusEntry := log.NewEntry(log.StandardLogger())
	grpc_logrus.ReplaceGrpcLogger(logrusEntry)

	ctxTagOpts := []grpc_ctxtags.Option{
		grpc_ctxtags.WithFieldExtractor(fieldextractors.RepositoryFieldExtractor),
	}

	server := grpc.NewServer(
		grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
			grpc_ctxtags.StreamServerInterceptor(ctxTagOpts...),
			grpc_prometheus.StreamServerInterceptor,
			grpc_logrus.StreamServerInterceptor(logrusEntry),
			sentryhandler.StreamLogHandler,
			authStreamServerInterceptor(),
			// Panic handler should remain last so that application panics will be
			// converted to errors and logged
			panichandler.StreamPanicHandler,
		)),
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			grpc_ctxtags.UnaryServerInterceptor(ctxTagOpts...),
			grpc_prometheus.UnaryServerInterceptor,
			grpc_logrus.UnaryServerInterceptor(logrusEntry),
			sentryhandler.UnaryLogHandler,
			authUnaryServerInterceptor(),
			// Panic handler should remain last so that application panics will be
			// converted to errors and logged
			panichandler.UnaryPanicHandler,
		)),
	)

	service.RegisterAll(server)
	reflection.Register(server)

	grpc_prometheus.Register(server)

	return server
}
