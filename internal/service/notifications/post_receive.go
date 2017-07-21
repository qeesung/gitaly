package notifications

import (
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"golang.org/x/net/context"
)

func (s *server) PostReceive(ctx context.Context, in *pb.PostReceiveRequest) (*pb.PostReceiveResponse, error) {
	grpc_logrus.Extract(ctx).Debug("PostReceive")

	return &pb.PostReceiveResponse{}, nil
}
