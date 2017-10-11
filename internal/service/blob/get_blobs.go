package blob

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

func (*server) GetBlobs(*pb.GetBlobsRequest, pb.BlobService_GetBlobsServer) error {
	return helper.Unimplemented
}
