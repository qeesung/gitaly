package ref

import (
	"bytes"
	"strings"

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

// The duplication of commit info fields can be avoided by using %(if)...%(then)...%(else),
// however, it's only supported in git 2.13.0+, so we workaround by duplication.
var tagsFormatFields = []string{
	// tag info
	"%(refname:strip=2)",
	"%(objectname)",
	"%(*objecttype)",
	"%(contents)",
	// commit info, present for annotated tag
	"%(*objectname)",
	"%(*contents:subject)",
	"%(*contents)",
	"%(*authorname)",
	"%(*authoremail)",
	"%(*authordate:iso-strict)",
	"%(*committername)",
	"%(*committeremail)",
	"%(*committerdate:iso-strict)",
	"%(*parent)",
	// commit info, present for lightweight tag
	"%(objectname)",
	"%(contents:subject)",
	"%(contents)",
	"%(authorname)",
	"%(authoremail)",
	"%(authordate:iso-strict)",
	"%(committername)",
	"%(committeremail)",
	"%(committerdate:iso-strict)",
	"%(parent)",
}

func parseRef(ref []byte) ([][]byte, error) {
	elements := bytes.Split(ref, []byte("\x00"))
	if len(elements) != 9 {
		return nil, grpc.Errorf(codes.Internal, "error parsing ref %q", ref)
	}
	return elements, nil
}

func buildCommitFromBranchInfo(elements [][]byte) (*pb.GitCommit, error) {
	return git.NewCommit(elements[0], elements[1], nil, elements[2],
		elements[3], elements[4], elements[5], elements[6], elements[7])
}

func buildCommitFromTagInfo(elements [][]byte) (*pb.GitCommit, error) {
	parentIds := strings.Split(string(elements[9]), " ")
	return git.NewCommit(
		elements[0],
		elements[1],
		elements[2],
		elements[3],
		bytes.Trim(elements[4], "<>"),
		elements[5],
		elements[6],
		bytes.Trim(elements[7], "<>"),
		elements[8],
		parentIds...)
}

func buildLocalBranch(elements [][]byte) (*pb.FindLocalBranchResponse, error) {
	target, err := buildCommitFromBranchInfo(elements[1:])
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
	target, err := buildCommitFromBranchInfo(elements[1:])
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

func newFindAllTagsWriter(repo *pb.Repository, stream pb.RefService_FindAllTagsServer) lines.Sender {
	return func(refs [][]byte) error {
		var tags []*pb.FindAllTagsResponse_Tag

		for _, ref := range refs {
			elements := bytes.Split(ref, []byte("\x1f"))
			if len(elements) != 24 {
				return grpc.Errorf(codes.Internal, "FindAllTags: error parsing ref %q", ref)
			}

			var message []byte
			var commitInfo [][]byte
			var commit *pb.GitCommit

			switch string(elements[2]) { // elements[2] is the object type the tag is pointing to
			case "commit": // tag is annotated tag pointing to a commit
				message = elements[3]
				commitInfo = elements[4:14]
			case "": // tag is a lightweight tag (essentially a commit), so it points to nothing
				commitInfo = elements[14:24]
			default: // tag is annotated tag pointing to something else (e.g. a blob), so we don't collect commit info because there is none
				message = elements[3]
			}

			if len(commitInfo) > 0 {
				var err error
				commit, err = buildCommitFromTagInfo(commitInfo)
				if err != nil {
					return grpc.Errorf(codes.Internal, "FindAllTags: error parsing commit: %v", err)
				}
			}

			tag := &pb.FindAllTagsResponse_Tag{
				Name:         elements[0],
				Id:           string(elements[1]),
				Message:      message,
				TargetCommit: commit,
			}

			tags = append(tags, tag)
		}

		return stream.Send(&pb.FindAllTagsResponse{Tags: tags})
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
