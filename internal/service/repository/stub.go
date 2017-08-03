package repository

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
)

func (*server) ApplyGitattributes(context.Context, *pb.ApplyGitattributesRequest) (*pb.ApplyGitattributesResponse, error) {
	return nil, nil
}
