package internalgitaly

import (
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/proto"
)

type server struct {
	storages []config.Storage
}

func NewServer(storages []config.Storage) proto.InternalGitalyServer {
	return &server{storages: storages}
}
