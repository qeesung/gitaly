package commit

import (
	"context"
	"errors"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) LastCommitForPath(ctx context.Context, in *gitalypb.LastCommitForPathRequest) (*gitalypb.LastCommitForPathResponse, error) {
	if err := validateLastCommitForPathRequest(ctx, s.locator, in); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	resp, err := s.lastCommitForPath(ctx, in)
	if err != nil {
		return nil, structerr.NewInternal("%w", err)
	}

	return resp, nil
}

func (s *server) lastCommitForPath(ctx context.Context, in *gitalypb.LastCommitForPathRequest) (*gitalypb.LastCommitForPathResponse, error) {
	path := string(in.GetPath())
	if len(path) == 0 || path == "/" {
		path = "."
	}

	repo := s.localrepo(in.GetRepository())
	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return nil, err
	}
	defer cancel()

	options := in.GetGlobalOptions()

	// Preserve backwards compatibility with legacy LiteralPathspec
	// flag: These protobuf changes were not shipped in 13.1, so can be
	// remove before 13.2.
	if in.GetLiteralPathspec() {
		if options == nil {
			options = &gitalypb.GlobalOptions{}
		}

		options.LiteralPathspecs = true
	}

	commit, err := log.LastCommitForPath(ctx, s.gitCmdFactory, objectReader, repo, git.Revision(in.GetRevision()), path, options)
	if err != nil {
		if errors.As(err, &catfile.NotFoundError{}) {
			return &gitalypb.LastCommitForPathResponse{}, nil
		}
		return nil, err
	}

	return &gitalypb.LastCommitForPathResponse{Commit: commit.GitCommit}, err
}

func validateLastCommitForPathRequest(ctx context.Context, locator storage.Locator, in *gitalypb.LastCommitForPathRequest) error {
	if err := locator.ValidateRepository(ctx, in.GetRepository()); err != nil {
		return err
	}
	if err := git.ValidateRevision(in.Revision); err != nil {
		return err
	}

	path := string(in.GetPath())
	switch {
	case path == "", path == "/":
		// We map both the empty path and "/" to instead refer to the root directory, so these are fine.
	case filepath.IsAbs(path):
		// Strictly speaking this is already handled by `filepath.IsLocal()`, but handling this case explicitly
		// allows us to generate a better error message.
		return structerr.NewInvalidArgument("path is an absolute path").WithMetadata("path", path)
	case !filepath.IsLocal(path):
		return structerr.NewInvalidArgument("path escapes repository").WithMetadata("path", path)
	}

	return nil
}
