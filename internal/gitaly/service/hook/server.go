package hook

import (
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/streamcache"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedHookServiceServer
	manager            gitalyhook.Manager
	gitCmdFactory      git.CommandFactory
	packObjectsCache   streamcache.Cache
	concurrencyTracker *gitalyhook.ConcurrencyTracker
}

// NewServer creates a new instance of a gRPC namespace server
func NewServer(
	manager gitalyhook.Manager,
	gitCmdFactory git.CommandFactory,
	packObjectsCache streamcache.Cache,
	concurrencyTracker *gitalyhook.ConcurrencyTracker,
) gitalypb.HookServiceServer {
	srv := &server{
		manager:            manager,
		gitCmdFactory:      gitCmdFactory,
		packObjectsCache:   packObjectsCache,
		concurrencyTracker: concurrencyTracker,
	}

	return srv
}
