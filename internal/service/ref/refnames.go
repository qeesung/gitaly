package ref

import (
	"bufio"
	"context"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// FindAllBranchNames creates a stream of ref names for all branches in the given repository
func (s *server) FindAllBranchNames(in *gitalypb.FindAllBranchNamesRequest, stream gitalypb.RefService_FindAllBranchNamesServer) error {
	chunker := chunk.New(&findAllBranchNamesSender{stream: stream})

	return listRefNames(stream.Context(), chunker, "refs/heads", in.Repository, nil)
}

type findAllBranchNamesSender struct {
	stream      gitalypb.RefService_FindAllBranchNamesServer
	branchNames [][]byte
}

func (ts *findAllBranchNamesSender) Reset() { ts.branchNames = nil }
func (ts *findAllBranchNamesSender) Append(it chunk.Item) {
	ts.branchNames = append(ts.branchNames, []byte(it.(string)))
}
func (ts *findAllBranchNamesSender) Send() error {
	return ts.stream.Send(&gitalypb.FindAllBranchNamesResponse{Names: ts.branchNames})
}

// FindAllTagNames creates a stream of ref names for all tags in the given repository
func (s *server) FindAllTagNames(in *gitalypb.FindAllTagNamesRequest, stream gitalypb.RefService_FindAllTagNamesServer) error {
	chunker := chunk.New(&findAllTagNamesSender{stream: stream})

	return listRefNames(stream.Context(), chunker, "refs/tags", in.Repository, nil)
}

type findAllTagNamesSender struct {
	stream   gitalypb.RefService_FindAllTagNamesServer
	tagNames [][]byte
}

func (ts *findAllTagNamesSender) Reset() { ts.tagNames = nil }
func (ts *findAllTagNamesSender) Append(it chunk.Item) {
	ts.tagNames = append(ts.tagNames, []byte(it.(string)))
}
func (ts *findAllTagNamesSender) Send() error {
	return ts.stream.Send(&gitalypb.FindAllTagNamesResponse{Names: ts.tagNames})
}

func listRefNames(ctx context.Context, chunker *chunk.Chunker, prefix string, repo *gitalypb.Repository, extraArgs []string) error {
	args := []string{
		"for-each-ref",
		"--format=%(refname)",
	}
	args = append(args, extraArgs...)
	args = append(args, prefix)

	cmd, err := git.Command(ctx, repo, args...)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(cmd)
	for scanner.Scan() {
		// Important: don't use scanner.Bytes() because the slice will become
		// invalid on the next loop iteration. Instead, use scanner.Text() to
		// force a copy.
		if err := chunker.Send(scanner.Text()); err != nil {
			return err
		}
	}

	if err := cmd.Wait(); err != nil {
		return err
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return chunker.Flush()
}
