package wiki

import (
	"golang.org/x/net/context"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

func (*server) WikiDeletePage(ctx context.Context, in *pb.WikiDeletePageRequest) (*pb.WikiDeletePageResponse, error) {
	return nil, helper.Unimplemented
}
