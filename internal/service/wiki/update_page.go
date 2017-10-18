package wiki

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

func (s *server) WikiUpdatePage(stream pb.WikiService_WikiUpdatePageServer) error {
	return helper.Unimplemented
}
