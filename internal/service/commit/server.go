package commit

import (
	"gitlab.com/gitlab-org/gitaly/internal/service/ref"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

type server struct{}

var defaultBranchName = ref.DefaultBranchName

// NewServer creates a new instance of a grpc CommitServiceServer
func NewServer() pb.CommitServiceServer {
	return &server{}
}

func (s *server) FindCommits(in *pb.FindCommitsRequest, stream pb.CommitService_FindCommitsServer) error {
	return nil
}
