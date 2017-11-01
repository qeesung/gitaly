package auth

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

// StreamServerInterceptor checks for Gitaly bearer tokens.
func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return grpc_auth.StreamServerInterceptor(check)
}

// UnaryServerInterceptor checks for Gitaly bearer tokens.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
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
	if config.Config.Auth.Transitioning {
		return nil
	}
	return err
}

func okLabel() string {
	if config.Config.Auth.Transitioning {
		// This special value is an extra warning sign to administrators that
		// authentication is currently not enforced.
		return "would be ok"
	}
	return "ok"
}

func countStatus(status string) prometheus.Counter {
	enforced := "true"
	if config.Config.Auth.Transitioning {
		enforced = "false"
	}
	return authCount.WithLabelValues(enforced, status)
}
