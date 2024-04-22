package operations

import (
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
)

func (s *Server) merge(
	ctx context.Context,
	quarantineRepo *localrepo.Repo,
	author git.Signature,
	committer git.Signature,
	message string,
	ours string,
	theirs string,
	squash bool,
) (string, error) {
	treeOID, err := quarantineRepo.MergeTree(ctx, ours, theirs, localrepo.WithAllowUnrelatedHistories(), localrepo.WithConflictingFileNamesOnly())
	if err != nil {
		return "", err
	}

	parents := []git.ObjectID{git.ObjectID(ours)}
	if !squash {
		parents = append(parents, git.ObjectID(theirs))
	}

	cfg := localrepo.WriteCommitConfig{
		TreeID:         treeOID,
		Message:        message,
		Parents:        parents,
		AuthorName:     author.Name,
		AuthorEmail:    author.Email,
		AuthorDate:     author.When,
		CommitterName:  committer.Name,
		CommitterEmail: committer.Email,
		CommitterDate:  committer.When,
		GitConfig:      s.gitConfig,
		Sign:           true,
	}
	c, err := quarantineRepo.WriteCommit(
		ctx,
		cfg,
	)
	if err != nil {
		return "", fmt.Errorf("create commit from tree: %w", err)
	}

	return string(c), nil
}
