package commit

import (
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	gitlog "gitlab.com/gitlab-org/gitaly/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func (s *server) ListCommitsByRefName(in *gitalypb.ListCommitsByRefNameRequest, stream gitalypb.CommitService_ListCommitsByRefNameServer) error {
	ctx := stream.Context()

	c, err := catfile.New(ctx, in.Repository)
	if err != nil {
		return helper.ErrInternal(err)
	}

	sender := chunk.New(&commitsByRefNameSender{stream: stream})

	for _, refName := range in.RefNames {
		commit, err := gitlog.GetCommitCatfile(c, string(refName))
		if catfile.IsNotFound(err) {
			continue
		}
		if err != nil {
			return helper.ErrInternal(err)
		}

		if err := sender.Send(commit); err != nil {
			return helper.ErrInternal(err)
		}
	}

	return sender.Flush()
}

type commitsByRefNameSender struct {
	response *gitalypb.ListCommitsByRefNameResponse
	stream   gitalypb.CommitService_ListCommitsByRefNameServer
}

func (c *commitsByRefNameSender) Append(it chunk.Item) {
	c.response.Commits = append(c.response.Commits, it.(*gitalypb.GitCommit))
}

func (c *commitsByRefNameSender) Send() error { return c.stream.Send(c.response) }
func (c *commitsByRefNameSender) Reset()      { c.response = &gitalypb.ListCommitsByRefNameResponse{} }
