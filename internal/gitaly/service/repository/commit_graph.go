package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

const (
	CommitGraphRelPath      = "objects/info/commit-graph"
	CommitGraphsRelPath     = "objects/info/commit-graphs"
	CommitGraphChainRelPath = CommitGraphsRelPath + "/commit-graph-chain"
)

// WriteCommitGraph write or update commit-graph file in a repository
func (s *server) WriteCommitGraph(ctx context.Context, in *gitalypb.WriteCommitGraphRequest) (*gitalypb.WriteCommitGraphResponse, error) {
	if err := s.safeWriteCommitGraph(ctx, in.GetRepository(), in.GetSplitStrategy()); err != nil {
		return nil, helper.ErrInternal(fmt.Errorf("WriteCommitGraph: gitCommand: %v", err))
	}

	return &gitalypb.WriteCommitGraphResponse{}, nil
}

func (s *server) combineSplitCommitGraph(
	ctx context.Context,
	repo repository.GitRepo,
) error {
	return s.safeWriteCommitGraph(ctx, repo, gitalypb.WriteCommitGraphRequest_Replace)
}

func (s *server) safeWriteCommitGraph(
	ctx context.Context,
	repo repository.GitRepo,
	splitStrategy gitalypb.WriteCommitGraphRequest_SplitStrategy,
) error {
	ctxlogger := ctxlogrus.Extract(ctx)

	if err := s.writeCommitGraph(ctx, repo, splitStrategy); err != nil {
		return err
	}

	if splitStrategy != gitalypb.WriteCommitGraphRequest_NoSplit {
		if err := s.validateCommitGraph(ctx, repo, false); err != nil {
			ctxlogger.
				WithField("Validate", "failed").
				WithError(err).
				Warn("CommitGraph")

			if err = s.rewriteCommitGraph(ctx, repo, splitStrategy); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *server) writeCommitGraph(
	ctx context.Context,
	repo repository.GitRepo,
	splitStrategy gitalypb.WriteCommitGraphRequest_SplitStrategy,
) error {
	flags := []git.Option{
		git.Flag{Name: "--reachable"},
	}

	var splitOptions []git.Option
	switch splitStrategy {
	case gitalypb.WriteCommitGraphRequest_SizeMultiple:
		splitOptions = []git.Option{
			git.Flag{Name: "--split"},
			git.ValueFlag{Name: "--size-multiple", Value: "4"},
		}
	case gitalypb.WriteCommitGraphRequest_NoMerge:
		splitOptions = []git.Option{
			git.ValueFlag{Name: "--split", Value: "no-merge"},
		}
	case gitalypb.WriteCommitGraphRequest_Replace:
		splitOptions = []git.Option{
			git.ValueFlag{Name: "--split", Value: "replace"},
		}
	default:
		splitOptions = []git.Option{}
	}
	flags = append(flags, splitOptions...)

	cmd, err := s.gitCmdFactory.New(ctx, repo,
		git.SubSubCmd{
			Name:   "commit-graph",
			Action: "write",
			Flags:  flags,
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

func (s *server) rewriteCommitGraph(
	ctx context.Context,
	repo repository.GitRepo,
	splitStrategy gitalypb.WriteCommitGraphRequest_SplitStrategy,
) error {
	repoPath, err := s.locator.GetRepoPath(repo)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(repoPath, CommitGraphChainRelPath)); err != nil {
		return err
	}

	return s.writeCommitGraph(ctx, repo, splitStrategy)
}

func (s *server) validateCommitGraph(ctx context.Context, repo repository.GitRepo, isShallow bool) error {
	var flags []git.Option
	if isShallow {
		flags = append(flags, git.Flag{Name: "--shallow"})
	}

	cmd, err := s.gitCmdFactory.New(ctx, repo, git.SubSubCmd{
		Name:   "commit-graph",
		Action: "verify",
		Flags:  flags,
	})
	if err != nil {
		return err
	}

	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}
