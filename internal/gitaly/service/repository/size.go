package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gitpipe"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func (s *server) RepositorySize(ctx context.Context, in *gitalypb.RepositorySizeRequest) (*gitalypb.RepositorySizeResponse, error) {
	repo := s.localrepo(in.GetRepository())
	var size int64
	var err error

	path, err := repo.Path()
	if err != nil {
		return nil, err
	}

	duSize := getPathSize(ctx, path)

	if featureflag.RevlistForRepoSize.IsEnabled(ctx) {
		size, err = calculateSizeWithRevlist(ctx, repo)
		if err != nil {
			return nil, fmt.Errorf("calculating repository size with git-rev-list: %w,", err)
		}

		ctxlogrus.Extract(ctx).
			WithField("repo_size_revlist", size).
			WithField("repo_size_du", duSize).
			Info("repository size calculated")
	} else if featureflag.CatfileRepoSize.IsEnabled(ctx) {
		size, err = calculateSizeWithCatfile(
			ctx,
			repo,
			s.locator,
			s.gitCmdFactory,
			s.catfileCache,
			s.txManager,
			s.housekeepingManager,
		)
		if err != nil {
			return nil, fmt.Errorf("calculating repository size with git-cat-file: %w", err)
		}

		ctxlogrus.Extract(ctx).
			WithField("repo_size_catfile", size).
			WithField("repo_size_du", duSize).
			Info("repository size calculated")
	} else {
		size = duSize
	}

	return &gitalypb.RepositorySizeResponse{Size: size}, nil
}

// calculateSizeWithCatfile calculates the repository size using git-cat-file.
// In the case the repository belongs to a pool, it will subract the total
// size of the pool repository objects from its total size. One limitation of
// this approach is that we don't distinguish whether an object in the pool
// repository belongs to the fork repository, so in fact we may end up with a
// smaller total size and theoretically could go negative.
func calculateSizeWithCatfile(
	ctx context.Context,
	repo *localrepo.Repo,
	locator storage.Locator,
	gitCmdFactory git.CommandFactory,
	catfileCache catfile.Cache,
	txManager transaction.Manager,
	housekeepingManager housekeeping.Manager,
) (int64, error) {
	var size int64

	catfileInfoIterator := gitpipe.CatfileInfoAllObjects(
		ctx,
		repo,
	)

	for catfileInfoIterator.Next() {
		size += catfileInfoIterator.Result().ObjectSize()
	}

	if err := catfileInfoIterator.Err(); err != nil {
		return 0, err
	}

	var poolSize int64

	if pool, err := objectpool.FromRepo(
		locator,
		gitCmdFactory,
		catfileCache,
		txManager,
		housekeepingManager,
		repo,
	); err == nil && pool != nil {
		catfileInfoIterator = gitpipe.CatfileInfoAllObjects(
			ctx,
			pool.Repo,
		)

		for catfileInfoIterator.Next() {
			poolSize += catfileInfoIterator.Result().ObjectSize()
		}

		if err := catfileInfoIterator.Err(); err != nil {
			return 0, err
		}
	}

	size -= poolSize
	// return the size in kb
	return size / 1024, nil
}

func calculateSizeWithRevlist(ctx context.Context, repo *localrepo.Repo) (int64, error) {
	var excludes []string
	for refPrefix := range git.InternalRefPrefixes {
		excludes = append(excludes, refPrefix+"*")
	}

	size, err := repo.Size(
		ctx,
		localrepo.WithExcludeRefs(excludes...),
		localrepo.WithoutAlternates(),
	)
	if err != nil {
		return 0, err
	}

	// return the size in kb to remain consistent
	return (size / 1024), nil
}

func (s *server) GetObjectDirectorySize(ctx context.Context, in *gitalypb.GetObjectDirectorySizeRequest) (*gitalypb.GetObjectDirectorySizeResponse, error) {
	repo := s.localrepo(in.GetRepository())

	path, err := repo.ObjectDirectoryPath()
	if err != nil {
		return nil, err
	}

	return &gitalypb.GetObjectDirectorySizeResponse{Size: getPathSize(ctx, path)}, nil
}

func getPathSize(ctx context.Context, path string) int64 {
	cmd, err := command.New(ctx, []string{"du", "-sk", path})
	if err != nil {
		ctxlogrus.Extract(ctx).WithError(err).Warn("ignoring du command error")
		return 0
	}

	sizeLine, err := io.ReadAll(cmd)
	if err != nil {
		ctxlogrus.Extract(ctx).WithError(err).Warn("ignoring command read error")
		return 0
	}

	if err := cmd.Wait(); err != nil {
		ctxlogrus.Extract(ctx).WithError(err).Warn("ignoring du wait error")
		return 0
	}

	sizeParts := bytes.Split(sizeLine, []byte("\t"))
	if len(sizeParts) != 2 {
		ctxlogrus.Extract(ctx).Warn(fmt.Sprintf("ignoring du malformed output: %q", sizeLine))
		return 0
	}

	size, err := strconv.ParseInt(string(sizeParts[0]), 10, 0)
	if err != nil {
		ctxlogrus.Extract(ctx).WithError(err).Warn("ignoring parsing size error")
		return 0
	}

	return size
}
