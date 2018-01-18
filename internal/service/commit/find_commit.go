package commit

import (
	"gitlab.com/gitlab-org/gitaly/internal/git/log"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) FindCommit(ctx context.Context, in *pb.FindCommitRequest) (*pb.FindCommitResponse, error) {
	if err := git.ValidateRevision(in.GetRevision()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "FindCommit: revision: %v", err)
	}

	commit, err := log.GetCommit(ctx, in.GetRepository(), string(in.GetRevision()), "")
	if err != nil {
		return nil, err
	}

	return &pb.FindCommitResponse{Commit: commit}, nil
}
