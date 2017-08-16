package commit

import (
	"fmt"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type commitsBetweenSender struct {
	stream pb.CommitService_CommitsBetweenServer
}

func (s *server) CommitsBetween(in *pb.CommitsBetweenRequest, stream pb.CommitService_CommitsBetweenServer) error {
	if err := git.ValidateRevision(in.GetFrom()); err != nil {
		return grpc.Errorf(codes.InvalidArgument, "CommitsBetween: from: %v", err)
	}
	if err := git.ValidateRevision(in.GetTo()); err != nil {
		return grpc.Errorf(codes.InvalidArgument, "CommitsBetween: to: %v", err)
	}

	writer := newCommitsWriter(&commitsBetweenSender{stream})
	revisionRange := fmt.Sprintf("%s..%s", in.GetFrom(), in.GetTo())

	return gitLog(stream.Context(), writer, in.GetRepository(), []string{revisionRange}, nil, "--reverse")
}

func (sender *commitsBetweenSender) Send(commits []*pb.GitCommit) error {
	return sender.stream.Send(&pb.CommitsBetweenResponse{Commits: commits})
}
