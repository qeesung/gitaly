package commit

import (
	"errors"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/git/gitpipe"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func verifyListCommitsRequest(request *gitalypb.ListCommitsRequest) error {
	if request.GetRepository() == nil {
		return errors.New("empty repository")
	}
	if len(request.GetRevisions()) == 0 {
		return errors.New("missing revisions")
	}
	for _, revision := range request.Revisions {
		if strings.HasPrefix(revision, "-") && revision != "--all" && revision != "--not" {
			return fmt.Errorf("invalid revision: %q", revision)
		}
	}
	return nil
}

func (s *server) ListCommits(
	request *gitalypb.ListCommitsRequest,
	stream gitalypb.CommitService_ListCommitsServer,
) error {
	if err := verifyListCommitsRequest(request); err != nil {
		return helper.ErrInvalidArgument(err)
	}

	ctx := stream.Context()
	repo := s.localrepo(request.GetRepository())

	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return helper.ErrInternal(fmt.Errorf("creating object reader: %w", err))
	}
	defer cancel()

	revlistOptions := []gitpipe.RevlistOption{}

	switch request.GetOrder() {
	case gitalypb.ListCommitsRequest_NONE:
		// Nothing to do, we use default sorting.
	case gitalypb.ListCommitsRequest_TOPO:
		revlistOptions = append(revlistOptions, gitpipe.WithOrder(gitpipe.OrderTopo))
	case gitalypb.ListCommitsRequest_DATE:
		revlistOptions = append(revlistOptions, gitpipe.WithOrder(gitpipe.OrderDate))
	}

	if request.GetReverse() {
		revlistOptions = append(revlistOptions, gitpipe.WithReverse())
	}

	if request.GetMaxParents() > 0 {
		revlistOptions = append(revlistOptions, gitpipe.WithMaxParents(uint(request.GetMaxParents())))
	}

	if request.GetDisableWalk() {
		revlistOptions = append(revlistOptions, gitpipe.WithDisabledWalk())
	}

	if request.GetFirstParent() {
		revlistOptions = append(revlistOptions, gitpipe.WithFirstParent())
	}

	if request.GetBefore() != nil {
		revlistOptions = append(revlistOptions, gitpipe.WithBefore(request.GetBefore().AsTime()))
	}

	if request.GetAfter() != nil {
		revlistOptions = append(revlistOptions, gitpipe.WithAfter(request.GetAfter().AsTime()))
	}

	if len(request.GetAuthor()) != 0 {
		revlistOptions = append(revlistOptions, gitpipe.WithAuthor(request.GetAuthor()))
	}

	// If we've got a pagination token, then we will only start to print commits as soon as
	// we've seen the token.
	if token := request.GetPaginationParams().GetPageToken(); token != "" {
		tokenSeen := false
		revlistOptions = append(revlistOptions, gitpipe.WithSkipRevlistResult(func(r *gitpipe.RevisionResult) bool {
			if !tokenSeen {
				tokenSeen = r.OID == git.ObjectID(token)
				// We also skip the token itself, thus we always return `false`
				// here.
				return true
			}

			return false
		}))
	}

	revlistIter := gitpipe.Revlist(ctx, repo, request.GetRevisions(), revlistOptions...)

	catfileObjectIter, err := gitpipe.CatfileObject(ctx, objectReader, revlistIter)
	if err != nil {
		return err
	}

	chunker := chunk.New(&commitsSender{
		send: func(commits []*gitalypb.GitCommit) error {
			return stream.Send(&gitalypb.ListCommitsResponse{
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
