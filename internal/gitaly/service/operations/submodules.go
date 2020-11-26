package operations

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const userUpdateSubmoduleName = "UserUpdateSubmodule"

func (s *Server) UserUpdateSubmodule(ctx context.Context, req *gitalypb.UserUpdateSubmoduleRequest) (*gitalypb.UserUpdateSubmoduleResponse, error) {
	if err := validateUserUpdateSubmoduleRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, userUpdateSubmoduleName+": %v", err)
	}

	if featureflag.IsEnabled(ctx, featureflag.GoUserUpdateSubmodule) {
		return s.userUpdateSubmodule(ctx, req)
	}

	client, err := s.ruby.OperationServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, s.locator, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.UserUpdateSubmodule(clientCtx, req)
}

func validateUserUpdateSubmoduleRequest(req *gitalypb.UserUpdateSubmoduleRequest) error {
	if req.GetRepository() == nil {
		return fmt.Errorf("empty Repository")
	}

	if req.GetUser() == nil {
		return fmt.Errorf("empty User")
	}

	if req.GetCommitSha() == "" {
		return fmt.Errorf("empty CommitSha")
	}

	if match, err := regexp.MatchString(`\A[0-9a-f]{40}\z`, req.GetCommitSha()); !match || err != nil {
		return fmt.Errorf("invalid CommitSha")
	}

	if len(req.GetBranch()) == 0 {
		return fmt.Errorf("empty Branch")
	}

	if len(req.GetSubmodule()) == 0 {
		return fmt.Errorf("empty Submodule")
	}

	if len(req.GetCommitMessage()) == 0 {
		return fmt.Errorf("empty CommitMessage")
	}

	return nil
}

func (s *Server) userUpdateSubmodule(ctx context.Context, req *gitalypb.UserUpdateSubmoduleRequest) (*gitalypb.UserUpdateSubmoduleResponse, error) {
	repo := git.NewRepository(req.GetRepository())
	branches, err := repo.GetBranches(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: get branches: %w", userUpdateSubmoduleName, err)
	}
	if len(branches) == 0 {
		return &gitalypb.UserUpdateSubmoduleResponse{
			CommitError: "Repository is empty",
		}, nil
	}

	branchRef, err := repo.GetBranch(ctx, string(req.GetBranch()))
	if err != nil {
		if errors.Is(err, git.ErrReferenceNotFound) {
			return nil, helper.ErrInvalidArgumentf("Cannot find branch") //nolint
		}
		return nil, fmt.Errorf("%s: get branch: %w", userUpdateSubmoduleName, err)
	}

	repoPath, err := s.locator.GetRepoPath(req.GetRepository())
	if err != nil {
		return nil, fmt.Errorf("%s: locate repo: %w", userUpdateSubmoduleName, err)
	}

	result, err := git2go.SubmoduleCommand{
		Repository: repoPath,
		AuthorMail: string(req.GetUser().GetEmail()),
		AuthorName: string(req.GetUser().GetName()),
		AuthorDate: time.Now(),
		Branch:     string(req.GetBranch()),
		CommitSHA:  req.GetCommitSha(),
		Submodule:  string(req.GetSubmodule()),
		Message:    string(req.GetCommitMessage()),
	}.Run(ctx, s.cfg)
	if err != nil {
		errStr := strings.TrimPrefix(err.Error(), "submodule: ")
		errStr = strings.TrimSpace(errStr)

		var resp *gitalypb.UserUpdateSubmoduleResponse
		for _, legacyErr := range []string{
			git2go.LegacyErrPrefixInvalidBranch,
			git2go.LegacyErrPrefixInvalidSubmodulePath,
			git2go.LegacyErrPrefixFailedCommit,
		} {
			if strings.HasPrefix(errStr, legacyErr) {
				resp = &gitalypb.UserUpdateSubmoduleResponse{
					CommitError: legacyErr,
				}
				ctxlogrus.
					Extract(ctx).
					WithError(err).
					Error(userUpdateSubmoduleName + ": git2go subcommand failure")
				break
			}
		}
		if strings.Contains(errStr, "is already at") {
			resp = &gitalypb.UserUpdateSubmoduleResponse{
				CommitError: errStr,
			}
		}
		if resp != nil {
			return resp, nil
		}
		return nil, fmt.Errorf("%s: submodule subcommand: %w", userUpdateSubmoduleName, err)
	}

	if err := s.updateReferenceWithHooks(
		ctx,
		req.GetRepository(),
		req.GetUser(),
		"refs/heads/"+string(req.GetBranch()),
		result.CommitID,
		branchRef.Target,
	); err != nil {
		var preReceiveError preReceiveError
		if errors.As(err, &preReceiveError) {
			return &gitalypb.UserUpdateSubmoduleResponse{
				PreReceiveError: preReceiveError.Error(),
			}, nil
		}
	}

	return &gitalypb.UserUpdateSubmoduleResponse{
		BranchUpdate: &gitalypb.OperationBranchUpdate{
			CommitId:      result.CommitID,
			BranchCreated: false,
			RepoCreated:   false,
		},
	}, nil
}
