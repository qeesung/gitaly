package main

import (
	"context"
	"fmt"
	"io"
	"net/url"

	"github.com/git-lfs/git-lfs/v3/lfs"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/smudge"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/labkit/log"
	"gitlab.com/gitlab-org/labkit/tracing"
)

func smudgeContents(cfg smudge.Config, to io.Writer, from io.Reader) (returnedErr error) {
	// Since the environment is sanitized at the moment, we're only
	// using this to extract the correlation ID. The finished() call
	// to clean up the tracing will be a NOP here.
	ctx, finished := tracing.ExtractFromEnv(context.Background())
	defer finished()

	output, err := handleSmudge(ctx, cfg, from)
	if err != nil {
		return fmt.Errorf("smudging contents: %w", err)
	}
	defer func() {
		if err := output.Close(); err != nil && returnedErr == nil {
			returnedErr = fmt.Errorf("closing LFS object: %w", err)
		}
	}()

	if _, err := io.Copy(to, output); err != nil {
		return fmt.Errorf("writing smudged contents: %w", err)
	}

	return nil
}

func handleSmudge(ctx context.Context, cfg smudge.Config, from io.Reader) (io.ReadCloser, error) {
	logger := log.ContextLogger(ctx)

	ptr, contents, err := lfs.DecodeFrom(from)
	if err != nil {
		// This isn't a valid LFS pointer. Just copy the existing pointer data.
		return io.NopCloser(contents), nil
	}

	logger.WithField("oid", ptr.Oid).Debug("decoded LFS OID")

	client, err := gitlab.NewHTTPClient(logger, cfg.Gitlab, cfg.TLS, prometheus.Config{})
	if err != nil {
		return io.NopCloser(contents), err
	}

	qs := url.Values{}
	qs.Set("oid", ptr.Oid)
	qs.Set("gl_repository", cfg.GlRepository)
	u := url.URL{Path: "/lfs", RawQuery: qs.Encode()}

	response, err := client.Get(ctx, u.String())
	if err != nil {
		return io.NopCloser(contents), fmt.Errorf("error loading LFS object: %v", err)
	}

	if response.StatusCode == 200 {
		return response.Body, nil
	}

	if err := response.Body.Close(); err != nil {
		logger.WithError(err).Error("closing LFS pointer body: %w", err)
	}

	return io.NopCloser(contents), nil
}
