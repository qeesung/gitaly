package hook

import (
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/connection"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct {
	conns       *connection.Pool
	hooksConfig config.Hooks
	gitlabAPI   GitlabAPI
}

// NewServer creates a new instance of a gRPC namespace server
func NewServer(gitlab GitlabAPI, hooksConfig config.Hooks) gitalypb.HookServiceServer {
	return &server{
		gitlabAPI:   gitlab,
		hooksConfig: hooksConfig,
		conns:       connection.NewPool(),
	}
}
