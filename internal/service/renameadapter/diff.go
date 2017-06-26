package renameadapter

import pb "gitlab.com/gitlab-org/gitaly-proto/go"

type diffAdapter struct {
	upstream pb.DiffServiceServer
}

func (s *diffAdapter) CommitDiff(req *pb.CommitDiffRequest, res pb.Diff_CommitDiffServer) error {
	return s.upstream.CommitDiff(req, res)
}

func (s *diffAdapter) CommitDelta(req *pb.CommitDeltaRequest, res pb.Diff_CommitDeltaServer) error {
	return s.upstream.CommitDelta(req, res)
}

// NewDiffAdapter adapts DiffServiceServer to DiffServer
func NewDiffAdapter(upstream pb.DiffServiceServer) pb.DiffServer {
	return &diffAdapter{upstream}
}
