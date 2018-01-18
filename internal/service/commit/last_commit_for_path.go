package commit

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/internal/git/log"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) LastCommitForPath(ctx context.Context, in *pb.LastCommitForPathRequest) (*pb.LastCommitForPathResponse, error) {
	if err := validateLastCommitForPathRequest(in); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "LastCommitForPath: %v", err)
	}

	path := string(in.GetPath())
	if len(path) == 0 || path == "/" {
		path = "."
	}

	commit, err := log.GetCommit(ctx, in.GetRepository(), string(in.GetRevision()), path)
	if err != nil {
		return nil, err
	}

	return &pb.LastCommitForPathResponse{Commit: commit}, nil
}

func validateLastCommitForPathRequest(in *pb.LastCommitForPathRequest) error {
	if len(in.Revision) == 0 {
		return fmt.Errorf("empty Revision")
	}
	return nil
}
