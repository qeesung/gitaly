package commit

import (
	"fmt"

	"github.com/golang/protobuf/proto"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/ref"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// We declare this function in variables so that we can override them in our tests
var _findBranchNamesFunc = ref.FindBranchNames

type findAllCommitsSender struct {
	stream  gitalypb.CommitService_FindAllCommitsServer
	commits []*gitalypb.GitCommit
}

func (sender *findAllCommitsSender) Reset() { sender.commits = nil }
func (sender *findAllCommitsSender) Append(m proto.Message) {
	sender.commits = append(sender.commits, m.(*gitalypb.GitCommit))
}

func (sender *findAllCommitsSender) Send() error {
	return sender.stream.Send(&gitalypb.FindAllCommitsResponse{Commits: sender.commits})
}

func (s *server) FindAllCommits(in *gitalypb.FindAllCommitsRequest, stream gitalypb.CommitService_FindAllCommitsServer) error {
	if err := validateFindAllCommitsRequest(in); err != nil {
		return err
	}

	var revisions []string
	if len(in.GetRevision()) == 0 {
		branchNames, err := _findBranchNamesFunc(stream.Context(), s.gitCmdFactory, in.Repository)
		if err != nil {
			return helper.ErrInvalidArgument(err)
		}

		for _, branch := range branchNames {
			revisions = append(revisions, string(branch))
		}
	} else {
		revisions = []string{string(in.GetRevision())}
	}

	if err := s.findAllCommits(in, stream, revisions); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func validateFindAllCommitsRequest(in *gitalypb.FindAllCommitsRequest) error {
	if err := git.ValidateRevisionAllowEmpty(in.Revision); err != nil {
		return helper.ErrInvalidArgument(err)
	}

	return nil
}

func (s *server) findAllCommits(in *gitalypb.FindAllCommitsRequest, stream gitalypb.CommitService_FindAllCommitsServer, revisions []string) error {
	sender := &findAllCommitsSender{stream: stream}

	var gitLogExtraOptions []git.Option
	if maxCount := in.GetMaxCount(); maxCount > 0 {
		gitLogExtraOptions = append(gitLogExtraOptions, git.Flag{Name: fmt.Sprintf("--max-count=%d", maxCount)})
	}
	if skip := in.GetSkip(); skip > 0 {
		gitLogExtraOptions = append(gitLogExtraOptions, git.Flag{Name: fmt.Sprintf("--skip=%d", skip)})
	}
	switch in.GetOrder() {
	case gitalypb.FindAllCommitsRequest_NONE:
		// Do nothing
	case gitalypb.FindAllCommitsRequest_DATE:
		gitLogExtraOptions = append(gitLogExtraOptions, git.Flag{Name: "--date-order"})
	case gitalypb.FindAllCommitsRequest_TOPO:
		gitLogExtraOptions = append(gitLogExtraOptions, git.Flag{Name: "--topo-order"})
	}

	return sendCommits(stream.Context(), sender, s.gitCmdFactory, in.GetRepository(), revisions, nil, nil, gitLogExtraOptions...)
}
