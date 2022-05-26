package diff

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	numStatDelimiter = 0
)

func (s *server) findChangedPathsCommon(ctx context.Context, name string, repository *gitalypb.Repository, options []git.Option, args []string, cmdOpts []git.CmdOpt, diffChunker *chunk.Chunker) error {
	flags := append([]git.Option{
		git.Flag{Name: "-z"},
		git.Flag{Name: "-m"},
		git.Flag{Name: "-r"},
		git.Flag{Name: "--name-status"},
		git.Flag{Name: "--no-renames"},
		git.Flag{Name: "--no-commit-id"},
		git.Flag{Name: "--diff-filter=AMDTC"},
	}, options...)

	cmd, err := s.gitCmdFactory.New(ctx, repository, git.SubCmd{
		Name:  "diff-tree",
		Flags: flags,
		Args:  args,
	}, cmdOpts...)
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return fmt.Errorf("%s Stdin Err: %w", name, err)
		}
		return status.Errorf(codes.Internal, "%s: Cmd Err: %v", name, err)
	}

	if err := parsePaths(name, bufio.NewReader(cmd), diffChunker); err != nil {
		return fmt.Errorf("%s Parsing Err: %w", name, err)
	}

	if err := cmd.Wait(); err != nil {
		return status.Errorf(codes.Unavailable, "%s: Cmd Wait Err: %v", name, err)
	}

	return diffChunker.Flush()
}

func (s *server) FindChangedPaths(in *gitalypb.FindChangedPathsRequest, stream gitalypb.DiffService_FindChangedPathsServer) error {
	if err := s.validateFindChangedPathsRequestParams(stream.Context(), in); err != nil {
		return err
	}

	diffChunker := chunk.New(&findChangedPathsSender{stream: stream})

	options := []git.Option{
		git.Flag{Name: "--stdin"},
	}
	cmdOpts := []git.CmdOpt{
		git.WithStdin(strings.NewReader(strings.Join(in.GetCommits(), "\n") + "\n")),
	}
	return s.findChangedPathsCommon(stream.Context(), "FindChangedPaths", in.GetRepository(), options, nil, cmdOpts, diffChunker)
}

func (s *server) FindChangedPathsBetweenCommits(in *gitalypb.FindChangedPathsBetweenCommitsRequest, stream gitalypb.DiffService_FindChangedPathsBetweenCommitsServer) error {
	if err := s.validateFindChangedPathsBetweenCommitsRequestParams(stream.Context(), in); err != nil {
		return err
	}

	diffChunker := chunk.New(&findChangedPathsBetweenCommitsSender{stream: stream})

	args := []string{
		in.GetLeftCommitId(),
		in.GetRightCommitId(),
	}
	return s.findChangedPathsCommon(stream.Context(), "FindChangedPathsBetweenCommits", in.GetRepository(), nil, args, nil, diffChunker)
}

func parsePaths(name string, reader *bufio.Reader, chunker *chunk.Chunker) error {
	for {
		path, err := nextPath(name, reader)
		if err != nil {
			if err == io.EOF {
				break
			}

			return fmt.Errorf("FindChangedPaths Next Path Err: %w", err)
		}

		if err := chunker.Send(path); err != nil {
			return fmt.Errorf("FindChangedPaths: err sending to chunker: %v", err)
		}
	}

	return nil
}

func nextPath(name string, reader *bufio.Reader) (*gitalypb.ChangedPaths, error) {
	pathStatus, err := reader.ReadBytes(numStatDelimiter)
	if err != nil {
		return nil, err
	}

	path, err := reader.ReadBytes(numStatDelimiter)
	if err != nil {
		return nil, err
	}

	statusTypeMap := map[string]gitalypb.ChangedPaths_Status{
		"M": gitalypb.ChangedPaths_MODIFIED,
		"D": gitalypb.ChangedPaths_DELETED,
		"T": gitalypb.ChangedPaths_TYPE_CHANGE,
		"C": gitalypb.ChangedPaths_COPIED,
		"A": gitalypb.ChangedPaths_ADDED,
	}

	parsedPath, ok := statusTypeMap[string(pathStatus[:len(pathStatus)-1])]
	if !ok {
		return nil, status.Errorf(codes.Internal, "%s: Unknown changed paths returned: %v", name, string(pathStatus))
	}

	changedPath := &gitalypb.ChangedPaths{
		Status: parsedPath,
		Path:   path[:len(path)-1],
	}

	return changedPath, nil
}

// This sender implements the interface in the chunker class
type findChangedPathsSender struct {
	paths  []*gitalypb.ChangedPaths
	stream gitalypb.DiffService_FindChangedPathsServer
}

func (t *findChangedPathsSender) Reset() {
	t.paths = nil
}

func (t *findChangedPathsSender) Append(m proto.Message) {
	t.paths = append(t.paths, m.(*gitalypb.ChangedPaths))
}

func (t *findChangedPathsSender) Send() error {
	return t.stream.Send(&gitalypb.FindChangedPathsResponse{
		Paths: t.paths,
	})
}

// This sender implements the interface in the chunker class
type findChangedPathsBetweenCommitsSender struct {
	paths  []*gitalypb.ChangedPaths
	stream gitalypb.DiffService_FindChangedPathsBetweenCommitsServer
}

func (t *findChangedPathsBetweenCommitsSender) Reset() {
	t.paths = nil
}

func (t *findChangedPathsBetweenCommitsSender) Append(m proto.Message) {
	t.paths = append(t.paths, m.(*gitalypb.ChangedPaths))
}

func (t *findChangedPathsBetweenCommitsSender) Send() error {
	return t.stream.Send(&gitalypb.FindChangedPathsBetweenCommitsResponse{
		Paths: t.paths,
	})
}

func (s *server) validateCommon(ctx context.Context, name string, repo *gitalypb.Repository, commits []string) error {
	if _, err := s.locator.GetRepoPath(repo); err != nil {
		return err
	}

	gitRepo := s.localrepo(repo)

	for _, commit := range commits {
		if commit == "" {
			return status.Errorf(codes.InvalidArgument, "%s: commits cannot contain an empty commit", name)
		}

		containsRef, err := gitRepo.HasRevision(ctx, git.Revision(commit+"^{commit}"))
		if err != nil {
			return fmt.Errorf("contains ref err: %w", err)
		}

		if !containsRef {
			return status.Errorf(codes.NotFound, "%s: commit: %v can not be found", name, commit)
		}
	}

	return nil
}

func (s *server) validateFindChangedPathsRequestParams(ctx context.Context, in *gitalypb.FindChangedPathsRequest) error {
	return s.validateCommon(ctx, "FindChangedPaths", in.GetRepository(), in.GetCommits())
}

func (s *server) validateFindChangedPathsBetweenCommitsRequestParams(ctx context.Context, in *gitalypb.FindChangedPathsBetweenCommitsRequest) error {
	return s.validateCommon(ctx, "FindChangedPathsBetweenCommits", in.GetRepository(), []string{in.GetLeftCommitId(), in.GetRightCommitId()})
}
