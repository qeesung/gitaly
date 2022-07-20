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
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func (s *server) RepositorySize(ctx context.Context, in *gitalypb.RepositorySizeRequest) (*gitalypb.RepositorySizeResponse, error) {
	repo := s.localrepo(in.GetRepository())
	var size int64
	var err error

	var excludes []string
	for _, prefix := range git.InternalRefPrefixes {
		excludes = append(excludes, prefix+"*")
	}

	path, err := repo.Path()
	if err != nil {
		return nil, err
	}

	if featureflag.RevlistForRepoSize.IsEnabled(ctx) {
		size, err = repo.Size(
			ctx,
			localrepo.WithExcludeRefs(excludes...),
			localrepo.WithoutAlternates(),
		)
		if err != nil {
			return nil, err
		}
		// return the size in kb to remain consistent
		size = size / 1024

		duSize := getPathSize(ctx, path)

		ctxlogrus.Extract(ctx).
			WithField("repo_size_revlist", size).
			WithField("repo_size_du", duSize).Info("repository size calculated")
	} else {
		size = getPathSize(ctx, path)
	}

	return &gitalypb.RepositorySizeResponse{Size: size}, nil
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
