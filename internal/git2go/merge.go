package git2go

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
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
	// AuthorDate is the author date of merge commit.
	AuthorDate time.Time
	// CommitterName. Can be empty if all Committer* vars are empty.
	// In that case AuthorName is used instead.
	CommitterName string
	// CommitterMail. Can be empty if all Committer* vars are empty.
	// In that case AuthorMail is used instead.
	CommitterMail string
	// CommitterDate. Can be empty if all Committer* vars are empty.
	// In that case AuthorDate is used instead.
	CommitterDate time.Time
	// Message is the message to be used for the merge commit.
	Message string
	// Ours is the commit into which theirs is to be merged.
	Ours string
	// Theirs is the commit that is to be merged into ours.
	Theirs string
	// AllowConflicts controls whether conflicts are allowed. If they are,
	// then conflicts will be committed as part of the result.
	AllowConflicts bool
	// Squash controls whether to perform squash merge.
	// If set to `true`, then the resulting commit will have `Ours` as its only parent.
	// Otherwise, a merge commit will be created with `Ours` and `Theirs` as its parents.
	Squash bool
	// SigningKey is a path to the key to sign commit using OpenPGP
	SigningKey string
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
	m.SigningKey = b.signingKey

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
	// If at least one Committer* var is set, require all of them to be set.
	if m.CommitterMail != "" || !m.CommitterDate.IsZero() || m.CommitterName != "" {
		if m.CommitterMail == "" {
			return errors.New("missing committer mail")
		}
		if m.CommitterName == "" {
			return errors.New("missing committer name")
		}
		if m.CommitterDate.IsZero() {
			return errors.New("missing committer date")
		}
	}
	return nil
}
