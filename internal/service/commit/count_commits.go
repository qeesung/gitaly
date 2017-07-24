package commit

import (
	"bufio"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/internal/helper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func (s *server) CountCommits(ctx context.Context, in *pb.CountCommitsRequest) (*pb.CountCommitsResponse, error) {
	if err := validateCountCommitsRequest(in); err != nil {
		return nil, grpc.Errorf(codes.InvalidArgument, "CountCommits: %v", err)
	}

	repoPath, err := helper.GetRepoPath(in.Repository)
	if err != nil {
		return nil, err
	}

	cmd, err := helper.GitCommandReader(ctx, "--git-dir", repoPath, "rev-list", string(in.GetRevision()))
	if err != nil {
		return nil, grpc.Errorf(codes.Internal, "CountCommits: cmd: %v", err)
	}
	defer cmd.Kill()

	count := 0
	scanner := bufio.NewScanner(cmd)
	for scanner.Scan() {
		count++
	}

	if err := scanner.Err(); err != nil {
		grpc_logrus.Extract(ctx).WithError(err).Info("ignoring scanner error")
		count = 0
	}

	if err := cmd.Wait(); err != nil {
		grpc_logrus.Extract(ctx).WithError(err).Info("ignoring git rev-list error")
		count = 0
	}

	return &pb.CountCommitsResponse{Count: int32(count)}, nil
}

func validateCountCommitsRequest(in *pb.CountCommitsRequest) error {
	if len(in.GetRevision()) == 0 {
		return fmt.Errorf("empty Revision")
	}

	return nil
}
