package commit

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
)

func (s *server) ExtractCommitSignature(request *pb.ExtractCommitSignatureRequest, stream pb.CommitService_ExtractCommitSignatureServer) error {
	ctx := stream.Context()

	client, err := s.CommitServiceClient(ctx)
	if err != nil {
		return err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, request.GetRepository())
	if err != nil {
		return err
	}

	rubyStream, err := client.ExtractCommitSignature(clientCtx, request)
	if err != nil {
		return err
	}

	return rubyserver.Proxy(func() error {
		resp, err := rubyStream.Recv()
		if err != nil {
			md := rubyStream.Trailer()
			stream.SetTrailer(md)
			return err
		}
		return stream.Send(resp)
	})
}
