package operations

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) UserRebase(ctx context.Context, req *pb.UserRebaseRequest) (*pb.UserRebaseResponse, error) {
	if err := validateUserRebaseRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "UserRebase: %v", err)
	}

	client, err := s.OperationServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.UserRebase(clientCtx, req)
}

func validateUserRebaseRequest(req *pb.UserRebaseRequest) error {
	if req.GetRepository() == nil {
		return fmt.Errorf("empty Repository")
	}

	if req.GetUser() == nil {
		return fmt.Errorf("empty User")
	}

	if req.GetRebaseId() == "" {
		return fmt.Errorf("empty RebaseId")
	}

	if req.GetBranch() == nil {
		return fmt.Errorf("empty Branch")
	}

	if req.GetBranchSha() == "" {
		return fmt.Errorf("empty BranchSha")
	}

	if req.GetRemoteRepository() == nil {
		return fmt.Errorf("empty RemoteRepository")
	}

	if req.GetRemoteBranch() == nil {
		return fmt.Errorf("empty RemoteBranch")
	}

	return nil
}
