package commit

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/internal/service/ref"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// We declare this function in variables so that we can override them in our tests
var _findBranchNamesFunc = ref.FindBranchNames

type findAllCommitsSender struct {
	stream pb.CommitService_FindAllCommitsServer
}

func (s *server) FindAllCommits(in *pb.FindAllCommitsRequest, stream pb.CommitService_FindAllCommitsServer) error {
	sender := &findAllCommitsSender{stream}

	var gitLogExtraOptions []string
	if maxCount := in.GetMaxCount(); maxCount > 0 {
		gitLogExtraOptions = append(gitLogExtraOptions, fmt.Sprintf("--max-count=%d", maxCount))
	}
	if skip := in.GetSkip(); skip > 0 {
		gitLogExtraOptions = append(gitLogExtraOptions, fmt.Sprintf("--skip=%d", skip))
	}
	switch in.GetOrder() {
	case pb.FindAllCommitsRequest_NONE:
		// Do nothing
	case pb.FindAllCommitsRequest_DATE:
		gitLogExtraOptions = append(gitLogExtraOptions, "--date-order")
	case pb.FindAllCommitsRequest_TOPO:
		gitLogExtraOptions = append(gitLogExtraOptions, "--topo-order")
	}

	var revisions []string
	if len(in.GetRevision()) == 0 {
		branchNames, err := _findBranchNamesFunc(stream.Context(), in.Repository)
		if err != nil {
			return grpc.Errorf(codes.InvalidArgument, "FindAllCommits: %v", err)
		}

		for _, branch := range branchNames {
			revisions = append(revisions, string(branch))
		}
	} else {
		revisions = []string{string(in.GetRevision())}
	}

	return sendCommits(stream.Context(), sender, in.GetRepository(), revisions, nil, gitLogExtraOptions...)
}

func (sender *findAllCommitsSender) Send(commits []*pb.GitCommit) error {
	return sender.stream.Send(&pb.FindAllCommitsResponse{Commits: commits})
}
