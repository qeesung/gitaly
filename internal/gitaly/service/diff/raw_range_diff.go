package diff

import (
	"context"
	"fmt"
	"io"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func (s *server) RawRangeDiff(in *gitalypb.RawRangeDiffRequest, stream gitalypb.DiffService_RawRangeDiffServer) error {
	ctxlogrus.Extract(stream.Context()).WithFields(log.Fields{
		"RangeSpec": in.GetRangeSpec(),
	}).Debug("RawRangeDiff")

	if err := validateRawRangeDiffRequest(stream.Context(), s.locator, in); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	ctx := stream.Context()
	repo := s.localrepo(in.GetRepository())

	var flags []git.Option
	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return structerr.NewInternal("detecting object hash: %w", err)
	}

	flags = append(flags, git.Flag{Name: fmt.Sprintf("--abbrev=%d", objectHash.EncodedLen())})

	var revisions []string

	switch in.GetRangeSpec().(type) {
	case *gitalypb.RawRangeDiffRequest_RangePair:
		revisions = append(revisions, in.GetRangePair().GetRange1(), in.GetRangePair().GetRange2())
	case *gitalypb.RawRangeDiffRequest_RevisionRange:
		revisions = append(revisions, fmt.Sprintf("%s...%s", in.GetRevisionRange().GetRev1(), in.GetRevisionRange().GetRev2()))
	case *gitalypb.RawRangeDiffRequest_BaseWithRevisions:
		revisions = append(revisions, in.GetBaseWithRevisions().GetBase(), in.GetBaseWithRevisions().GetRev1(), in.GetBaseWithRevisions().GetRev2())
	}

	subCmd := git.Command{
		Name:  "range-diff",
		Flags: flags,
		Args:  revisions,
	}

	sw := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.RawRangeDiffResponse{Data: p})
	})

	return sendRawRangeDiffOutput(ctx, s.localrepo(in.GetRepository()), sw, subCmd)
}

func validateRawRangeDiffRequest(ctx context.Context, locator storage.Locator, in *gitalypb.RawRangeDiffRequest) error {
	if err := locator.ValidateRepository(ctx, in.GetRepository()); err != nil {
		return err
	}

	switch spec := in.GetRangeSpec().(type) {
	case *gitalypb.RawRangeDiffRequest_RangePair:
		if spec.RangePair.GetRange1() == "" {
			return fmt.Errorf("empty Range1")
		}
		if spec.RangePair.GetRange2() == "" {
			return fmt.Errorf("empty Range2")
		}
	case *gitalypb.RawRangeDiffRequest_RevisionRange:
		if spec.RevisionRange.GetRev1() == "" {
			return fmt.Errorf("empty Rev1")
		}
		if spec.RevisionRange.GetRev2() == "" {
			return fmt.Errorf("empty Rev2")
		}
	case *gitalypb.RawRangeDiffRequest_BaseWithRevisions:
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

func sendRawRangeDiffOutput(ctx context.Context, repo *localrepo.Repo, sender io.Writer, subCmd git.Command) error {
	cmd, err := repo.Exec(ctx, subCmd, git.WithSetupStdout())
	if err != nil {
		return structerr.NewInternal("cmd: %w", err)
	}

	if _, err := io.Copy(sender, cmd); err != nil {
		return structerr.NewAborted("send: %w", err)
	}

	return cmd.Wait()
}
