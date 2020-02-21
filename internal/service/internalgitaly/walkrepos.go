package internalgitaly

import (
	"context"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/log"
	"gitlab.com/gitlab-org/gitaly/internal/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) WalkRepos(req *proto.WalkReposRequest, stream proto.InternalGitaly_WalkReposServer) error {
	sPath, err := s.storagePath(req.GetStorageName())
	if err != nil {
		return err
	}

	return walkStorage(stream.Context(), sPath, stream)
}

func (s *server) storagePath(storageName string) (string, error) {
	for _, storage := range s.storages {
		if storage.Name == storageName {
			return storage.Path, nil
		}
	}
	return "", status.Errorf(
		codes.NotFound,
		"storage name %q not found", storageName,
	)
}

func walkStorage(ctx context.Context, storagePath string, stream proto.InternalGitaly_WalkReposServer) error {
	return filepath.Walk(storagePath, func(path string, info os.FileInfo, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// keep walking
		}

		log.Default().WithField("path", path).Debug("walking dir")
		if helper.IsGitDirectory(path) {
			relPath, err := filepath.Rel(storagePath, path)
			if err != nil {
				return err
			}

			return stream.Send(&proto.WalkReposResponse{
				RelativePath: relPath,
			})
		}

		return nil
	})
}
