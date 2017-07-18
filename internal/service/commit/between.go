package commit

import (
	"bytes"
	"fmt"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type commitsBetweenSender struct {
	stream pb.CommitService_CommitsBetweenServer
}

func parseCommitsBetweenRevision(revision []byte) (string, error) {
	if len(revision) == 0 {
		return "", fmt.Errorf("empty revision")
	}
	if bytes.HasPrefix(revision, []byte("-")) {
		return "", fmt.Errorf("revision can't start with '-'")
	}

	return string(revision), nil
}

func (s *server) CommitsBetween(in *pb.CommitsBetweenRequest, stream pb.CommitService_CommitsBetweenServer) error {
	from, err := parseCommitsBetweenRevision(in.GetFrom())
	if err != nil {
		return grpc.Errorf(codes.InvalidArgument, "CommitsBetween: from: %v", err)
	}
	to, err := parseCommitsBetweenRevision(in.GetTo())
	if err != nil {
		return grpc.Errorf(codes.InvalidArgument, "CommitsBetween: to: %v", err)
	}

	writer := newCommitsWriter(&commitsBetweenSender{stream})
	revisionRange := string(from) + ".." + string(to)
	gitLogExtraArgs := []string{"--reverse"}

	return gitLog(writer, in.GetRepository(), [][]byte{[]byte(revisionRange)}, gitLogExtraArgs...)
}

func (sender *commitsBetweenSender) Send(commits []*pb.GitCommit) error {
	return sender.stream.Send(&pb.CommitsBetweenResponse{Commits: commits})
}
