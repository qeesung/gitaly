package git2go

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git/repository"
)

const (
	// MergeRecursionLimit limits how many virtual merge bases are computed
	// in a recursive merge.
	MergeRecursionLimit = 20
)

// MergeCommand contains parameters to perform a merge.
type MergeCommand struct {
	// Repository is the path to execute merge in.
	Repository string
	// AuthorName is the author name of merge commit.
	AuthorName string
	// AuthorMail is the author mail of merge commit.
	AuthorMail string
	// AuthorDate is the auithor date of merge commit.
	AuthorDate time.Time
	// Message is the message to be used for the merge commit.
	Message string
	// Ours is the commit that is to be merged into theirs.
	Ours string
	// Theirs is the commit into which ours is to be merged.
	Theirs string
	// AllowConflicts controls whether conflicts are allowed. If they are,
	// then conflicts will be committed as part of the result.
	AllowConflicts bool
}

// MergeResult contains results from a merge.
type MergeResult struct {
	// CommitID is the object ID of the generated merge commit.
	CommitID string
}

// Merge performs a merge via gitaly-git2go.
func (b *Executor) Merge(ctx context.Context, repo repository.GitRepo, m MergeCommand) (MergeResult, error) {
	if err := m.verify(); err != nil {
		return MergeResult{}, fmt.Errorf("merge: %w: %s", ErrInvalidArgument, err.Error())
	}

	commitID, err := b.runWithGob(ctx, repo, "merge", m)
	if err != nil {
		return MergeResult{}, err
	}

	return MergeResult{
		CommitID: commitID.String(),
	}, nil
}

func (m MergeCommand) verify() error {
	if m.Repository == "" {
		return errors.New("missing repository")
	}
	if m.AuthorName == "" {
		return errors.New("missing author name")
	}
	if m.AuthorMail == "" {
		return errors.New("missing author mail")
	}
	if m.Message == "" {
		return errors.New("missing message")
	}
	if m.Ours == "" {
		return errors.New("missing ours")
	}
	if m.Theirs == "" {
		return errors.New("missing theirs")
	}
	return nil
}
