package repository

import (
	log "github.com/Sirupsen/logrus"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

func (server) RepackFull(ctx context.Context, in *pb.RepackFullRequest) (*pb.RepackFullResponse, error) {
	if err := repackCommand(ctx, "RepackFull", in.GetRepository(), in.GetCreateBitmap(), "-A", "--pack-kept-objects"); err != nil {
		return nil, err
	}
	return &pb.RepackFullResponse{}, nil
}

func (server) RepackIncremental(ctx context.Context, in *pb.RepackIncrementalRequest) (*pb.RepackIncrementalResponse, error) {
	if err := repackCommand(ctx, "RepackIncremental", in.GetRepository(), false); err != nil {
		return nil, err
	}
	return &pb.RepackIncrementalResponse{}, nil
}

func repackCommand(ctx context.Context, rpcName string, repo *pb.Repository, bitmap bool, args ...string) error {
	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"WriteBitmaps": bitmap,
	}).Debug(rpcName)

	repoPath, err := helper.GetRepoPath(repo)
	if err != nil {
		return err
	}

	var cmdArgs []string
	if bitmap {
		cmdArgs = []string{"-C", repoPath, "-c", "repack.writeBitmaps=true", "repack", "-d"}
	} else {
		cmdArgs = []string{"-C", repoPath, "-c", "repack.writeBitmaps=false", "repack", "-d"}
	}
	cmdArgs = append(cmdArgs, args...)

	cmd, err := command.Git(ctx, cmdArgs...)
	if err != nil {
		return grpc.Errorf(codes.Internal, err.Error())
	}

	if err := cmd.Wait(); err != nil {
		return grpc.Errorf(codes.Internal, err.Error())
	}

	return nil
}
