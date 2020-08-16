package repository

import (
	"context"
	"fmt"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/internal/gitalyssh"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
)

func (s *server) FetchSourceBranch(ctx context.Context, req *gitalypb.FetchSourceBranchRequest) (*gitalypb.FetchSourceBranchResponse, error) {
	if featureflag.IsDisabled(ctx, featureflag.GoFetchSourceBranch) {
		return s.rubyFetchSourceBranch(ctx, req)
	}

	if err := git.ValidateRevision(req.GetSourceBranch()); err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	if err := git.ValidateRevision(req.GetTargetRef()); err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	exists, err := s.repositoryWithContent(ctx, req)
	if err != nil {
		return nil, err
	}

	if !exists {
		return &gitalypb.FetchSourceBranchResponse{Result: false}, nil
	}

	commitID, err := s.getRemoteCommit(ctx, req.GetRepository(), req.GetSourceRepository(), req.GetSourceBranch())
	if err != nil {
		return nil, err
	}

	if commitID == "" {
		return &gitalypb.FetchSourceBranchResponse{Result: false}, nil
	}

	cmd, err := git.SafeCmd(ctx, req.GetRepository(), nil,
		git.SubCmd{
			Name: "update-ref",
			Args: []string{string(req.GetTargetRef()), commitID},
		})
	if err != nil {
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		// Design quirk: if the fetch fails, this RPC returns Result: false, but no error.
		ctxlogrus.Extract(ctx).
			WithField("repo", req.GetRepository()).
			WithField("commit_id", commitID).
			WithField("target_ref", req.GetTargetRef()).
			WithError(err).Warn("git update-ref failed")
		return &gitalypb.FetchSourceBranchResponse{Result: false}, nil
	}

	return &gitalypb.FetchSourceBranchResponse{Result: true}, nil
}

func (s *server) repositoryWithContent(ctx context.Context, req *gitalypb.FetchSourceBranchRequest) (bool, error) {
	exists, err := s.repositoryExists(ctx, req.GetSourceRepository())
	if err != nil {
		return false, err
	}

	if !exists {
		return false, nil
	}

	hasContent, err := s.hasVisibleContent(ctx, req.GetSourceRepository())
	if err != nil {
		return false, err
	}

	return hasContent, nil
}

func (s *server) getRemoteCommit(ctx context.Context, repository *gitalypb.Repository, sourceRepository *gitalypb.Repository, branch []byte) (string, error) {
	if helper.RepoPathEqual(repository, sourceRepository) {
		return string(branch), nil
	}

	commitID, err := s.findCommit(ctx, sourceRepository, branch)
	if err != nil {
		return "", err
	}

	if commitID == "" {
		return "", nil
	}

	_, err = log.GetCommit(ctx, repository, commitID)
	if log.IsNotFound(err) {
		err := s.fetchSHA(ctx, repository, sourceRepository, commitID)
		if err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}

	return commitID, nil
}

func (s *server) fetchSHA(ctx context.Context, repository *gitalypb.Repository, sourceRepository *gitalypb.Repository, commitID string) error {
	repoPath, err := helper.GetRepoPath(repository)
	if err != nil {
		return err
	}

	env, err := gitalyssh.UploadPackEnv(ctx, &gitalypb.SSHUploadPackRequest{Repository: sourceRepository})
	if err != nil {
		return err
	}

	cmd, err := git.SafeBareCmd(ctx, git.CmdStream{}, env,
		[]git.Option{git.ValueFlag{"--git-dir", repoPath}},
		git.SubCmd{
			Name: "fetch",
			Args: []string{gitalyInternalURL, commitID},
		},
	)
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		ctxlogrus.Extract(ctx).
			WithField("repo_path", repoPath).
			WithField("commit_id", commitID).
			WithError(err).Warn("git fetch failed")
	}
	return err
}

func (s *server) rubyFetchSourceBranch(ctx context.Context, req *gitalypb.FetchSourceBranchRequest) (*gitalypb.FetchSourceBranchResponse, error) {
	client, err := s.ruby.RepositoryServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.FetchSourceBranch(clientCtx, req)
}

func (s *server) findCommit(ctx context.Context, repo *gitalypb.Repository, ref []byte) (string, error) {
	conn, err := s.getConnection(ctx, repo)
	if err != nil {
		return "", fmt.Errorf("findCommit: %w", err)
	}

	cc := gitalypb.NewCommitServiceClient(conn)
	resp, err := cc.FindCommit(ctx, &gitalypb.FindCommitRequest{
		Repository: repo,
		Revision:   ref,
	})

	if err != nil {
		return "", fmt.Errorf("findCommit: %w", err)
	}

	if resp.Commit == nil {
		return "", nil
	}

	return resp.Commit.Id, nil
}

func (s *server) repositoryExists(ctx context.Context, repo *gitalypb.Repository) (bool, error) {
	conn, err := s.getConnection(ctx, repo)
	if err != nil {
		return false, fmt.Errorf("repositoryExists: %w", err)
	}

	rc := gitalypb.NewRepositoryServiceClient(conn)
	resp, err := rc.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
		Repository: repo,
	})

	if err != nil {
		return false, fmt.Errorf("repositoryExists: %w", err)
	}

	return resp.Exists, nil
}

func (s *server) hasVisibleContent(ctx context.Context, repo *gitalypb.Repository) (bool, error) {
	conn, err := s.getConnection(ctx, repo)
	if err != nil {
		return false, fmt.Errorf("hasVisibleContent: %w", err)
	}

	rc := gitalypb.NewRepositoryServiceClient(conn)
	resp, err := rc.HasLocalBranches(ctx, &gitalypb.HasLocalBranchesRequest{
		Repository: repo,
	})

	if err != nil {
		return false, fmt.Errorf("hasVisibleContent: %w", err)
	}

	return resp.Value, nil
}

func (s *server) getConnection(ctx context.Context, repo *gitalypb.Repository) (*grpc.ClientConn, error) {
	server, err := helper.ExtractGitalyServer(ctx, repo.StorageName)
	if err != nil {
		return nil, err
	}

	conn, err := s.conns.Dial(ctx, server["address"], server["token"])
	if err != nil {
		return nil, err
	}
	return conn, nil
}
