package diff

import (
	"context"
	"fmt"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/rangediff"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) RangeDiff(in *gitalypb.RangeDiffRequest, stream gitalypb.DiffService_RangeDiffServer) error {
	ctxlogrus.Extract(stream.Context()).WithFields(log.Fields{
		"RangeSpec": in.GetRangeSpec(),
	}).Info("RangeDiff")

	if err := validateRangeDiffRequest(stream.Context(), s.locator, in); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	ctx := stream.Context()
	repo := s.localrepo(in.GetRepository())

	flags := []git.Option{
		git.Flag{Name: "--no-color"},
	}
	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return structerr.NewInternal("detecting object hash: %w", err)
	}

	flags = append(flags, git.Flag{Name: fmt.Sprintf("--abbrev=%d", objectHash.EncodedLen())})

	var revisions []string

	switch spec := in.GetRangeSpec().(type) {
	case *gitalypb.RangeDiffRequest_RangePair:
		revisions = append(revisions, spec.RangePair.GetRange1(), spec.RangePair.GetRange2())
	case *gitalypb.RangeDiffRequest_RevisionRange:
		revisions = append(revisions, fmt.Sprintf("%s...%s", spec.RevisionRange.GetRev1(), spec.RevisionRange.GetRev2()))
	case *gitalypb.RangeDiffRequest_BaseWithRevisions:
		revisions = append(revisions, spec.BaseWithRevisions.GetBase(), spec.BaseWithRevisions.GetRev1(), spec.BaseWithRevisions.GetRev2())
	}

	cmd := git.Command{
		Name:  "range-diff",
		Flags: flags,
		Args:  revisions,
	}

	return s.eachRangeDiff(ctx, "RangeDiff", s.localrepo(in.GetRepository()), cmd, func(pair *rangediff.CommitPair) error {
		patchData := pair.PatchData
		response := &gitalypb.RangeDiffResponse{
			FromCommitId:       pair.FromCommit,
			ToCommitId:         pair.ToCommit,
			Comparison:         pair.Comparison,
			CommitMessageTitle: pair.CommitMessageTitle,
			PatchData:          pair.PatchData,
		}

		if len(patchData) <= s.MsgSizeThreshold {
			response.PatchData = patchData
			response.EndOfPatch = true

			if err := stream.Send(response); err != nil {
				return structerr.NewAborted("send: %w", err)
			}
		} else {
			for len(patchData) > 0 {
				partSize := s.MsgSizeThreshold
				if partSize >= len(patchData) {
					partSize = len(patchData)
					response.EndOfPatch = true
				}
				response.PatchData = patchData[:partSize]

				if err := stream.Send(response); err != nil {
					return structerr.NewAborted("send: %w", err)
				}
				response = &gitalypb.RangeDiffResponse{}
				patchData = patchData[partSize:]
			}
		}
		return nil
	})
}

func validateRangeDiffRequest(ctx context.Context, locator storage.Locator, in *gitalypb.RangeDiffRequest) error {
	if err := locator.ValidateRepository(ctx, in.GetRepository()); err != nil {
		return err
	}

	switch spec := in.GetRangeSpec().(type) {
	case *gitalypb.RangeDiffRequest_RangePair:
		if spec.RangePair.GetRange1() == "" {
			return fmt.Errorf("empty Range1")
		}
		if spec.RangePair.GetRange2() == "" {
			return fmt.Errorf("empty Range2")
		}
	case *gitalypb.RangeDiffRequest_RevisionRange:
		if spec.RevisionRange.GetRev1() == "" {
			return fmt.Errorf("empty Rev1")
		}
		if spec.RevisionRange.GetRev2() == "" {
			return fmt.Errorf("empty Rev2")
		}
	case *gitalypb.RangeDiffRequest_BaseWithRevisions:
		if spec.BaseWithRevisions.GetRev1() == "" {
			return fmt.Errorf("empty Rev1")
		}
		if spec.BaseWithRevisions.GetRev2() == "" {
			return fmt.Errorf("empty Rev2")
		}
		if spec.BaseWithRevisions.GetBase() == "" {
			return fmt.Errorf("empty Base")
		}
	}
	return nil
}

func (s *server) eachRangeDiff(ctx context.Context, rpc string, repo *localrepo.Repo, subCmd git.Command, callback func(commitPair *rangediff.CommitPair) error) error {
	cmd, err := repo.Exec(ctx, subCmd, git.WithSetupStdout())
	if err != nil {
		return structerr.NewInternal("cmd: %w", err)
	}

	parser := rangediff.NewRangeDiffParser(cmd)

	for parser.Parse() {
		if err := callback(parser.CommitPair()); err != nil {
			return err
		}
	}
	if err := parser.Err(); err != nil {
		return structerr.NewInternal("%s: parse failure: %w", rpc, err)
	}

	if err := cmd.Wait(); err != nil {
		return structerr.NewFailedPrecondition("%s: %w", rpc, err)
	}

	return nil
}
