package storage

import (
	"gitlab.com/gitlab-org/gitaly/internal/helper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"golang.org/x/net/context"
)

func (s *server) DeleteAllRepositories(context.Context, *pb.DeleteAllRepositoriesRequest) (*pb.DeleteAllRepositoriesResponse, error) {
	return nil, helper.Unimplemented
}
