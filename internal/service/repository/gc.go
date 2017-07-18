package repository

import (
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

func (server) GarbageCollect(ctx context.Context, in *pb.GarbageCollectRequest) (*pb.GarbageCollectResponse, error) {
	repoPath, err := helper.GetRepoPath(in.GetRepository())
	if err != nil {
		return nil, err
	}

	args := []string{"-C", repoPath, "-c"}
	if in.GetCreateBitmap() {
		args = append(args, "repack.writeBitmaps=true")
	} else {
		args = append(args, "repack.writeBitmaps=false")
	}
	args = append(args, "gc")
	cmd, err := helper.GitCommandReader(args...)
	if err != nil {
		return nil, grpc.Errorf(codes.Internal, err.Error())
	}
	if err := cmd.Wait(); err != nil {
		return nil, grpc.Errorf(codes.Internal, err.Error())
	}

	return &pb.GarbageCollectResponse{}, nil
}
