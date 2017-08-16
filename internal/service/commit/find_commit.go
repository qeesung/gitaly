package commit

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type findCommitSender struct {
	commit *pb.GitCommit
}

func (s *server) FindCommit(ctx context.Context, in *pb.FindCommitRequest) (*pb.FindCommitResponse, error) {
	if err := git.ValidateRevision(in.GetRevision()); err != nil {
		return nil, grpc.Errorf(codes.InvalidArgument, "FindCommit: revision: %v", err)
	}

	sender := &findCommitSender{}
	writer := newCommitsWriter(sender)
	revisions := []string{string(in.GetRevision())}
	if err := gitLog(ctx, writer, in.GetRepository(), revisions, nil, "--max-count=1"); err != nil {
		return nil, err
	}

	return &pb.FindCommitResponse{Commit: sender.commit}, nil
}

func (sender *findCommitSender) Send(commits []*pb.GitCommit) error {
	// Since FindCommit's response is not streamed this is not actually
	// _sending_ anything. We just set the commit for the caller to return it.
	if len(commits) > 0 {
		sender.commit = commits[0]
	}
	return nil
}
