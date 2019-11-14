package info

import (
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// ListRepositories returns a list of repositories that includes the checksum of the primary as well as the replicas
func (s *Server) ListRepositories(in *gitalypb.ListRepositoriesRequest, stream gitalypb.InfoService_ListRepositoriesServer) error {
	return helper.Unimplemented
}
