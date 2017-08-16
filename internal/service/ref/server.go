package ref

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type server struct{}

// NewServer creates a new instance of a grpc RefServer
func NewServer() pb.RefServiceServer {
	return &server{}
}

func (s *server) RefExists(ctx context.Context, in *pb.RefExistsRequest) (*pb.RefExistsResponse, error) {
	return nil, grpc.Errorf(codes.Unimplemented, "Not implemented")
}
