package ref

import (
	"bytes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper/lines"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var localBranchFormatFields = []string{
	"%(refname)", "%(objectname)", "%(contents:subject)", "%(authorname)",
	"%(authoremail)", "%(authordate:iso-strict)", "%(committername)",
	"%(committeremail)", "%(committerdate:iso-strict)",
}

func parseRef(ref []byte) ([][]byte, error) {
	elements := bytes.Split(ref, []byte("\x00"))
	if len(elements) != 9 {
		return nil, grpc.Errorf(codes.Internal, "error parsing ref %q", ref)
	}
	return elements, nil
}

func buildCommit(elements [][]byte) (*pb.GitCommit, error) {
	return git.NewCommit(elements[0], elements[1], nil, elements[2],
		elements[3], elements[4], elements[5], elements[6], elements[7])
}

func buildLocalBranch(elements [][]byte) (*pb.FindLocalBranchResponse, error) {
	target, err := buildCommit(elements[1:])
	if err != nil {
		return nil, err
	}
	author := pb.FindLocalBranchCommitAuthor{
		Name:  target.Author.Name,
		Email: target.Author.Email,
		Date:  target.Author.Date,
	}
	committer := pb.FindLocalBranchCommitAuthor{
		Name:  target.Committer.Name,
		Email: target.Committer.Email,
		Date:  target.Committer.Date,
	}

	return &pb.FindLocalBranchResponse{
		Name:            elements[0],
		CommitId:        target.Id,
		CommitSubject:   target.Subject,
		CommitAuthor:    &author,
		CommitCommitter: &committer,
	}, nil
}

func buildBranch(elements [][]byte) (*pb.FindAllBranchesResponse_Branch, error) {
	target, err := buildCommit(elements[1:])
	if err != nil {
		return nil, err
	}

	return &pb.FindAllBranchesResponse_Branch{
		Name:   elements[0],
		Target: target,
	}, nil
}

func newFindAllBranchNamesWriter(stream pb.Ref_FindAllBranchNamesServer) lines.Sender {
	return func(refs [][]byte) error {
		return stream.Send(&pb.FindAllBranchNamesResponse{Names: refs})
	}
}

func newFindAllTagNamesWriter(stream pb.Ref_FindAllTagNamesServer) lines.Sender {
	return func(refs [][]byte) error {
		return stream.Send(&pb.FindAllTagNamesResponse{Names: refs})
	}
}

func newFindLocalBranchesWriter(stream pb.Ref_FindLocalBranchesServer) lines.Sender {
	return func(refs [][]byte) error {
		var branches []*pb.FindLocalBranchResponse

		for _, ref := range refs {
			elements, err := parseRef(ref)
			if err != nil {
				return err
			}
			branch, err := buildLocalBranch(elements)
			if err != nil {
				return err
			}
			branches = append(branches, branch)
		}
		return stream.Send(&pb.FindLocalBranchesResponse{Branches: branches})
	}
}

func newFindAllBranchesWriter(stream pb.RefService_FindAllBranchesServer) lines.Sender {
	return func(refs [][]byte) error {
		var branches []*pb.FindAllBranchesResponse_Branch

		for _, ref := range refs {
			elements, err := parseRef(ref)
			if err != nil {
				return err
			}
			branch, err := buildBranch(elements)
			if err != nil {
				return err
			}
			branches = append(branches, branch)
		}
		return stream.Send(&pb.FindAllBranchesResponse{Branches: branches})
	}
}
