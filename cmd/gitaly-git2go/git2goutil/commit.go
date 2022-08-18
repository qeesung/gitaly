package git2goutil

import (
	"fmt"

	git "github.com/libgit2/git2go/v33"
)

// CommitSubmitter is the helper struct to make signed Commits conveniently.
type CommitSubmitter struct {
	Repo           *git.Repository
	SigningKeyPath string
}

// NewCommitSubmitter creates a new CommitSubmitter.
func NewCommitSubmitter(repo *git.Repository, signingKeyPath string) *CommitSubmitter {
	return &CommitSubmitter{
		Repo:           repo,
		SigningKeyPath: signingKeyPath,
	}
}

// Commit commits a commit with or without OpenPGP signature depends on SigningKeyPath value.
func (cs *CommitSubmitter) Commit(
	author, committer *git.Signature,
	messageEncoding git.MessageEncoding,
	message string,
	tree *git.Tree,
	parents ...*git.Commit,
) (*git.Oid, error) {
	commitBytes, err := cs.Repo.CreateCommitBuffer(author, committer, messageEncoding, message, tree, parents...)
	if err != nil {
		return nil, err
	}

	signature, err := CreateCommitSignature(cs.SigningKeyPath, string(commitBytes))
	if err != nil {
		return nil, fmt.Errorf("create commit signature: %w", err)
	}

	commitID, err := cs.Repo.CreateCommitWithSignature(string(commitBytes), signature, "")
	if err != nil {
		return nil, err
	}

	return commitID, nil
}
