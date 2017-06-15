package server

import (
	"encoding/base64"

	"gitlab.com/gitlab-org/gitaly/internal/config"

	"github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var (
	authCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_authentications",
			Help: "Counts of of Gitaly request authentication attempts",
		},
		[]string{"enforced", "status"},
	)
)

func init() {
	prometheus.MustRegister(authCount)
}

func authStreamServerInterceptor() grpc.StreamServerInterceptor {
	return grpc_auth.StreamServerInterceptor(check)
}

func authUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return grpc_auth.UnaryServerInterceptor(check)
}

func check(ctx context.Context) (context.Context, error) {
	encodedToken, err := grpc_auth.AuthFromMD(ctx, "bearer")
	if err != nil {
		countStatus("unauthenticated").Inc()
		err = grpc.Errorf(codes.Unauthenticated, "authentication required")
		return ctx, ifRequired(err)
	}

	token, err := base64.StdEncoding.DecodeString(encodedToken)
	if err != nil {
		countStatus("invalid").Inc()
		err = grpc.Errorf(codes.Unauthenticated, "authentication required")
		return ctx, ifRequired(err)
	}

	if config.Config.Auth.Token.Equal(string(token)) {
		countStatus(okLabel()).Inc()
		return ctx, nil
	}

	countStatus("denied").Inc()
	err = grpc.Errorf(codes.PermissionDenied, "permission denied")
	return ctx, ifRequired(err)
}

func ifRequired(err error) error {
	if config.Config.Auth.Required {
		return err
	}
	return nil
}

func okLabel() string {
	if config.Config.Auth.Required {
		return "ok"
	}
	// This special value is an extra warning sign to administrators that
	// authentication is currently not enforced.
	return "would be ok"
}

func countStatus(status string) prometheus.Counter {
	enforced := "false"
	if config.Config.Auth.Required {
		enforced = "true"
	}
	return authCount.WithLabelValues(enforced, status)
}
