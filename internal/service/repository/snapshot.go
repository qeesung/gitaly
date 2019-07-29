package repository

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/archive"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

var objectFiles = []*regexp.Regexp{
	regexp.MustCompile(`/[[:xdigit:]]{2}/[[:xdigit:]]{38}\z`),
	regexp.MustCompile(`/pack/pack\-[[:xdigit:]]{40}\.(pack|idx)\z`),
}

func (s *server) GetSnapshot(in *gitalypb.GetSnapshotRequest, stream gitalypb.RepositoryService_GetSnapshotServer) error {
	path, err := helper.GetRepoPath(in.Repository)
	if err != nil {
		return err
	}

	writer := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.GetSnapshotResponse{Data: p})
	})

	// Building a raw archive may race with `git push`, but GitLab can enforce
	// concurrency control if necessary. Using `TarBuilder` means we can keep
	// going even if some files are added or removed during the operation.
	builder := archive.NewTarBuilder(path, writer)

	// Pick files directly by filename so we can get a snapshot even if the
	// repository is corrupted. https://gitirc.eu/gitrepository-layout.html
	// documents the various files and directories. We exclude the following
	// on purpose:
	//
	//   * branches - legacy, not replicated by git fetch
	//   * commondir - may differ between sites
	//   * config - may contain credentials, and cannot be managed by client
	//   * custom-hooks - GitLab-specific, no supported in Geo, may differ between sites
	//   * hooks - symlink, may differ between sites
	//   * {shared,}index[.*] - not found in bare repositories
	//   * info/{attributes,exclude,grafts} - not replicated by git fetch
	//   * info/refs - dumb protocol only
	//   * logs/* - not replicated by git fetch
	//   * modules/* - not replicated by git fetch
	//   * objects/info/* - unneeded (dumb protocol) or to do with alternates
	//   * worktrees/* - not replicated by git fetch

	// References
	builder.FileIfExist("HEAD")
	builder.FileIfExist("packed-refs")
	builder.RecursiveDirIfExist("refs")
	builder.RecursiveDirIfExist("branches")

	// The packfiles + any loose objects.
	builder.RecursiveDirIfExist("objects", objectFiles...)

	// In case this repository is a shallow clone. Seems unlikely, but better
	// safe than sorry.
	builder.FileIfExist("shallow")

	if err := addAlternateFiles(stream.Context(), in.GetRepository(), builder); err != nil {
		return helper.ErrInternal(err)
	}

	if err := builder.Close(); err != nil {
		return helper.ErrInternal(fmt.Errorf("building snapshot failed: %v", err))
	}

	return nil
}

func addAlternateFiles(ctx context.Context, repository *gitalypb.Repository, builder *archive.TarBuilder) error {
	alternateFilePath, err := git.InfoAlternatesPath(repository)
	if err != nil {
		return fmt.Errorf("error when getting alternates file path: %v", err)
	}

	if stat, err := os.Stat(alternateFilePath); err == nil && stat.Size() > 0 {
		alternatesFile, err := os.Open(alternateFilePath)
		if err != nil {
			grpc_logrus.Extract(ctx).WithField("error", err).Warn("error opening alternates file")
			return nil
		}
		defer alternatesFile.Close()

		alternateObjDir, err := bufio.NewReader(alternatesFile).ReadString('\n')
		if err != nil && err != io.EOF {
			grpc_logrus.Extract(ctx).WithField("error", err).Warn("error reading alternates file")
			return nil
		}

		if err == nil {
			alternateObjDir = alternateObjDir[:len(alternateObjDir)-1]
		}

		if stat, err := os.Stat(alternateObjDir); err != nil || !stat.IsDir() {
			grpc_logrus.Extract(ctx).WithFields(
				log.Fields{"error": err, "object_dir": alternateObjDir}).Warn("error reading alternate objects directory")
			return nil
		}

		if err := walkAndAddToBuilder(alternateObjDir, builder); err != nil {
			return fmt.Errorf("walking alternates file: %v", err)
		}
	}
	return nil
}

func walkAndAddToBuilder(alternateObjDir string, builder *archive.TarBuilder) error {

	matchWalker := archive.NewMatchWalker(objectFiles, func(path string, info os.FileInfo, err error) error {
		fmt.Printf("walking down %v\n", path)
		if err != nil {
			return fmt.Errorf("error walking %v: %v", path, err)
		}

		relPath, err := filepath.Rel(alternateObjDir, path)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("opening file %s: %v", path, err)
		}
		defer file.Close()

		objectPath := filepath.Join("objects", relPath)

		if err := builder.VirtualFileWithContents(objectPath, file); err != nil {
			return fmt.Errorf("expected file %v to exist: %v", path, err)
		}

		return nil
	})

	if err := filepath.Walk(alternateObjDir, matchWalker.Walk); err != nil {
		return fmt.Errorf("error when traversing: %v", err)
	}

	return nil
}
