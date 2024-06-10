package diff

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/diff"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) CommitDelta(in *gitalypb.CommitDeltaRequest, stream gitalypb.DiffService_CommitDeltaServer) error {
	ctx := stream.Context()

	s.logger.WithFields(log.Fields{
		"LeftCommitId":  in.LeftCommitId,
		"RightCommitId": in.RightCommitId,
		"Paths":         logPaths(in.Paths),
	}).DebugContext(ctx, "CommitDelta")

	if err := validateRequest(ctx, s.locator, in); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	leftSha := in.LeftCommitId
	rightSha := in.RightCommitId
	paths := in.GetPaths()

	repo := s.localrepo(in.GetRepository())

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object hash: %w", err)
	}

	cmd := git.Command{
		Name: "diff",
		Flags: []git.Option{
			git.Flag{Name: "--raw"},
			git.Flag{Name: fmt.Sprintf("--abbrev=%d", objectHash.EncodedLen())},
			git.Flag{Name: "--full-index"},
			git.Flag{Name: "--find-renames"},
		},
		Args: []string{leftSha, rightSha},
	}
	if len(paths) > 0 {
		for _, path := range paths {
			cmd.PostSepArgs = append(cmd.PostSepArgs, string(path))
		}
	}

	var batch []*gitalypb.CommitDelta
	var batchSize int

	flushFunc := func() error {
		if len(batch) == 0 {
			return nil
		}

		if err := stream.Send(&gitalypb.CommitDeltaResponse{Deltas: batch}); err != nil {
			return structerr.NewAborted("send: %w", err)
		}

		return nil
	}

	if err := s.eachDiff(ctx, repo, cmd, diff.Limits{}, func(diff *diff.Diff) error {
		delta := &gitalypb.CommitDelta{
			FromPath: diff.FromPath,
			ToPath:   diff.ToPath,
			FromId:   diff.FromID,
			ToId:     diff.ToID,
			OldMode:  diff.OldMode,
			NewMode:  diff.NewMode,
		}

		batch = append(batch, delta)
		batchSize += deltaSize(diff)

		if batchSize > s.MsgSizeThreshold {
			if err := flushFunc(); err != nil {
				return err
			}

			batch = nil
			batchSize = 0
		}

		return nil
	}); err != nil {
		return err
	}

	return flushFunc()
}

func deltaSize(diff *diff.Diff) int {
	size := len(diff.FromID) + len(diff.ToID) +
		4 + 4 + // OldMode and NewMode are int32 = 32/8 = 4 bytes
		len(diff.FromPath) + len(diff.ToPath)

	return size
}
