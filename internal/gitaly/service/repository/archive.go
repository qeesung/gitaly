package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/smudge"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/log"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"google.golang.org/protobuf/proto"
)

type archiveParams struct {
	writer       io.Writer
	in           *gitalypb.GetArchiveRequest
	compressArgs []string
	format       string
	archivePath  string
	exclude      []string
	loggingDir   string
}

func (s *server) GetArchive(in *gitalypb.GetArchiveRequest, stream gitalypb.RepositoryService_GetArchiveServer) error {
	ctx := stream.Context()
	compressArgs, format := parseArchiveFormat(in.GetFormat())
	repo := s.localrepo(in.GetRepository())

	repoRoot, err := repo.Path()
	if err != nil {
		return err
	}

	path, err := storage.ValidateRelativePath(repoRoot, string(in.GetPath()))
	if err != nil {
		return helper.ErrInvalidArgument(err)
	}

	exclude := make([]string, len(in.GetExclude()))
	for i, ex := range in.GetExclude() {
		exclude[i], err = storage.ValidateRelativePath(repoRoot, string(ex))
		if err != nil {
			return helper.ErrInvalidArgument(err)
		}
	}

	if err := validateGetArchiveRequest(in, format); err != nil {
		return err
	}

	if err := s.validateGetArchivePrecondition(ctx, repo, in.GetCommitId(), path, exclude); err != nil {
		return err
	}

	if in.GetElidePath() {
		// `git archive <commit ID>:<path>` expects exclusions to be relative to path
		pathSlash := path + string(os.PathSeparator)
		for i := range exclude {
			if !strings.HasPrefix(exclude[i], pathSlash) {
				return helper.ErrInvalidArgumentf("invalid exclude: %q is not a subdirectory of %q", exclude[i], path)
			}

			exclude[i] = exclude[i][len(pathSlash):]
		}
	}

	writer := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.GetArchiveResponse{Data: p})
	})

	ctxlogrus.Extract(ctx).WithField("request_hash", requestHash(in)).Info("request details")

	return s.handleArchive(ctx, archiveParams{
		writer:       writer,
		in:           in,
		compressArgs: compressArgs,
		format:       format,
		archivePath:  path,
		exclude:      exclude,
		loggingDir:   s.loggingCfg.Dir,
	})
}

func parseArchiveFormat(format gitalypb.GetArchiveRequest_Format) ([]string, string) {
	switch format {
	case gitalypb.GetArchiveRequest_TAR:
		return nil, "tar"
	case gitalypb.GetArchiveRequest_TAR_GZ:
		return []string{"gzip", "-c", "-n"}, "tar"
	case gitalypb.GetArchiveRequest_TAR_BZ2:
		return []string{"bzip2", "-c"}, "tar"
	case gitalypb.GetArchiveRequest_ZIP:
		return nil, "zip"
	}

	return nil, ""
}

func validateGetArchiveRequest(in *gitalypb.GetArchiveRequest, format string) error {
	if err := git.ValidateRevision([]byte(in.GetCommitId())); err != nil {
		return helper.ErrInvalidArgumentf("invalid commitId: %v", err)
	}

	if len(format) == 0 {
		return helper.ErrInvalidArgumentf("invalid format")
	}

	return nil
}

func (s *server) validateGetArchivePrecondition(
	ctx context.Context,
	repo git.RepositoryExecutor,
	commitID string,
	path string,
	exclude []string,
) error {
	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return err
	}
	defer cancel()

	objectInfoReader, cancel, err := s.catfileCache.ObjectInfoReader(ctx, repo)
	if err != nil {
		return err
	}
	defer cancel()

	f := catfile.NewTreeEntryFinder(objectReader, objectInfoReader)
	if path != "." {
		if ok, err := findGetArchivePath(ctx, f, commitID, path); err != nil {
			return err
		} else if !ok {
			return helper.ErrFailedPreconditionf("path doesn't exist")
		}
	}

	for i, exclude := range exclude {
		if ok, err := findGetArchivePath(ctx, f, commitID, exclude); err != nil {
			return err
		} else if !ok {
			return helper.ErrFailedPreconditionf("exclude[%d] doesn't exist", i)
		}
	}

	return nil
}

func findGetArchivePath(ctx context.Context, f *catfile.TreeEntryFinder, commitID, path string) (ok bool, err error) {
	treeEntry, err := f.FindByRevisionAndPath(ctx, commitID, path)
	if err != nil {
		return false, err
	}

	if treeEntry == nil || len(treeEntry.Oid) == 0 {
		return false, nil
	}
	return true, nil
}

func (s *server) handleArchive(ctx context.Context, p archiveParams) error {
	var args []string
	pathspecs := make([]string, 0, len(p.exclude)+1)
	if !p.in.GetElidePath() {
		// git archive [options] <commit ID> -- <path> [exclude*]
		args = []string{p.in.GetCommitId()}
		pathspecs = append(pathspecs, p.archivePath)
	} else if p.archivePath != "." {
		// git archive [options] <commit ID>:<path> -- [exclude*]
		args = []string{p.in.GetCommitId() + ":" + p.archivePath}
	} else {
		// git archive [options] <commit ID> -- [exclude*]
		args = []string{p.in.GetCommitId()}
	}

	for _, exclude := range p.exclude {
		pathspecs = append(pathspecs, ":(exclude)"+exclude)
	}

	var env []string
	var config []git.ConfigPair

	if p.in.GetIncludeLfsBlobs() {
		smudgeCfg := smudge.Config{
			GlRepository: p.in.GetRepository().GetGlRepository(),
			Gitlab:       s.cfg.Gitlab,
			TLS:          s.cfg.TLS,
			DriverType:   smudge.DriverTypeProcess,
		}

		smudgeEnv, err := smudgeCfg.Environment()
		if err != nil {
			return fmt.Errorf("setting up smudge environment: %w", err)
		}

		smudgeGitConfig, err := smudgeCfg.GitConfiguration(s.cfg)
		if err != nil {
			return fmt.Errorf("setting up smudge gitconfig: %w", err)
		}

		env = append(
			env,
			smudgeEnv,
			fmt.Sprintf("%s=%s", log.GitalyLogDirEnvKey, p.loggingDir),
		)
		config = append(config, smudgeGitConfig)
	}

	archiveCommand, err := s.gitCmdFactory.New(ctx, p.in.GetRepository(), git.SubCmd{
		Name:        "archive",
		Flags:       []git.Option{git.ValueFlag{Name: "--format", Value: p.format}, git.ValueFlag{Name: "--prefix", Value: p.in.GetPrefix() + "/"}},
		Args:        args,
		PostSepArgs: pathspecs,
	}, git.WithEnv(env...), git.WithConfig(config...))
	if err != nil {
		return err
	}

	if len(p.compressArgs) > 0 {
		command, err := command.New(ctx, p.compressArgs,
			command.WithStdin(archiveCommand), command.WithStdout(p.writer),
		)
		if err != nil {
			return err
		}

		if err := command.Wait(); err != nil {
			return err
		}
	} else if _, err = io.Copy(p.writer, archiveCommand); err != nil {
		return err
	}

	return archiveCommand.Wait()
}

func requestHash(req proto.Message) string {
	reqBytes, err := proto.Marshal(req)
	if err != nil {
		return "failed to hash request"
	}

	hash := sha256.Sum256(reqBytes)
	return hex.EncodeToString(hash[:])
}
