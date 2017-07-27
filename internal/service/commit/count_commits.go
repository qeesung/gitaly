package commit

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"strconv"

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

	cmdArgs := []string{"--git-dir", repoPath, "rev-list", "--count", string(in.GetRevision())}

	cmd, err := helper.GitCommandReader(ctx, cmdArgs...)
	if err != nil {
		return nil, grpc.Errorf(codes.Internal, "CountCommits: cmd: %v", err)
	}
	defer cmd.Kill()

	var count int64
	countStr, readAllErr := ioutil.ReadAll(cmd)
	if readAllErr != nil {
		grpc_logrus.Extract(ctx).WithError(err).Info("ignoring git rev-list error")
	}

	if err := cmd.Wait(); err != nil {
		grpc_logrus.Extract(ctx).WithError(err).Info("ignoring git rev-list error")
		count = 0
	} else if readAllErr == nil {
		var err error
		countStr = bytes.TrimSpace(countStr)
		count, err = strconv.ParseInt(string(countStr), 10, 0)

		if err != nil {
			return nil, grpc.Errorf(codes.Internal, "CountCommits: parse count: %v", err)
		}
	}

	return &pb.CountCommitsResponse{Count: int32(count)}, nil
}

func validateCountCommitsRequest(in *pb.CountCommitsRequest) error {
	if len(in.GetRevision()) == 0 {
		return fmt.Errorf("empty Revision")
	}

	return nil
}
