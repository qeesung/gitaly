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
		[]string{"unenforced", "status"},
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
	if len(config.Config.Auth.Token) == 0 {
		countStatus("server disabled authentication").Inc()
		return ctx, nil
	}

	encodedToken, err := grpc_auth.AuthFromMD(ctx, "bearer")
	if err != nil {
		countStatus("unauthenticated").Inc()
		err = grpc.Errorf(codes.Unauthenticated, "authentication required")
		return ctx, ifEnforced(err)
	}

	token, err := base64.StdEncoding.DecodeString(encodedToken)
	if err != nil {
		countStatus("invalid").Inc()
		err = grpc.Errorf(codes.Unauthenticated, "authentication required")
		return ctx, ifEnforced(err)
	}

	if !config.Config.Auth.Token.Equal(string(token)) {
		countStatus("denied").Inc()
		err = grpc.Errorf(codes.PermissionDenied, "permission denied")
		return ctx, ifEnforced(err)
	}

	countStatus(okLabel()).Inc()
	return ctx, nil
}

func ifEnforced(err error) error {
	if config.Config.Auth.Unenforced {
		return nil
	}
	return err
}

func okLabel() string {
	if config.Config.Auth.Unenforced {
		// This special value is an extra warning sign to administrators that
		// authentication is currently not enforced.
		return "would be ok"
	}
	return "ok"
}

func countStatus(status string) prometheus.Counter {
	unenforced := "false"
	if config.Config.Auth.Unenforced {
		unenforced = "true"
	}
	return authCount.WithLabelValues(unenforced, status)
}
