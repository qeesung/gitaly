//go:build static && system_libgit2
// +build static,system_libgit2

package main

import (
	"context"
	"encoding/gob"
	"errors"
	"flag"
	"fmt"

	git "github.com/libgit2/git2go/v33"
	"gitlab.com/gitlab-org/gitaly/cmd/gitaly-git2go/git2goutil"
	"gitlab.com/gitlab-org/gitaly/internal/git2go"
)

type cherryPickSubcommand struct{}

func (cmd *cherryPickSubcommand) Flags() *flag.FlagSet {
	return flag.NewFlagSet("cherry-pick", flag.ExitOnError)
}

func (cmd *cherryPickSubcommand) Run(ctx context.Context, decoder *gob.Decoder, encoder *gob.Encoder) error {
	var request git2go.CherryPickCommand
	if err := decoder.Decode(&request); err != nil {
		return err
	}

	commitID, err := cmd.cherryPick(ctx, &request)
	return encoder.Encode(git2go.Result{
		CommitID: commitID,
		Err:      git2go.SerializableError(err),
	})
}

func (cmd *cherryPickSubcommand) verify(ctx context.Context, r *git2go.CherryPickCommand) error {
	if r.Repository == "" {
		return errors.New("missing repository")
	}
	if r.CommitterName == "" {
		return errors.New("missing committer name")
	}
	if r.CommitterMail == "" {
		return errors.New("missing committer mail")
	}
	if r.CommitterDate.IsZero() {
		return errors.New("missing committer date")
	}
	if r.Message == "" {
		return errors.New("missing message")
	}
	if r.Ours == "" {
		return errors.New("missing ours")
	}
	if r.Commit == "" {
		return errors.New("missing commit")
	}

	return nil
}

func (cmd *cherryPickSubcommand) cherryPick(ctx context.Context, r *git2go.CherryPickCommand) (string, error) {
	if err := cmd.verify(ctx, r); err != nil {
		return "", err
	}

	repo, err := git2goutil.OpenRepository(r.Repository)
	if err != nil {
		return "", fmt.Errorf("could not open repository: %w", err)
	}
	defer repo.Free()

	ours, err := lookupCommit(repo, r.Ours)
	if err != nil {
		return "", fmt.Errorf("ours commit lookup: %w", err)
	}

	pick, err := lookupCommit(repo, r.Commit)
	if err != nil {
		return "", fmt.Errorf("commit lookup: %w", err)
	}

	opts, err := git.DefaultCherrypickOptions()
	if err != nil {
		return "", fmt.Errorf("could not get default cherry-pick options: %w", err)
	}
	opts.Mainline = r.Mainline

	index, err := repo.CherrypickCommit(pick, ours, opts)
	if err != nil {
		return "", fmt.Errorf("could not cherry-pick commit: %w", err)
	}

	if index.HasConflicts() {
		return "", git2go.HasConflictsError{}
	}

	tree, err := index.WriteTreeTo(repo)
	if err != nil {
		return "", fmt.Errorf("could not write tree: %w", err)
	}

	if tree.Equal(ours.TreeId()) {
		return "", git2go.EmptyError{}
	}

	committer := git.Signature(git2go.NewSignature(r.CommitterName, r.CommitterMail, r.CommitterDate))

	commit, err := repo.CreateCommitFromIds("", pick.Author(), &committer, r.Message, tree, ours.Id())
	if err != nil {
		return "", fmt.Errorf("could not create cherry-pick commit: %w", err)
	}

	return commit.String(), nil
}
