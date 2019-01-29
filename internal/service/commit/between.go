package commit

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper/chunk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type commitsBetweenSender struct {
	stream  gitalypb.CommitService_CommitsBetweenServer
	commits []*gitalypb.GitCommit
}

func (sender *commitsBetweenSender) Reset() { sender.commits = nil }
func (sender *commitsBetweenSender) Append(it chunk.Item) {
	sender.commits = append(sender.commits, it.(*gitalypb.GitCommit))
}
func (sender *commitsBetweenSender) Send() error {
	return sender.stream.Send(&gitalypb.CommitsBetweenResponse{Commits: sender.commits})
}

func (s *server) CommitsBetween(in *gitalypb.CommitsBetweenRequest, stream gitalypb.CommitService_CommitsBetweenServer) error {
	if err := git.ValidateRevision(in.GetFrom()); err != nil {
		return status.Errorf(codes.InvalidArgument, "CommitsBetween: from: %v", err)
	}
	if err := git.ValidateRevision(in.GetTo()); err != nil {
		return status.Errorf(codes.InvalidArgument, "CommitsBetween: to: %v", err)
	}

	sender := &commitsBetweenSender{stream: stream}
	revisionRange := fmt.Sprintf("%s..%s", in.GetFrom(), in.GetTo())

	return sendCommits(stream.Context(), sender, in.GetRepository(), []string{revisionRange}, nil, "--reverse")
}
