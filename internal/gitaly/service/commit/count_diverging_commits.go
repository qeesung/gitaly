package commit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// CountDivergingCommits counts the diverging commits between from and to. Important to note that when --max-count is applied, the counts are not guaranteed to be
// accurate because --max-count is applied before it does the rev walk.
func (s *server) CountDivergingCommits(ctx context.Context, req *gitalypb.CountDivergingCommitsRequest) (*gitalypb.CountDivergingCommitsResponse, error) {
	if err := s.validateCountDivergingCommitsRequest(ctx, req); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	from, to := string(req.GetFrom()), string(req.GetTo())
	maxCount := int(req.GetMaxCount())
	left, right, err := s.findLeftRightCount(ctx, req.GetRepository(), from, to, maxCount)
	if err != nil {
		return nil, structerr.NewInternal("%w", err)
	}

	return &gitalypb.CountDivergingCommitsResponse{LeftCount: left, RightCount: right}, nil
}

func (s *server) validateCountDivergingCommitsRequest(ctx context.Context, req *gitalypb.CountDivergingCommitsRequest) error {
	if req.GetFrom() == nil || req.GetTo() == nil {
		return errors.New("from and to are both required")
	}

	repository := req.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return err
	}

	return nil
}

func buildRevListCountCmd(from, to string, maxCount int) git.Command {
	subCmd := git.Command{
		Name:  "rev-list",
		Flags: []git.Option{git.Flag{Name: "--count"}, git.Flag{Name: "--left-right"}},
		Args:  []string{fmt.Sprintf("%s...%s", from, to)},
	}
	if maxCount != 0 {
		subCmd.Flags = append(subCmd.Flags, git.Flag{Name: fmt.Sprintf("--max-count=%d", maxCount)})
	}
	return subCmd
}

func (s *server) findLeftRightCount(ctx context.Context, repo *gitalypb.Repository, from, to string, maxCount int) (int32, int32, error) {
	cmd, err := s.gitCmdFactory.New(ctx, repo, buildRevListCountCmd(from, to, maxCount), git.WithSetupStdout())
	if err != nil {
		return 0, 0, fmt.Errorf("git rev-list cmd: %w", err)
	}

	var leftCount, rightCount int64
	countStr, err := io.ReadAll(cmd)
	if err != nil {
		return 0, 0, fmt.Errorf("git rev-list error: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return 0, 0, fmt.Errorf("gi rev-list error: %w", err)
	}

	counts := strings.Fields(string(countStr))
	if len(counts) != 2 {
		return 0, 0, fmt.Errorf("invalid output from git rev-list --left-right: %v", string(countStr))
	}

	leftCount, err = strconv.ParseInt(counts[0], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid left count value: %v", counts[0])
	}

	rightCount, err = strconv.ParseInt(counts[1], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid right count value: %v", counts[1])
	}

	return int32(leftCount), int32(rightCount), nil
}
