package operations

import (
	"context"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func (s *server) UserMergeBranch(bidi gitalypb.OperationService_UserMergeBranchServer) error {
	firstRequest, err := bidi.Recv()
	if err != nil {
		return err
	}

	ctx := bidi.Context()
	client, err := s.OperationServiceClient(ctx)
	if err != nil {
		return err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, firstRequest.GetRepository())
	if err != nil {
		return err
	}

	rubyBidi, err := client.UserMergeBranch(clientCtx)
	if err != nil {
		return err
	}

	if err := rubyBidi.Send(firstRequest); err != nil {
		return err
	}

	return rubyserver.ProxyBidi(
		func() error {
			request, err := bidi.Recv()
			if err != nil {
				return err
			}

			return rubyBidi.Send(request)
		},
		rubyBidi,
		func() error {
			response, err := rubyBidi.Recv()
			if err != nil {
				return err
			}

			return bidi.Send(response)
		},
	)
}

func validateFFRequest(in *gitalypb.UserFFBranchRequest) error {
	if len(in.Branch) == 0 {
		return fmt.Errorf("empty branch name")
	}

	if in.User == nil {
		return fmt.Errorf("empty user")
	}

	if in.CommitId == "" {
		return fmt.Errorf("empty commit id")
	}

	return nil
}

func (s *server) UserFFBranch(ctx context.Context, in *gitalypb.UserFFBranchRequest) (*gitalypb.UserFFBranchResponse, error) {
	if err := validateFFRequest(in); err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	client, err := s.OperationServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, in.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.UserFFBranch(clientCtx, in)
}

func validateUserMergeToRefRequest(in *gitalypb.UserMergeToRefRequest) error {
	if len(in.FirstParentRef) == 0 && len(in.Branch) == 0 {
		return fmt.Errorf("empty first parent ref and branch name")
	}

	if in.User == nil {
		return fmt.Errorf("empty user")
	}

	if in.SourceSha == "" {
		return fmt.Errorf("empty source SHA")
	}

	if len(in.TargetRef) == 0 {
		return fmt.Errorf("empty target ref")
	}

	if !strings.HasPrefix(string(in.TargetRef), "refs/merge-requests") {
		return fmt.Errorf("invalid target ref")
	}

	return nil
}

func (s *server) UserMergeToRef(ctx context.Context, in *gitalypb.UserMergeToRefRequest) (*gitalypb.UserMergeToRefResponse, error) {
	if err := validateUserMergeToRefRequest(in); err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	client, err := s.OperationServiceClient(ctx)
	if err != nil {
		return nil, helper.ErrInternal(err)
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, in.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.UserMergeToRef(clientCtx, in)
}
