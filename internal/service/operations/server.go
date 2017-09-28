package operations

import (
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"golang.org/x/net/context"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

type server struct {
	*rubyserver.Server
}

// NewServer creates a new instance of a grpc OperationServiceServer
func NewServer(rs *rubyserver.Server) pb.OperationServiceServer {
	return &server{rs}
}

// UserDeleteBranch is a stub
func (s *server) UserDeleteBranch(ctx context.Context, req *pb.UserDeleteBranchRequest) (*pb.UserDeleteBranchResponse, error) {
	return nil, nil
}
