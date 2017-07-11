package commit

import (
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
)

func (*server) CommitLanguages(ctx context.Context, req *pb.CommitLanguagesRequest) (*pb.CommitLanguagesResponse, error) {
	client, err := rubyserver.CommitServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.CommitLanguages(clientCtx, req)
}
