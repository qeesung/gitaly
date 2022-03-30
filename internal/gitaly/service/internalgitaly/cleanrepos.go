package internalgitaly

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

func (s *server) CleanRepos(ctx context.Context, req *gitalypb.CleanReposRequest) (*gitalypb.CleanReposResponse, error) {
	sPath, err := s.storagePath(req.GetStorageName())
	if err != nil {
		return nil, err
	}

	directory := filepath.Join(
		sPath,
		"lost+found",
		time.Now().Format("2006-01-02"),
	)

	if err := os.MkdirAll(directory, 0o750); err != nil {
		return nil, err
	}

	for _, relPath := range req.GetRelativePaths() {
		if err := os.Rename(
			filepath.Join(sPath, relPath),
			filepath.Join(directory, relPath),
		); err != nil {
			return nil, err
		}
	}

	return &gitalypb.CleanReposResponse{}, nil
}
