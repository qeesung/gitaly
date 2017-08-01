package repository

import (
	"golang.org/x/net/context"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

func (s *server) Exists(ctx context.Context, in *pb.RepositoryExistsRequest) (*pb.RepositoryExistsResponse, error) {
	path, err := helper.GetPath(in.Repository)
	if err != nil {
		return nil, err
	}

	return &pb.RepositoryExistsResponse{Exists: helper.IsGitDirectory(path)}, nil
}
