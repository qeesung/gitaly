package commit

import (
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	gitlog "gitlab.com/gitlab-org/gitaly/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/internal/helper/chunker"
)

func (s *server) ListCommitsByOid(in *gitalypb.ListCommitsByOidRequest, stream gitalypb.CommitService_ListCommitsByOidServer) error {
	ctx := stream.Context()

	c, err := catfile.New(ctx, in.Repository)
	if err != nil {
		return err
	}

	sender := &chunker.Sender{
		NewResponse: func() chunker.Response {
			return &commitsByOidResponse{
				stream:   stream,
				response: &gitalypb.ListCommitsByOidResponse{},
			}
		},
	}

	for _, oid := range in.Oid {
		commit, err := gitlog.GetCommitCatfile(c, oid)
		if err != nil {
			return err
		}

		if commit == nil {
			continue
		}

		if err := sender.Send(commit); err != nil {
			return err
		}
	}

	return sender.Flush()
}

type commitsByOidResponse struct {
	response *gitalypb.ListCommitsByOidResponse
	stream   gitalypb.CommitService_ListCommitsByOidServer
}

func (c *commitsByOidResponse) Append(it chunker.Item) {
	c.response.Commits = append(c.response.Commits, it.(*gitalypb.GitCommit))
}

func (c *commitsByOidResponse) Send() error { return c.stream.Send(c.response) }
