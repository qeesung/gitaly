package notifications

import (
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"golang.org/x/net/context"
)

func (s *server) PostReceive(ctx context.Context, in *pb.PostReceiveRequest) (*pb.PostReceiveResponse, error) {
	_, err := helper.GetRepoPath(in.GetRepository())
	if err != nil {
		return nil, err
	}

	grpc_logrus.Extract(ctx).Debug("PostReceive")

	return &pb.PostReceiveResponse{}, nil
}
