package commit

import (
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gitpipe"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

func verifyListAllCommitsRequest(request *gitalypb.ListAllCommitsRequest) error {
	if request.GetRepository() == nil {
		return errors.New("empty repository")
	}
	return nil
}

func (s *server) ListAllCommits(
	request *gitalypb.ListAllCommitsRequest,
	stream gitalypb.CommitService_ListAllCommitsServer,
) error {
	if err := verifyListAllCommitsRequest(request); err != nil {
		return helper.ErrInvalidArgument(err)
	}

	ctx := stream.Context()
	repo := s.localrepo(request.GetRepository())

	objectReader, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return helper.ErrInternalf("creating object reader: %w", err)
	}

	// If we've got a pagination token, then we will only start to print commits as soon as
	// we've seen the token.
	token := request.GetPaginationParams().GetPageToken()
	waitingForToken := token != ""

	catfileInfoIter := gitpipe.CatfileInfoAllObjects(ctx, repo,
		gitpipe.WithSkipCatfileInfoResult(func(objectInfo *catfile.ObjectInfo) bool {
			if waitingForToken {
				waitingForToken = objectInfo.Oid != git.ObjectID(token)
				// We also skip the token itself, thus we always return `false`
				// here.
				return true
			}

			return objectInfo.Type != "commit"
		}),
	)

	catfileObjectIter := gitpipe.CatfileObject(ctx, objectReader, catfileInfoIter)

	chunker := chunk.New(&commitsSender{
		send: func(commits []*gitalypb.GitCommit) error {
			return stream.Send(&gitalypb.ListAllCommitsResponse{
				Commits: commits,
			})
		},
	})

	limit := request.GetPaginationParams().GetLimit()
	parser := catfile.NewParser()

	for i := int32(0); catfileObjectIter.Next(); i++ {
		// If we hit the pagination limit, then we stop sending commits even if there are
		// more commits in the pipeline.
		if limit > 0 && limit <= i {
			break
		}

		object := catfileObjectIter.Result()

		commit, err := parser.ParseCommit(object)
		if err != nil {
			return helper.ErrInternal(fmt.Errorf("parsing commit: %w", err))
		}

		if err := chunker.Send(commit); err != nil {
			return helper.ErrInternal(fmt.Errorf("sending commit: %w", err))
		}
	}

	if err := catfileObjectIter.Err(); err != nil {
		return helper.ErrInternal(fmt.Errorf("iterating objects: %w", err))
	}

	if err := chunker.Flush(); err != nil {
		return helper.ErrInternal(fmt.Errorf("flushing commits: %w", err))
	}

	return nil
}
