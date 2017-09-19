package repository

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"

	"golang.org/x/net/context"
)

type server struct{}

// NewServer creates a new instance of a gRPC repo server
func NewServer() pb.RepositoryServiceServer {
	return &server{}
}

func (*server) CreateRepository(context.Context, *pb.CreateRepositoryRequest) (*pb.CreateRepositoryResponse, error) {
	return nil, helper.Unimplemented
}
