package commit

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type findCommitSender struct {
	commitPointer **pb.GitCommit
}

func (s *server) FindCommit(ctx context.Context, in *pb.FindCommitRequest) (*pb.FindCommitResponse, error) {
	if err := validateRevision(in.GetRevision()); err != nil {
		return nil, grpc.Errorf(codes.InvalidArgument, "FindCommit: revision: %v", err)
	}
	var commit *pb.GitCommit
	writer := newCommitsWriter(&findCommitSender{&commit})

	if err := gitLog(ctx, writer, in.GetRepository(), [][]byte{in.GetRevision()}, "--max-count=1"); err != nil {
		return nil, err
	}

	return &pb.FindCommitResponse{Commit: commit}, nil
}

func (sender *findCommitSender) Send(commits []*pb.GitCommit) error {
	// Since FindCommit's response is not streamed this is not actually
	// _sending_ anything. We just set the commit for the caller to return it.
	if len(commits) > 0 {
		*sender.commitPointer = commits[0]
	}
	return nil
}
