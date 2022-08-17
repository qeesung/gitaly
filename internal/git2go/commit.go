package git2go

import (
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
)

// IndexError is an error that was produced by performing an invalid operation on the index.
type IndexError string

// Error returns the error message of the index error.
func (err IndexError) Error() string { return string(err) }

// InvalidArgumentError is returned when an invalid argument is provided.
type InvalidArgumentError string

func (err InvalidArgumentError) Error() string { return string(err) }

// FileNotFoundError is returned when an action attempts to operate on a non-existing file.
type FileNotFoundError string

func (err FileNotFoundError) Error() string {
	return fmt.Sprintf("file not found: %q", string(err))
}

// FileExistsError is returned when an action attempts to overwrite an existing file.
type FileExistsError string

func (err FileExistsError) Error() string {
	return fmt.Sprintf("file exists: %q", string(err))
}

// DirectoryExistsError is returned when an action attempts to overwrite a directory.
type DirectoryExistsError string

func (err DirectoryExistsError) Error() string {
	return fmt.Sprintf("directory exists: %q", string(err))
}

// CommitCommand contains the information and the steps to build a commit.
type CommitCommand struct {
	// Repository is the path of the repository to operate on.
	Repository string
	// Author is the author of the commit.
	Author Signature
	// Committer is the committer of the commit.
	Committer Signature
	// Message is message of the commit.
	Message string
	// Parent is the OID of the commit to use as the parent of this commit.
	Parent string
	// Actions are the steps to build the commit.
	Actions []Action
	// SigningKey is a path to the key to sign commit using OpenPGP
	SigningKey string
}

// Commit builds a commit from the actions, writes it to the object database and
// returns its object id.
func (b *Executor) Commit(ctx context.Context, repo repository.GitRepo, c CommitCommand) (git.ObjectID, error) {
	c.SigningKey = b.signingKey

	return b.runWithGob(ctx, repo, "commit", c)
}
