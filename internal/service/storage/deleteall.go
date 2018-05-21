package storage

import (
	"io"
	"os"
	"path"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/tempdir"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"golang.org/x/net/context"
)

func (s *server) DeleteAllRepositories(ctx context.Context, req *pb.DeleteAllRepositoriesRequest) (*pb.DeleteAllRepositoriesResponse, error) {
	storageDir, err := helper.GetStorageByName(req.StorageName)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "storage lookup failed: %v", err)
	}

	tempReposDir, err := tempdir.ForDeleteAllRepositories(req.StorageName)
	if err != nil {
		status.Errorf(codes.Internal, "create temp dir: %v", err)
	}

	dir, err := os.Open(storageDir)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "open storage dir: %v", err)
	}

	for err != io.EOF {
		var dirents []os.FileInfo
		dirents, err = dir.Readdir(100)
		if err != nil && err != io.EOF {
			return nil, status.Errorf(codes.Internal, "read storage dir: %v", err)
		}

		for _, d := range dirents {
			if d.Name() == tempdir.GitalyDataPrefix {
				continue
			}

			if err := os.Rename(path.Join(storageDir, d.Name()), path.Join(tempReposDir, d.Name())); err != nil {
				return nil, status.Errorf(codes.Internal, "move dir: %v", err)
			}
		}
	}

	return &pb.DeleteAllRepositoriesResponse{}, nil
}
