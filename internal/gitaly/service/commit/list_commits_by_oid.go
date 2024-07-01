package commit

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

var listCommitsbyOidHistogram = promauto.NewHistogram(
	prometheus.HistogramOpts{
		Name: "gitaly_list_commits_by_oid_request_size",
		Help: "Number of commits requested in a ListCommitsByOid request",

		// We want to count the pathological case where the request is empty. I
		// am not sure if with floats, Observe(0) would go into bucket 0. Use
		// bucket 0.001 because 0 <= 0.001 for sure.
		Buckets: []float64{0.001, 1, 5, 10, 20},
	})

func (s *server) ListCommitsByOid(in *gitalypb.ListCommitsByOidRequest, stream gitalypb.CommitService_ListCommitsByOidServer) error {
	ctx := stream.Context()
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}
	repo := s.localrepo(repository)

	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return err
	}
	defer cancel()

	sender := chunk.New(&commitsByOidSender{stream: stream})
	listCommitsbyOidHistogram.Observe(float64(len(in.Oid)))

	for _, oid := range in.Oid {
		commit, err := catfile.GetCommit(ctx, objectReader, git.Revision(oid))
		if errors.As(err, &catfile.NotFoundError{}) {
			continue
		}
		if err != nil {
			return err
		}

		if err := sender.Send(commit.GitCommit); err != nil {
			return err
		}
	}

	return sender.Flush()
}

type commitsByOidSender struct {
	response *gitalypb.ListCommitsByOidResponse
	stream   gitalypb.CommitService_ListCommitsByOidServer
}

func (c *commitsByOidSender) Append(m proto.Message) {
	c.response.Commits = append(c.response.Commits, m.(*gitalypb.GitCommit))
}

func (c *commitsByOidSender) Send() error { return c.stream.Send(c.response) }
func (c *commitsByOidSender) Reset()      { c.response = &gitalypb.ListCommitsByOidResponse{} }
