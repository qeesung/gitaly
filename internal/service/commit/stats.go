package commit

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

func (s *server) CommitStats(ctx context.Context, in *gitalypb.CommitStatsRequest) (*gitalypb.CommitStatsResponse, error) {
	if err := git.ValidateRevision(in.Revision); err != nil {
		return nil, helper.ErrInvalidArgument(err)
	}

	resp, err := commitStats(ctx, in)
	if err != nil {
		return nil, helper.ErrInternal(err)
	}

	return resp, nil
}

func commitStats(ctx context.Context, in *gitalypb.CommitStatsRequest) (*gitalypb.CommitStatsResponse, error) {
	commit, err := log.GetCommit(ctx, in.Repository, string(in.Revision))
	if err != nil {
		return nil, err
	}
	if commit == nil {
		return nil, fmt.Errorf("commit not found: %q", in.Revision)
	}

	cmd, err := git.Command(ctx, in.Repository, "diff", "--numstat", commit.Id+"^", commit.Id)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(cmd)
	var added, deleted int32

	for scanner.Scan() {
		split := strings.SplitN(scanner.Text(), "\t", 3)
		if len(split) != 3 {
			return nil, fmt.Errorf("invalid numstat line %q", scanner.Text())
		}

		if add64, err := strconv.ParseInt(split[0], 10, 32); err == nil {
			added += int32(add64)
		}

		if del64, err := strconv.ParseInt(split[1], 10, 32); err == nil {
			deleted += int32(del64)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return &gitalypb.CommitStatsResponse{
		Oid:       commit.Id,
		Additions: added,
		Deletions: deleted,
	}, cmd.Wait()
}
