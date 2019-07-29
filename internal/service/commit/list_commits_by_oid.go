package commit

import (
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	gitlog "gitlab.com/gitlab-org/gitaly/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func (s *server) ListCommitsByOid(in *gitalypb.ListCommitsByOidRequest, stream gitalypb.CommitService_ListCommitsByOidServer) error {
	ctx := stream.Context()

	c, err := catfile.New(ctx, in.Repository)
	if err != nil {
		return err
	}

	sender := chunk.New(&commitsByOidSender{stream: stream})

	for _, oid := range in.Oid {
		commit, err := gitlog.GetCommitCatfile(c, oid)
		if catfile.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}

		if err := sender.Send(commit); err != nil {
			return err
		}
	}

	return sender.Flush()
}

type commitsByOidSender struct {
	response *gitalypb.ListCommitsByOidResponse
	stream   gitalypb.CommitService_ListCommitsByOidServer
}

func (c *commitsByOidSender) Append(it chunk.Item) {
	c.response.Commits = append(c.response.Commits, it.(*gitalypb.GitCommit))
}

func (c *commitsByOidSender) Send() error { return c.stream.Send(c.response) }
func (c *commitsByOidSender) Reset()      { c.response = &gitalypb.ListCommitsByOidResponse{} }
