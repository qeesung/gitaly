package objectpool

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) ReduplicateRepository(ctx context.Context, req *gitalypb.ReduplicateRepositoryRequest) (*gitalypb.ReduplicateRepositoryResponse, error) {
	if req.GetRepository() == nil {
		return nil, status.Errorf(codes.InvalidArgument, "ReduplicateRepository: no repository")
	}

	cmd, err := s.gitCmdFactory.New(ctx, req.GetRepository(), git.SubCmd{
		Name: "repack",
		Flags: []git.Option{
			git.Flag{Name: "--quiet"},
			git.Flag{Name: "-a"},
			// This can be removed as soon as we have upstreamed a
			// `repack.updateServerInfo` config option. See gitlab-org/git#105 for more
			// details.
			git.Flag{Name: "-n"},
		},
	})
	if err != nil {
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}

	return &gitalypb.ReduplicateRepositoryResponse{}, nil
}
