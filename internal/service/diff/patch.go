package diff

import (
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

func (s *server) CommitPatch(in *pb.CommitPatchRequest, stream pb.DiffService_CommitPatchServer) error {
	ctx := stream.Context()

	client, err := s.DiffServiceClient(ctx)
	if err != nil {
		return err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, in.GetRepository())
	if err != nil {
		return err
	}

	rubyStream, err := client.CommitPatch(clientCtx, in)
	if err != nil {
		return err
	}

	return rubyserver.Proxy(func() error {
		resp, err := rubyStream.Recv()
		if err != nil {
			return err
		}
		return stream.Send(resp)
	})
}
