package hook

import (
	"sync"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
)

type server struct {
	mutex            sync.RWMutex
	praefectConnPool map[string]*grpc.ClientConn
	hooksConfig      config.Hooks
	gitlabAPI        GitlabAPI
}

// NewServer creates a new instance of a gRPC namespace server
func NewServer(gitlab GitlabAPI, hooksConfig config.Hooks) gitalypb.HookServiceServer {
	return &server{
		gitlabAPI:        gitlab,
		hooksConfig:      hooksConfig,
		praefectConnPool: make(map[string]*grpc.ClientConn),
	}
}
