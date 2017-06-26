package renameadapter

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"golang.org/x/net/context"
)

type commitAdapter struct {
	upstream pb.CommitServiceServer
}

func (s *commitAdapter) CommitIsAncestor(ctx context.Context, req *pb.CommitIsAncestorRequest) (*pb.CommitIsAncestorResponse, error) {
	return s.upstream.CommitIsAncestor(ctx, req)
}

func (s *commitAdapter) TreeEntry(req *pb.TreeEntryRequest, res pb.Commit_TreeEntryServer) error {
	return s.upstream.TreeEntry(req, res)
}

// NewCommitAdapter adapts CommitServiceServer to CommitServer
func NewCommitAdapter(upstream pb.CommitServiceServer) pb.CommitServer {
	return &commitAdapter{upstream}
}
