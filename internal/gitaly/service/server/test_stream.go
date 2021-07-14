package server

import (
	"context"
	"io"

	"gitlab.com/gitlab-org/gitaly/v14/internal/streamrpc"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

func (s *server) TestStream(ctx context.Context, request *gitalypb.TestStreamRequest) (*emptypb.Empty, error) {
	c, err := streamrpc.AcceptConnection(ctx)
	if err != nil {
		return nil, err
	}

	_, err = io.CopyN(c, c, request.Size)
	return nil, err
}
