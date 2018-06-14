package commit

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/log"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) FindCommit(ctx context.Context, in *pb.FindCommitRequest) (*pb.FindCommitResponse, error) {
	revision := in.GetRevision()
	if err := git.ValidateRevision(revision); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "FindCommit: revision: %v", err)
	}

	repo := in.GetRepository()

	commit, err := log.GetCommit(ctx, repo, string(revision), "")
	return &pb.FindCommitResponse{Commit: commit}, err
}
