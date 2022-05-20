package ssh

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

var (
	defaultUploadPackRequestTimeout    = 10 * time.Minute
	defaultUploadArchiveRequestTimeout = time.Minute
)

type server struct {
	gitalypb.UnimplementedSSHServiceServer
	locator                     storage.Locator
	gitCmdFactory               git.CommandFactory
	txManager                   transaction.Manager
	uploadPackRequestTimeout    time.Duration
	uploadArchiveRequestTimeout time.Duration
	packfileNegotiationMetrics  *prometheus.CounterVec
}

// NewServer creates a new instance of a grpc SSHServer
func NewServer(
	locator storage.Locator,
	gitCmdFactory git.CommandFactory,
	txManager transaction.Manager,
	serverOpts ...ServerOpt,
) gitalypb.SSHServiceServer {
	s := &server{
		locator:                     locator,
		gitCmdFactory:               gitCmdFactory,
		txManager:                   txManager,
		uploadPackRequestTimeout:    defaultUploadPackRequestTimeout,
		uploadArchiveRequestTimeout: defaultUploadArchiveRequestTimeout,
		packfileNegotiationMetrics: prometheus.NewCounterVec(
			prometheus.CounterOpts{},
			[]string{"git_negotiation_feature"},
		),
	}

	for _, serverOpt := range serverOpts {
		serverOpt(s)
	}

	return s
}

// ServerOpt is a self referential option for server
type ServerOpt func(s *server)

// WithUploadPackRequestTimeout sets the upload pack request timeout
func WithUploadPackRequestTimeout(d time.Duration) ServerOpt {
	return func(s *server) {
		s.uploadPackRequestTimeout = d
	}
}

// WithArchiveRequestTimeout sets the upload pack request timeout
func WithArchiveRequestTimeout(d time.Duration) ServerOpt {
	return func(s *server) {
		s.uploadArchiveRequestTimeout = d
	}
}

//nolint: revive,stylecheck // This is unintentionally missing documentation.
func WithPackfileNegotiationMetrics(c *prometheus.CounterVec) ServerOpt {
	return func(s *server) {
		s.packfileNegotiationMetrics = c
	}
}
