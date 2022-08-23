//go:build static && system_libgit2

package main

import (
	"context"
	"encoding/gob"
	"errors"
	"flag"
	"fmt"

	git "github.com/libgit2/git2go/v33"
	"gitlab.com/gitlab-org/gitaly/v15/cmd/gitaly-git2go/git2goutil"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
)

type revertSubcommand struct{}

func (cmd *revertSubcommand) Flags() *flag.FlagSet {
	return flag.NewFlagSet("revert", flag.ExitOnError)
}

func (cmd *revertSubcommand) Run(ctx context.Context, decoder *gob.Decoder, encoder *gob.Encoder) error {
	var request git2go.RevertCommand
	if err := decoder.Decode(&request); err != nil {
		return err
	}

	commitID, err := cmd.revert(ctx, &request)
	return encoder.Encode(git2go.Result{
		CommitID: commitID,
		Err:      git2go.SerializableError(err),
	})
}

func (cmd *revertSubcommand) verify(ctx context.Context, r *git2go.RevertCommand) error {
	if r.Repository == "" {
		return errors.New("missing repository")
	}
	if r.AuthorName == "" {
		return errors.New("missing author name")
	}
	if r.AuthorMail == "" {
		return errors.New("missing author mail")
	}
	if r.Message == "" {
		return errors.New("missing message")
	}
	if r.Ours == "" {
		return errors.New("missing ours")
	}
	if r.Revert == "" {
		return errors.New("missing revert")
	}
	return nil
}

func (cmd *revertSubcommand) revert(ctx context.Context, request *git2go.RevertCommand) (string, error) {
	if err := cmd.verify(ctx, request); err != nil {
		return "", err
	}
	repo, err := git2goutil.OpenRepository(request.Repository)
	if err != nil {
		return "", fmt.Errorf("open repository: %w", err)
	}
	defer repo.Free()

	ours, err := lookupCommit(repo, request.Ours)
	if err != nil {
		return "", fmt.Errorf("ours commit lookup: %w", err)
	}

	revert, err := lookupCommit(repo, request.Revert)
	if err != nil {
		return "", fmt.Errorf("revert commit lookup: %w", err)
	}

	index, err := repo.RevertCommit(revert, ours, request.Mainline, nil)
	if err != nil {
		return "", fmt.Errorf("revert: %w", err)
	}
	defer index.Free()

	if index.HasConflicts() {
		return "", git2go.HasConflictsError{}
	}

	treeOID, err := index.WriteTreeTo(repo)
	if err != nil {
		return "", fmt.Errorf("write tree: %w", err)
	}
	tree, err := repo.LookupTree(treeOID)
	if err != nil {
		return "", fmt.Errorf("lookup tree: %w", err)
	}

	if treeOID.Equal(ours.TreeId()) {
		return "", git2go.EmptyError{}
	}

	committer := git.Signature(git2go.NewSignature(request.AuthorName, request.AuthorMail, request.AuthorDate))
	commitID, err := git2goutil.NewCommitSubmitter(repo, request.SigningKey).
		Commit(&committer, &committer, git.MessageEncodingUTF8, request.Message, tree, ours)
	if err != nil {
		return "", fmt.Errorf("create commit: %w", err)
	}

	return commitID.String(), nil
}
