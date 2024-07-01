package diff

import (
	"context"
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/diff"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type requestWithLeftRightCommitIDs interface {
	GetRepository() *gitalypb.Repository
	GetLeftCommitId() string
	GetRightCommitId() string
}

func validateRequest(ctx context.Context, locator storage.Locator, in requestWithLeftRightCommitIDs) error {
	if err := locator.ValidateRepository(ctx, in.GetRepository()); err != nil {
		return err
	}
	if in.GetLeftCommitId() == "" {
		return errors.New("empty LeftCommitId")
	}
	if in.GetRightCommitId() == "" {
		return errors.New("empty RightCommitId")
	}

	return nil
}

func (s *server) eachDiff(ctx context.Context, repo *localrepo.Repo, subCmd git.Command, limits diff.Limits, callback func(*diff.Diff) error) error {
	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object hash: %w", err)
	}

	diffConfig := git.ConfigPair{Key: "diff.noprefix", Value: "false"}

	cmd, err := repo.Exec(ctx, subCmd, git.WithConfig(diffConfig), git.WithSetupStdout())
	if err != nil {
		return structerr.NewInternal("cmd: %w", err)
	}

	diffParser := diff.NewDiffParser(objectHash, cmd, limits)

	for diffParser.Parse() {
		if err := callback(diffParser.Diff()); err != nil {
			return err
		}
	}

	if err := diffParser.Err(); err != nil {
		return structerr.NewInternal("parse failure: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return structerr.NewFailedPrecondition("%w", err)
	}

	return nil
}

func logPaths(paths [][]byte) []string {
	result := make([]string, len(paths))
	for i, p := range paths {
		result[i] = string(p)
	}
	return result
}
