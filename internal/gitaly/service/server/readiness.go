package server

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

// ReadinessCheck is a stub that does nothing but exists to support single interface for gitaly
// and praefect. The praefect service requires this method.
func (s *server) ReadinessCheck(context.Context, *gitalypb.ReadinessCheckRequest) (*gitalypb.ReadinessCheckResponse, error) {
	return &gitalypb.ReadinessCheckResponse{Result: &gitalypb.ReadinessCheckResponse_OkResponse{}}, nil
}
