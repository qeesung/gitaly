package gitpipe

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
)

// diffTreeConfig is configuration for the DiffTree pipeline step.
type diffTreeConfig struct {
	recursive        bool
	ignoreSubmodules bool
	skipResult       func(*RevisionResult) bool
}

// DiffTreeOption is an option for the DiffTree pipeline step.
type DiffTreeOption func(cfg *diffTreeConfig)

// DiffTreeWithRecursive will make DiffTree recurse into subtrees.
func DiffTreeWithRecursive() DiffTreeOption {
	return func(cfg *diffTreeConfig) {
		cfg.recursive = true
	}
}

// DiffTreeWithIgnoreSubmodules causes git-diff-tree(1) to exclude submodule changes.
func DiffTreeWithIgnoreSubmodules() DiffTreeOption {
	return func(cfg *diffTreeConfig) {
		cfg.ignoreSubmodules = true
	}
}

// DiffTreeWithSkip will execute the given function for each RevisionResult processed by the
// pipeline. If the callback returns `true`, then the object will be skipped and not passed down
// the pipeline.
func DiffTreeWithSkip(skipResult func(*RevisionResult) bool) DiffTreeOption {
	return func(cfg *diffTreeConfig) {
		cfg.skipResult = skipResult
	}
}

// DiffTree runs git-diff-tree(1) between the two given revisions. The returned
// channel will contain the new object IDs listed by this command. For deleted
// files this would be the all-zeroes object ID. Cancelling the context will cause the
// pipeline to be cancelled, too. By default, it will not recurse into subtrees.
func DiffTree(
	ctx context.Context,
	repo *localrepo.Repo,
	leftRevision, rightRevision string,
	options ...DiffTreeOption,
) RevisionIterator {
	var cfg diffTreeConfig
	for _, option := range options {
		option(&cfg)
	}

	resultChan := make(chan RevisionResult)
	go func() {
		defer close(resultChan)

		flags := []git.Option{}

		if cfg.recursive {
			flags = append(flags, git.Flag{Name: "-r"})
		}
		if cfg.ignoreSubmodules {
			flags = append(flags, git.Flag{Name: "--ignore-submodules"})
		}

		var stderr strings.Builder
		cmd, err := repo.Exec(ctx,
			git.SubCmd{
				Name:  "diff-tree",
				Flags: flags,
				Args:  []string{leftRevision, rightRevision},
			},
			git.WithStderr(&stderr),
		)
		if err != nil {
			sendRevisionResult(ctx, resultChan, RevisionResult{
				err: fmt.Errorf("executing diff-tree: %w", err),
			})
			return
		}

		scanner := bufio.NewScanner(cmd)
		for scanner.Scan() {
			// We need to copy the line here because we'll hand it over to the caller
			// asynchronously, and the next call to `Scan()` will overwrite the buffer.
			line := make([]byte, len(scanner.Bytes()))
			copy(line, scanner.Bytes())

			attrsAndFile := bytes.SplitN(line, []byte{'\t'}, 2)
			if len(attrsAndFile) != 2 {
				sendRevisionResult(ctx, resultChan, RevisionResult{
					err: fmt.Errorf("splitting diff-tree attributes and file"),
				})
				return
			}

			attrs := bytes.SplitN(attrsAndFile[0], []byte{' '}, 5)
			if len(attrs) != 5 {
				sendRevisionResult(ctx, resultChan, RevisionResult{
					err: fmt.Errorf("splitting diff-tree attributes"),
				})
				return
			}

			result := RevisionResult{
				OID:        git.ObjectID(attrs[3]),
				ObjectName: attrsAndFile[1],
			}

			if cfg.skipResult != nil && cfg.skipResult(&result) {
				continue
			}

			if isDone := sendRevisionResult(ctx, resultChan, result); isDone {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			sendRevisionResult(ctx, resultChan, RevisionResult{
				err: fmt.Errorf("scanning diff-tree output: %w", err),
			})
			return
		}

		if err := cmd.Wait(); err != nil {
			sendRevisionResult(ctx, resultChan, RevisionResult{
				err: fmt.Errorf("diff-tree pipeline command: %w, stderr: %q", err, stderr.String()),
			})
			return
		}
	}()

	return &revisionIterator{
		ctx: ctx,
		ch:  resultChan,
	}
}
