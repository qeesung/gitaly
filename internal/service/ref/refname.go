package ref

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// FindRefName returns a ref that starts with the given prefix, if one exists.
//  If there is more than one such ref there is no guarantee which one is
//  returned or that the same one is returned on each call.
func (s *server) FindRefName(ctx context.Context, in *gitalypb.FindRefNameRequest) (*gitalypb.FindRefNameResponse, error) {
	if in.CommitId == "" {
		return nil, helper.ErrInvalidArgument(fmt.Errorf("empty commit sha"))
	}

	ref, err := findRefName(ctx, in.Repository, in.CommitId, string(in.Prefix))
	if err != nil {
		return nil, helper.ErrInternal(err)
	}

	return &gitalypb.FindRefNameResponse{Name: []byte(ref)}, nil
}

// We assume `repo` and `commitID` and `prefix` are non-empty
func findRefName(ctx context.Context, repo *gitalypb.Repository, commitID, prefix string) (string, error) {
	flags := []git.Option{
		git.Flag{Name: "--format=%(refname)"},
		git.Flag{Name: "--count=1"},
	}

	subCmd := ForEachRefCmd{PostArgFlags: []git.Option{
		git.ValueFlag{Name: "--contains", Value: commitID},
	}}

	subCmd.Name = "for-each-ref"
	subCmd.Flags = flags
	subCmd.Args = []string{prefix}

	cmd, err := git.SafeCmd(ctx, repo, nil, subCmd)
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(cmd)
	scanner.Scan()
	if err := scanner.Err(); err != nil {
		return "", err
	}
	refName := scanner.Text()

	if err := cmd.Wait(); err != nil {
		// We're suppressing the error since invalid commits isn't an error
		//  according to Rails
		return "", nil
	}

	// Trailing spaces are not allowed per the documentation
	//  https://www.kernel.org/pub/software/scm/git/docs/git-check-ref-format.html
	return strings.TrimSpace(refName), nil
}

// ForEachRefCmd is a command specialized for for-each-ref
type ForEachRefCmd struct {
	git.SubCmd
	PostArgFlags []git.Option
}

// IsCmd is to satisfy the git.Cmd interface
func (f ForEachRefCmd) IsCmd() {}

var (
	// ErrOnlyForEachRefAllowed indicates a command other than for-each-ref is being used with ForEachRefCmd
	ErrOnlyForEachRefAllowed = errors.New("only for-each-ref allowed")

	// ErrNoPostSeparatorArgsAllowed indicates post separator args exist when none are allowed
	ErrNoPostSeparatorArgsAllowed = errors.New("post separator args not allowed")
)

// ValidateArgs validates and returns the flags and arguments for the for-each-ref command
func (f ForEachRefCmd) ValidateArgs() ([]string, error) {
	if f.Name != "for-each-ref" {
		return nil, ErrOnlyForEachRefAllowed
	}

	args, err := f.SubCmd.ValidateArgs()
	if err != nil {
		return nil, err
	}

	var postArgFlags []string

	for _, o := range f.PostArgFlags {
		args, err := o.ValidateArgs()
		if err != nil {
			return nil, err
		}
		postArgFlags = append(postArgFlags, args...)
	}

	if len(f.SubCmd.PostSepArgs) > 0 {
		return nil, ErrNoPostSeparatorArgsAllowed
	}

	return append(args, postArgFlags...), nil
}
