package operations

//lint:file-ignore SA1019 due to planned removal in issue https://gitlab.com/gitlab-org/gitaly/issues/1628

import (
	"context"
	"errors"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) UserRebaseConfirmable(stream gitalypb.OperationService_UserRebaseConfirmableServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return err
	}

	header := firstRequest.GetHeader()
	if header == nil {
		return helper.ErrInvalidArgument(errors.New("UserRebaseConfirmable: empty UserRebaseConfirmableRequest.Header"))
	}

	if err := validateUserRebaseConfirmableHeader(header); err != nil {
		return helper.ErrInvalidArgumentf("UserRebaseConfirmable: %v", err)
	}

	if err := s.userRebaseConfirmable(stream, firstRequest, header.GetRepository()); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func (s *server) userRebaseConfirmable(stream gitalypb.OperationService_UserRebaseConfirmableServer, firstRequest *gitalypb.UserRebaseConfirmableRequest, repository *gitalypb.Repository) error {
	ctx := stream.Context()
	client, err := s.OperationServiceClient(ctx)
	if err != nil {
		return err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, repository)
	if err != nil {
		return err
	}

	rubyStream, err := client.UserRebaseConfirmable(clientCtx)
	if err != nil {
		return err
	}

	if err := rubyStream.Send(firstRequest); err != nil {
		return err
	}

	return rubyserver.ProxyBidi(
		func() error {
			request, err := stream.Recv()
			if err != nil {
				return err
			}

			return rubyStream.Send(request)
		},
		rubyStream,
		func() error {
			response, err := rubyStream.Recv()
			if err != nil {
				return err
			}

			return stream.Send(response)
		},
	)
}

func validateUserRebaseConfirmableHeader(header *gitalypb.UserRebaseConfirmableRequest_Header) error {
	if header.GetRepository() == nil {
		return errors.New("empty Repository")
	}

	if header.GetUser() == nil {
		return errors.New("empty User")
	}

	if header.GetRebaseId() == "" {
		return errors.New("empty RebaseId")
	}

	if header.GetBranch() == nil {
		return errors.New("empty Branch")
	}

	if header.GetBranchSha() == "" {
		return errors.New("empty BranchSha")
	}

	if header.GetRemoteRepository() == nil {
		return errors.New("empty RemoteRepository")
	}

	if header.GetRemoteBranch() == nil {
		return errors.New("empty RemoteBranch")
	}

	return nil
}

// DEPRECATED: https://gitlab.com/gitlab-org/gitaly/issues/1628
func (s *server) UserRebase(ctx context.Context, req *gitalypb.UserRebaseRequest) (*gitalypb.UserRebaseResponse, error) {
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

// DEPRECATED: https://gitlab.com/gitlab-org/gitaly/issues/1628
func validateUserRebaseRequest(req *gitalypb.UserRebaseRequest) error {
	if req.GetRepository() == nil {
		return errors.New("empty Repository")
	}

	if req.GetUser() == nil {
		return errors.New("empty User")
	}

	if req.GetRebaseId() == "" {
		return errors.New("empty RebaseId")
	}

	if req.GetBranch() == nil {
		return errors.New("empty Branch")
	}

	if req.GetBranchSha() == "" {
		return errors.New("empty BranchSha")
	}

	if req.GetRemoteRepository() == nil {
		return errors.New("empty RemoteRepository")
	}

	if req.GetRemoteBranch() == nil {
		return errors.New("empty RemoteBranch")
	}

	return nil
}
