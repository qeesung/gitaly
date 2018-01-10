package commit

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

func (s *server) ExtractCommitSignature(req *pb.ExtractCommitSignatureRequest, stream pb.CommitService_ExtractCommitSignatureServer) error {
	return helper.Unimplemented
}
