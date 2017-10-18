package commit

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

func (s *server) ListCommitsByOid(in *pb.ListCommitsByOidRequest, stream pb.CommitService_ListCommitsByOidServer) error {
	return helper.Unimplemented
}
