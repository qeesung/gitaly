package gitpipe

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/localrepo"
)

// CatfileInfoResult is a result for the CatfileInfo pipeline step.
type CatfileInfoResult struct {
	// err is an error which occurred during execution of the pipeline.
	err error

	// ObjectName is the object name as received from the revlistResultChan.
	ObjectName []byte
	// ObjectInfo is the object info of the object.
	ObjectInfo *catfile.ObjectInfo
}

type catfileInfoConfig struct {
	skipResult func(*catfile.ObjectInfo) bool
}

// CatfileInfoOption is an option for the CatfileInfo and CatfileInfoAllObjects pipeline steps.
type CatfileInfoOption func(cfg *catfileInfoConfig)

// WithSkipCatfileInfoResult will execute the given function for each ObjectInfo processed by the
// pipeline. If the callback returns `true`, then the object will be skipped and not passed down the
// pipeline.
func WithSkipCatfileInfoResult(skipResult func(*catfile.ObjectInfo) bool) CatfileInfoOption {
	return func(cfg *catfileInfoConfig) {
		cfg.skipResult = skipResult
	}
}

// CatfileInfo processes revlistResults from the given channel and extracts object information via
// `git cat-file --batch-check`. The returned channel will contain all processed catfile info
// results. Any error received via the channel or encountered in this step will cause the pipeline
// to fail. Context cancellation will gracefully halt the pipeline.
func CatfileInfo(
	ctx context.Context,
	objectInfoReader catfile.ObjectInfoReader,
	revisionIterator RevisionIterator,
	opts ...CatfileInfoOption,
) CatfileInfoIterator {
	var cfg catfileInfoConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	resultChan := make(chan CatfileInfoResult)
	go func() {
		defer close(resultChan)

		for revisionIterator.Next() {
			revlistResult := revisionIterator.Result()

			objectInfo, err := objectInfoReader.Info(ctx, revlistResult.OID.Revision())
			if err != nil {
				sendCatfileInfoResult(ctx, resultChan, CatfileInfoResult{
					err: fmt.Errorf("retrieving object info for %q: %w", revlistResult.OID, err),
				})
				return
			}

			if cfg.skipResult != nil && cfg.skipResult(objectInfo) {
				continue
			}

			if isDone := sendCatfileInfoResult(ctx, resultChan, CatfileInfoResult{
				ObjectName: revlistResult.ObjectName,
				ObjectInfo: objectInfo,
			}); isDone {
				return
			}
		}

		if err := revisionIterator.Err(); err != nil {
			sendCatfileInfoResult(ctx, resultChan, CatfileInfoResult{err: err})
			return
		}
	}()

	return &catfileInfoIterator{
		ch: resultChan,
	}
}

// CatfileInfoAllObjects enumerates all Git objects part of the repository's object directory and
// extracts their object info via `git cat-file --batch-check`. The returned channel will contain
// all processed results. Any error encountered during execution of this pipeline step will cause
// the pipeline to fail. Context cancellation will gracefully halt the pipeline. Note that with this
// pipeline step, the resulting catfileInfoResults will never have an object name.
func CatfileInfoAllObjects(
	ctx context.Context,
	repo *localrepo.Repo,
	opts ...CatfileInfoOption,
) CatfileInfoIterator {
	var cfg catfileInfoConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	resultChan := make(chan CatfileInfoResult)

	go func() {
		defer close(resultChan)

		var stderr bytes.Buffer
		cmd, err := repo.Exec(ctx, git.SubCmd{
			Name: "cat-file",
			Flags: []git.Option{
				git.Flag{Name: "--batch-all-objects"},
				git.Flag{Name: "--batch-check"},
				git.Flag{Name: "--buffer"},
				git.Flag{Name: "--unordered"},
			},
		}, git.WithStderr(&stderr))
		if err != nil {
			sendCatfileInfoResult(ctx, resultChan, CatfileInfoResult{
				err: fmt.Errorf("spawning cat-file failed: %w", err),
			})
			return
		}

		reader := bufio.NewReader(cmd)
		for {
			objectInfo, err := catfile.ParseObjectInfo(reader)
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}

				sendCatfileInfoResult(ctx, resultChan, CatfileInfoResult{
					err: fmt.Errorf("parsing object info: %w", err),
				})
				return
			}

			if cfg.skipResult != nil && cfg.skipResult(objectInfo) {
				continue
			}

			if isDone := sendCatfileInfoResult(ctx, resultChan, CatfileInfoResult{
				ObjectInfo: objectInfo,
			}); isDone {
				return
			}
		}

		if err := cmd.Wait(); err != nil {
			sendCatfileInfoResult(ctx, resultChan, CatfileInfoResult{
				err: fmt.Errorf("cat-file failed: %w, stderr: %q", err, stderr),
			})
			return
		}
	}()

	return &catfileInfoIterator{
		ch: resultChan,
	}
}

func sendCatfileInfoResult(ctx context.Context, ch chan<- CatfileInfoResult, result CatfileInfoResult) bool {
	// In case the context has been cancelled, we have a race between observing an error from
	// the killed Git process and observing the context cancellation itself. But if we end up
	// here because of cancellation of the Git process, we don't want to pass that one down the
	// pipeline but instead just stop the pipeline gracefully. We thus have this check here up
	// front to error messages from the Git process.
	select {
	case <-ctx.Done():
		return true
	default:
	}

	select {
	case ch <- result:
		return false
	case <-ctx.Done():
		return true
	}
}
