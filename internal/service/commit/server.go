package commit

import (
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/service/ref"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

type server struct {
	*rubyserver.Server
}

var (
	defaultBranchName = ref.DefaultBranchName
	maxMsgSize        = 1024 * 128 // 128 KiB
)

// NewServer creates a new instance of a grpc CommitServiceServer
func NewServer(rs *rubyserver.Server) pb.CommitServiceServer {
	return &server{rs}
}
