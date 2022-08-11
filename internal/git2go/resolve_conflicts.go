package git2go

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git/conflict"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
)

// ResolveCommand contains arguments to perform a merge commit and resolve any
// conflicts produced from that merge commit
type ResolveCommand struct {
	MergeCommand
	Resolutions []conflict.Resolution
}

// ResolveResult returns information about the successful merge and resolution
type ResolveResult struct {
	MergeResult

	// Err is set if an error occurred. Err must exist on all gob serialized
	// results so that any error can be returned.
	Err error
}

// Resolve will attempt merging and resolving conflicts for the provided request
func (b *Executor) Resolve(ctx context.Context, repo repository.GitRepo, r ResolveCommand) (ResolveResult, error) {
	if err := r.verify(); err != nil {
		return ResolveResult{}, fmt.Errorf("resolve: %w: %s", ErrInvalidArgument, err.Error())
	}

	input := &bytes.Buffer{}
	if err := gob.NewEncoder(input).Encode(r); err != nil {
		return ResolveResult{}, fmt.Errorf("resolve: %w", err)
	}

	var args []string
	if b.signingKey != "" {
		args = []string{"-signing-key", b.signingKey}
	}

	stdout, err := b.run(ctx, repo, input, "resolve", args...)
	if err != nil {
		return ResolveResult{}, err
	}

	var response ResolveResult
	if err := gob.NewDecoder(stdout).Decode(&response); err != nil {
		return ResolveResult{}, fmt.Errorf("resolve: %w", err)
	}

	if response.Err != nil {
		return ResolveResult{}, response.Err
	}

	return response, nil
}
