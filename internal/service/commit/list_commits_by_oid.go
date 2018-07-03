package commit

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	gitlog "gitlab.com/gitlab-org/gitaly/internal/git/log"
)

const batchSizeListCommitsByOid = 20

func (s *server) ListCommitsByOid(in *pb.ListCommitsByOidRequest, stream pb.CommitService_ListCommitsByOidServer) error {
	ctx := stream.Context()

	c, err := catfile.New(ctx, in.Repository)
	if err != nil {
		return err
	}

	var commits []*pb.GitCommit
	for _, oid := range in.Oid {
		commit, err := gitlog.GetCommitCatfile(c, oid)
		if err != nil {
			return err
		}

		if commit != nil {
			commits = append(commits, commit)
		}

		if len(commits) == batchSizeListCommitsByOid {
			if err := stream.Send(&pb.ListCommitsByOidResponse{Commits: commits}); err != nil {
				return err
			}
			commits = commits[:0]
		}
	}

	if len(commits) > 0 {
		return stream.Send(&pb.ListCommitsByOidResponse{Commits: commits})
	}

	return nil
}
