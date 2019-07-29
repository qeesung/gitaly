package storage

import (
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) ListDirectories(req *gitalypb.ListDirectoriesRequest, stream gitalypb.StorageService_ListDirectoriesServer) error {
	storageDir, err := helper.GetStorageByName(req.StorageName)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "storage lookup failed: %v", err)
	}

	storageDir = storageDir + "/"
	maxDepth := dirDepth(storageDir) + req.GetDepth()
	sender := chunk.New(&dirSender{stream: stream})

	err = filepath.Walk(storageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			relPath := strings.TrimPrefix(path, storageDir)
			if relPath == "" {
				return nil
			}

			sender.Send(relPath)

			if dirDepth(path)+1 > maxDepth {
				return filepath.SkipDir
			}

			return nil
		}

		return nil
	})

	if err != nil {
		return err
	}

	return sender.Flush()
}

func dirDepth(dir string) uint32 {
	return uint32(len(strings.Split(dir, string(os.PathSeparator)))) + 1
}

type dirSender struct {
	stream gitalypb.StorageService_ListDirectoriesServer
	dirs   []string
}

func (s *dirSender) Reset()               { s.dirs = nil }
func (s *dirSender) Append(it chunk.Item) { s.dirs = append(s.dirs, it.(string)) }
func (s *dirSender) Send() error {
	return s.stream.Send(&gitalypb.ListDirectoriesResponse{Paths: s.dirs})
}
