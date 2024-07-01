package repository

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// RepositorySize returns the size of the specified repository in kibibytes. By default, this
// calculation is performed using the disk usage command.
func (s *server) RepositorySize(ctx context.Context, in *gitalypb.RepositorySizeRequest) (*gitalypb.RepositorySizeResponse, error) {
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}
	repo := s.localrepo(repository)

	path, err := repo.Path(ctx)
	if err != nil {
		return nil, err
	}

	sizeInBytes, err := dirSizeInBytes(path)
	if err != nil {
		return nil, fmt.Errorf("calculating directory size: %w", err)
	}

	return &gitalypb.RepositorySizeResponse{Size: sizeInBytes / 1024}, nil
}

func (s *server) GetObjectDirectorySize(ctx context.Context, in *gitalypb.GetObjectDirectorySizeRequest) (*gitalypb.GetObjectDirectorySizeResponse, error) {
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}
	repo := s.localrepo(repository)

	path, err := repo.ObjectDirectoryPath(ctx)
	if err != nil {
		return nil, err
	}

	sizeInBytes, err := dirSizeInBytes(path)
	if err != nil {
		return nil, fmt.Errorf("calculating directory size: %w", err)
	}

	return &gitalypb.GetObjectDirectorySizeResponse{Size: sizeInBytes / 1024}, nil
}

func dirSizeInBytes(path string) (int64, error) {
	var totalSize int64

	if err := filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// It can happen that we try to walk a directory like the object shards or
			// an empty reference directory that gets deleted concurrently. This is fine
			// and expected to happen, so let's ignore any such errors.
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}

			return err
		}

		if d.IsDir() {
			return nil
		}

		fi, err := d.Info()
		if err != nil {
			// The file may have been concurrently removed.
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}

			return fmt.Errorf("retrieving file info: %w", err)
		}

		totalSize += fi.Size()

		return nil
	}); err != nil {
		return 0, fmt.Errorf("walking directory: %w", err)
	}

	return totalSize, nil
}
