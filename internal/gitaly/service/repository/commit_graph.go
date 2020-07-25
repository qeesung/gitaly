package repository

import (
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

const (
	CommitGraphRelPath = "objects/info/commit-graph"
)

// WriteCommitGraph write or update commit-graph file in a repository
func (s *server) WriteCommitGraph(ctx context.Context, in *gitalypb.WriteCommitGraphRequest) (*gitalypb.WriteCommitGraphResponse, error) {
	if err := s.writeCommitGraph(ctx, in.GetRepository()); err != nil {
		return nil, helper.ErrInternal(fmt.Errorf("WriteCommitGraph: gitCommand: %v", err))
	}

	return &gitalypb.WriteCommitGraphResponse{}, nil
}

func (s *server) writeCommitGraph(ctx context.Context, repo repository.GitRepo) error {
	cmd, err := s.gitCmdFactory.New(ctx, repo,
		git.SubSubCmd{
			Name:   "commit-graph",
			Action: "write",
			Flags: []git.Option{
				git.Flag{Name: "--reachable"},
			},
		},
	)
	if err != nil {
		return err
	}

	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}
