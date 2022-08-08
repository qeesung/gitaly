package gitpipe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/lstree"
)

// lsTreeConfig is configuration for the LsTree pipeline step.
type lsTreeConfig struct {
	recursive  bool
	typeFilter func(*lstree.Entry) bool
	skipResult func(*RevisionResult) bool
}

// LsTreeOption is an option for the LsTree pipeline step.
type LsTreeOption func(cfg *lsTreeConfig)

// LsTreeWithRecursive will make LsTree recursive into subtrees.
func LsTreeWithRecursive() LsTreeOption {
	return func(cfg *lsTreeConfig) {
		cfg.recursive = true
	}
}

// LsTreeWithBlobFilter configures LsTree to only pass through blob objects.
func LsTreeWithBlobFilter() LsTreeOption {
	return func(cfg *lsTreeConfig) {
		cfg.typeFilter = func(e *lstree.Entry) bool { return e.Type == lstree.Blob }
	}
}

// LsTree runs git-ls-tree(1) for the given revisions. The returned channel will
// contain all object IDs listed by this command. This might include:
//  - Blobs
//  - Trees, unless you're calling it with LsTreeWithRecursive()
//  - Submodules, referring to the commit of the submodule
func LsTree(
	ctx context.Context,
	repo *localrepo.Repo,
	revision string,
	options ...LsTreeOption,
) RevisionIterator {
	var cfg lsTreeConfig
	for _, option := range options {
		option(&cfg)
	}

	resultChan := make(chan RevisionResult)
	go func() {
		defer close(resultChan)

		flags := []git.Option{
			git.Flag{Name: "-z"},
		}

		if cfg.recursive {
			flags = append(flags, git.Flag{Name: "-r"})
		}

		var stderr strings.Builder
		cmd, err := repo.Exec(ctx,
			git.SubCmd{
				Name:  "ls-tree",
				Flags: flags,
				Args:  []string{revision},
			},
			git.WithStderr(&stderr),
		)
		if err != nil {
			sendRevisionResult(ctx, resultChan, RevisionResult{
				err: fmt.Errorf("spawning ls-tree: %w", err),
			})
			return
		}

		parser := lstree.NewParser(cmd, git.ObjectHashSHA1)
		for {
			entry, err := parser.NextEntry()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				sendRevisionResult(ctx, resultChan, RevisionResult{
					err: fmt.Errorf("scanning ls-tree output: %w", err),
				})
				return
			}

			if cfg.typeFilter != nil && !cfg.typeFilter(entry) {
				continue
			}

			result := RevisionResult{
				OID:        entry.ObjectID,
				ObjectName: []byte(entry.Path),
			}

			if cfg.skipResult != nil && cfg.skipResult(&result) {
				continue
			}

			if isDone := sendRevisionResult(ctx, resultChan, result); isDone {
				return
			}
		}

		if err := cmd.Wait(); err != nil {
			sendRevisionResult(ctx, resultChan, RevisionResult{
				err: fmt.Errorf("ls-tree pipeline command: %w, stderr: %q", err, stderr.String()),
			})
			return
		}
	}()

	return &revisionIterator{
		ctx: ctx,
		ch:  resultChan,
	}
}
