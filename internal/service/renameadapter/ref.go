package renameadapter

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"golang.org/x/net/context"
)

type refAdapter struct {
	upstream pb.RefServiceServer
}

func (s *refAdapter) FindDefaultBranchName(ctx context.Context, req *pb.FindDefaultBranchNameRequest) (*pb.FindDefaultBranchNameResponse, error) {
	return s.upstream.FindDefaultBranchName(ctx, req)
}

func (s *refAdapter) FindAllBranchNames(req *pb.FindAllBranchNamesRequest, res pb.Ref_FindAllBranchNamesServer) error {
	return s.upstream.FindAllBranchNames(req, res)
}
func (s *refAdapter) FindAllTagNames(req *pb.FindAllTagNamesRequest, res pb.Ref_FindAllTagNamesServer) error {
	return s.upstream.FindAllTagNames(req, res)
}
func (s *refAdapter) FindRefName(ctx context.Context, req *pb.FindRefNameRequest) (*pb.FindRefNameResponse, error) {
	return s.upstream.FindRefName(ctx, req)
}
func (s *refAdapter) FindLocalBranches(req *pb.FindLocalBranchesRequest, res pb.Ref_FindLocalBranchesServer) error {
	return s.upstream.FindLocalBranches(req, res)
}

// NewRefAdapter adapts RefServiceServer to RefServer
func NewRefAdapter(upstream pb.RefServiceServer) pb.RefServer {
	return &refAdapter{upstream}
}
