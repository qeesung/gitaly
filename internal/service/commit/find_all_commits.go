package commit

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
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
	writer := newCommitsWriter(&findAllCommitsSender{stream})

	var gitLogExtraArgs []string
	if maxCount := in.GetMaxCount(); maxCount > 0 {
		gitLogExtraArgs = append(gitLogExtraArgs, fmt.Sprintf("--max-count=%d", maxCount))
	}
	if skip := in.GetSkip(); skip > 0 {
		gitLogExtraArgs = append(gitLogExtraArgs, fmt.Sprintf("--skip=%d", skip))
	}
	switch in.GetOrder() {
	case pb.FindAllCommitsRequest_NONE:
		// Do nothing
	case pb.FindAllCommitsRequest_DATE:
		gitLogExtraArgs = append(gitLogExtraArgs, "--date-order")
	case pb.FindAllCommitsRequest_TOPO:
		gitLogExtraArgs = append(gitLogExtraArgs, "--topo-order")
	}

	var revisionRange [][]byte
	if len(in.GetRevision()) == 0 {
		repoPath, err := helper.GetRepoPath(in.GetRepository())
		if err != nil {
			return grpc.Errorf(codes.InvalidArgument, "FindAllCommits: %v", err)
		}

		branchNames, err := _findBranchNamesFunc(repoPath)
		if err != nil {
			return grpc.Errorf(codes.InvalidArgument, "FindAllCommits: %v", err)
		}

		revisionRange = branchNames
	} else {
		revisionRange = [][]byte{in.GetRevision()}
	}

	return gitLog(writer, in.GetRepository(), revisionRange, gitLogExtraArgs...)
}

func (sender *findAllCommitsSender) Send(commits []*pb.GitCommit) error {
	return sender.stream.Send(&pb.FindAllCommitsResponse{Commits: commits})
}
