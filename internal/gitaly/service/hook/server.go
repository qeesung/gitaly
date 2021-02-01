package hook

import (
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	gitalyhook "gitlab.com/gitlab-org/gitaly/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct {
	cfg     config.Cfg
	manager gitalyhook.Manager
	gitalypb.UnimplementedHookServiceServer
}

// NewServer creates a new instance of a gRPC namespace server
func NewServer(cfg config.Cfg, manager gitalyhook.Manager) gitalypb.HookServiceServer {
	return &server{
		cfg:     cfg,
		manager: manager,
	}
}
