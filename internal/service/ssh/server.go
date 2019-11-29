package ssh

import (
	"time"

	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

var (
	defaultUploadPackRequestTimeout    = 10 * time.Minute
	defaultUploadArchiveRequestTimeout = time.Minute
)

type server struct {
	uploadPackRequestTimeout    time.Duration
	uploadArchiveRequestTimeout time.Duration
	gitalypb.UnimplementedSSHServiceServer
}

// NewServer creates a new instance of a grpc SSHServer
func NewServer(serverOpts ...ServerOpt) gitalypb.SSHServiceServer {
	s := &server{
		uploadPackRequestTimeout:    defaultUploadPackRequestTimeout,
		uploadArchiveRequestTimeout: defaultUploadArchiveRequestTimeout,
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
