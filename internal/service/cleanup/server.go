package cleanup

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct{}

// NewServer creates a new instance of a grpc CleanupServer
func NewServer() gitalypb.CleanupServiceServer {
	return &server{}
}

func (s *server) CloseSession(context.Context, *gitalypb.CloseSessionRequest) (*gitalypb.CloseSessionResponse, error) {
	return nil, helper.Unimplemented
}
