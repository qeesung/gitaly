package commit

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/helper/chunk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type commitsByMessageSender struct {
	stream  gitalypb.CommitService_CommitsByMessageServer
	commits []*gitalypb.GitCommit
}

func (sender *commitsByMessageSender) Reset() { sender.commits = nil }
func (sender *commitsByMessageSender) Append(it chunk.Item) {
	sender.commits = append(sender.commits, it.(*gitalypb.GitCommit))
}
func (sender *commitsByMessageSender) Send() error {
	return sender.stream.Send(&gitalypb.CommitsByMessageResponse{Commits: sender.commits})
}

func (s *server) CommitsByMessage(in *gitalypb.CommitsByMessageRequest, stream gitalypb.CommitService_CommitsByMessageServer) error {
	if err := validateCommitsByMessageRequest(in); err != nil {
		return status.Errorf(codes.InvalidArgument, "CommitsByMessage: %v", err)
	}

	ctx := stream.Context()
	sender := &commitsByMessageSender{stream: stream}

	gitLogExtraOptions := []string{
		"--grep=" + in.GetQuery(),
		"--regexp-ignore-case",
	}
	if offset := in.GetOffset(); offset > 0 {
		gitLogExtraOptions = append(gitLogExtraOptions, fmt.Sprintf("--skip=%d", offset))
	}
	if limit := in.GetLimit(); limit > 0 {
		gitLogExtraOptions = append(gitLogExtraOptions, fmt.Sprintf("--max-count=%d", limit))
	}

	revision := in.GetRevision()
	if len(revision) == 0 {
		var err error

		revision, err = defaultBranchName(ctx, in.Repository)
		if err != nil {
			if _, ok := status.FromError(err); ok {
				return err
			}
			return status.Errorf(codes.Internal, "CommitsByMessage: defaultBranchName: %v", err)
		}
	}

	var paths []string
	if path := in.GetPath(); len(path) > 0 {
		paths = append(paths, string(path))
	}

	return sendCommits(stream.Context(), sender, in.GetRepository(), []string{string(revision)}, paths, gitLogExtraOptions...)
}

func validateCommitsByMessageRequest(in *gitalypb.CommitsByMessageRequest) error {
	if in.GetQuery() == "" {
		return fmt.Errorf("empty Query")
	}

	return nil
}
