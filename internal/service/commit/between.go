package commit

import (
	"bytes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type commitsBetweenSender struct {
	stream pb.CommitService_CommitsBetweenServer
}

func (s *server) CommitsBetween(in *pb.CommitsBetweenRequest, stream pb.CommitService_CommitsBetweenServer) error {
	if err := validateRevision(in.GetFrom()); err != nil {
		return grpc.Errorf(codes.InvalidArgument, "CommitsBetween: from: %v", err)
	}
	if err := validateRevision(in.GetTo()); err != nil {
		return grpc.Errorf(codes.InvalidArgument, "CommitsBetween: to: %v", err)
	}

	writer := newCommitsWriter(&commitsBetweenSender{stream})
	revisionRange := bytes.Join([][]byte{
		in.GetFrom(),
		{'.', '.'},
		in.GetTo(),
	}, nil)

	gitLogExtraArgs := []string{"--reverse"}

	return gitLog(stream.Context(), writer, in.GetRepository(), [][]byte{revisionRange}, gitLogExtraArgs...)
}

func (sender *commitsBetweenSender) Send(commits []*pb.GitCommit) error {
	return sender.stream.Send(&pb.CommitsBetweenResponse{Commits: commits})
}
