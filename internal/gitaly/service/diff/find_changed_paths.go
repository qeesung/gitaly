package diff

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

const (
	numStatDelimiter = 0
)

// changedPathsRequestToString converts the given FindChangedPathsRequest to a string that can be passed to git-diff-tree(1). Note
// that this function expects that all revisions have already been resolved to their respective object IDs.
func changedPathsRequestToString(r *gitalypb.FindChangedPathsRequest_Request) (string, error) {
	switch t := r.GetType().(type) {
	case *gitalypb.FindChangedPathsRequest_Request_CommitRequest_:
		return strings.Join(append([]string{t.CommitRequest.GetCommitRevision()}, t.CommitRequest.GetParentCommitRevisions()...), " "), nil
	case *gitalypb.FindChangedPathsRequest_Request_TreeRequest_:
		return t.TreeRequest.GetLeftTreeRevision() + " " + t.TreeRequest.GetRightTreeRevision(), nil
	}

	// This shouldn't happen
	return "", fmt.Errorf("unknown FindChangedPathsRequest type")
}

func (s *server) FindChangedPaths(in *gitalypb.FindChangedPathsRequest, stream gitalypb.DiffService_FindChangedPathsServer) error {
	if err := s.validateFindChangedPathsRequestParams(stream.Context(), in); err != nil {
		return err
	}

	diffChunker := chunk.New(&findChangedPathsSender{stream: stream})

	requests := make([]string, len(in.GetRequests()))
	for i, request := range in.GetRequests() {
		str, err := changedPathsRequestToString(request)
		if err != nil {
			return err
		}
		requests[i] = str
	}

	flags := []git.Option{
		git.Flag{Name: "-z"},
		git.Flag{Name: "--stdin"},
		git.Flag{Name: "-r"},
		git.Flag{Name: "--no-renames"},
		git.Flag{Name: "--no-commit-id"},
		// By default, git-diff-tree(1) does not report changes in the root commit.
		// By adding below flag we ask Git to behave as when comparing to an empty
		// tree in that case.
		git.Flag{Name: "--root"},
		git.Flag{Name: "--diff-filter=AMDTC"},
	}
	switch in.MergeCommitDiffMode {
	case gitalypb.FindChangedPathsRequest_MERGE_COMMIT_DIFF_MODE_INCLUDE_MERGES, gitalypb.FindChangedPathsRequest_MERGE_COMMIT_DIFF_MODE_UNSPECIFIED:
		// By default, git diff-tree --stdin does not show differences
		// for merge commits. With this flag, it shows differences to
		// that commit from all of its parents.
		flags = append(flags, git.Flag{Name: "-m"})
	case gitalypb.FindChangedPathsRequest_MERGE_COMMIT_DIFF_MODE_ALL_PARENTS:
		// This flag changes the way a merge commit is displayed (which
		// means it is useful only when the command is given one
		// <tree-ish>, or --stdin). It shows the differences from each
		// of the parents to the merge result simultaneously instead of
		// showing pairwise diff between a parent and the result one at
		// a time (which is what the -m option does). Furthermore, it
		// lists only files which were modified from all parents.
		flags = append(flags, git.Flag{Name: "-c"})
	}

	cmd, err := s.gitCmdFactory.New(stream.Context(), in.Repository, git.Command{
		Name:  "diff-tree",
		Flags: flags,
	}, git.WithStdin(strings.NewReader(strings.Join(requests, "\n")+"\n")), git.WithSetupStdout())
	if err != nil {
		return structerr.NewInternal("cmd err: %w", err)
	}

	if err := parsePaths(bufio.NewReader(cmd), diffChunker); err != nil {
		return fmt.Errorf("parsing err: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return structerr.NewFailedPrecondition("cmd wait err: %w", err)
	}

	return diffChunker.Flush()
}

func parsePaths(reader *bufio.Reader, chunker *chunk.Chunker) error {
	for {
		paths, err := nextPath(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return fmt.Errorf("next path err: %w", err)
		}

		for _, path := range paths {
			if err := chunker.Send(path); err != nil {
				return fmt.Errorf("err sending to chunker: %w", err)
			}
		}
	}

	return nil
}

func nextPath(reader *bufio.Reader) ([]*gitalypb.ChangedPaths, error) {
	// When using git-diff-tree(1) option '-c' each line will be in the format:
	//
	//    1. a colon for each source.
	//    2. mode for each "src"; 000000 if creation or unmerged.
	//    3. a space.
	//    4. mode for "dst"; 000000 if deletion or unmerged.
	//    5. a space.
	//    6. oid for each "src"; 0{40} if creation or unmerged.
	//    7. a space.
	//    8. oid for "dst"; 0{40} if deletion, unmerged or "work tree out of
	//       sync with the index".
	//    9. a space.
	//   10. status is concatenated status characters for each parent
	//   11. a tab or a NUL when -z option is used.
	//   12. path for "src"
	//   13. a tab or a NUL when -z option is used; only exists for C or R.
	//   14. path for "dst"; only exists for C or R.
	//
	// Example output:
	//
	// ::100644 100644 100644 fabadb8 cc95eb0 4866510 MM       desc.c
	// ::100755 100755 100755 52b7a2d 6d1ac04 d2ac7d7 RM       bar.sh
	// ::100644 100644 100644 e07d6c5 9042e82 ee91881 RR       phooey.c
	//
	// This example has 2 sources, the mode and oid represent the values at
	// each of the parents. When option '-m' was used this would be shown as:
	//
	// :100644 100644 fabadb8 4866510 M       desc.c
	// :100755 100755 52b7a2d d2ac7d7 R       bar.sh
	// :100644 100644 e07d6c5 ee91881 R       phooey.c
	// :100644 100644 cc95eb0 4866510 M       desc.c
	// :100755 100755 6d1ac04 d2ac7d7 M       bar.sh
	// :100644 100644 9042e82 ee91881 R       phooey.c
	//
	// The number of sources returned depends on the number of parents of
	// the commit, so we don't know in advance. First step is to count
	// number of colons.

	// Read up to the first colon. If trees were passed to the command,
	// git-diff-tree(1) will print them, but are swallowed.
	_, err := reader.ReadBytes(':')
	if err != nil {
		return nil, err
	}

	line, err := reader.ReadBytes(numStatDelimiter)
	if err != nil {
		return nil, err
	}
	split := bytes.Split(line[:len(line)-1], []byte(" "))

	// Determine the number of sources.
	// The first colon was eaten by reader.ReadBytes(':') so we need to add
	// one extra to get the total source count.
	srcCount := bytes.LastIndexByte(split[0], byte(':')) + 2
	split[0] = split[0][srcCount-1:]

	pathStatus := split[len(split)-1]

	// The new blob id of a changed path is the last object ID in the output.
	// It's applied for a single source:
	//
	// :100644 100644 5be4a4a 0000000 M file.c
	//
	// And for multiple sources:
	//
	// ::100644 100644 100644 fabadb8 cc95eb0 4866510 MM	desc.c
	newBlobID := string(split[len(split)-2])

	// Sanity check on the number of fields. There should be:
	// * a mode + hash for each source
	// * a mode + hash for the destination
	// * a status indicator (might be concatenated for multiple sources)
	if len(split) != (2*srcCount)+2+1 || len(pathStatus) != srcCount {
		return nil, fmt.Errorf("git diff-tree parsing failed on: %v", line)
	}

	// Read the path (until the next NUL delimiter)
	path, err := reader.ReadBytes(numStatDelimiter)
	if err != nil {
		return nil, err
	}
	path = path[:len(path)-1]

	statusTypeMap := map[string]gitalypb.ChangedPaths_Status{
		"M": gitalypb.ChangedPaths_MODIFIED,
		"D": gitalypb.ChangedPaths_DELETED,
		"T": gitalypb.ChangedPaths_TYPE_CHANGE,
		"C": gitalypb.ChangedPaths_COPIED,
		"A": gitalypb.ChangedPaths_ADDED,
	}

	// Produce a gitalypb.ChangedPaths for each source
	changedPaths := make([]*gitalypb.ChangedPaths, srcCount)
	for i := range changedPaths {
		oldMode, err := strconv.ParseInt(string(split[i]), 8, 32)
		if err != nil {
			return nil, fmt.Errorf("parsing old mode: %w", err)
		}

		newMode, err := strconv.ParseInt(string(split[srcCount]), 8, 32)
		if err != nil {
			return nil, fmt.Errorf("parsing new mode: %w", err)
		}

		parsedPath, ok := statusTypeMap[string(pathStatus[i:i+1])]
		if !ok {
			return nil, structerr.NewInternal("unknown changed paths returned: %v", string(pathStatus))
		}

		changedPaths[i] = &gitalypb.ChangedPaths{
			Status:    parsedPath,
			Path:      path,
			OldMode:   int32(oldMode),
			NewMode:   int32(newMode),
			OldBlobId: string(split[srcCount+i+1]),
			NewBlobId: newBlobID,
		}
	}

	return changedPaths, nil
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

func resolveObjectWithType(
	ctx context.Context,
	repo *localrepo.Repo,
	objectInfoReader catfile.ObjectInfoReader,
	revision string,
	expectedType string,
) (git.ObjectID, error) {
	if revision == "" {
		return "", structerr.NewInvalidArgument("revision cannot be empty")
	}

	info, err := objectInfoReader.Info(ctx, git.Revision(fmt.Sprintf("%s^{%s}", revision, expectedType)))
	if err != nil {
		if errors.As(err, &catfile.NotFoundError{}) {
			return "", structerr.NewNotFound("revision can not be found: %q", revision)
		}
		return "", err
	}

	return info.Oid, nil
}

func (s *server) validateFindChangedPathsRequestParams(ctx context.Context, in *gitalypb.FindChangedPathsRequest) error {
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(repository); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	gitRepo := s.localrepo(repository)

	if len(in.GetCommits()) > 0 { //nolint:staticcheck
		if len(in.GetRequests()) > 0 {
			return structerr.NewInvalidArgument("cannot specify both commits and requests")
		}

		in.Requests = make([]*gitalypb.FindChangedPathsRequest_Request, len(in.GetCommits())) //nolint:staticcheck
		for i, commit := range in.GetCommits() {                                              //nolint:staticcheck
			in.Requests[i] = &gitalypb.FindChangedPathsRequest_Request{
				Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
					CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
						CommitRevision: commit,
					},
				},
			}
		}
	}

	objectInfoReader, cancel, err := s.catfileCache.ObjectInfoReader(ctx, gitRepo)
	if err != nil {
		return structerr.NewInternal("getting object info reader: %w", err)
	}
	defer cancel()

	for _, request := range in.GetRequests() {
		switch t := request.Type.(type) {
		case *gitalypb.FindChangedPathsRequest_Request_CommitRequest_:
			oid, err := resolveObjectWithType(
				ctx,
				gitRepo,
				objectInfoReader,
				t.CommitRequest.GetCommitRevision(),
				"commit",
			)
			if err != nil {
				return structerr.NewInternal("resolving commit: %w", err)
			}
			t.CommitRequest.CommitRevision = oid.String()

			for i, commit := range t.CommitRequest.GetParentCommitRevisions() {
				oid, err := resolveObjectWithType(
					ctx,
					gitRepo,
					objectInfoReader,
					commit,
					"commit",
				)
				if err != nil {
					return structerr.NewInternal("resolving commit parent: %w", err)
				}
				t.CommitRequest.ParentCommitRevisions[i] = oid.String()
			}
		case *gitalypb.FindChangedPathsRequest_Request_TreeRequest_:
			oid, err := resolveObjectWithType(
				ctx,
				gitRepo,
				objectInfoReader,
				t.TreeRequest.GetLeftTreeRevision(),
				"tree",
			)
			if err != nil {
				return structerr.NewInternal("resolving left tree: %w", err)
			}
			t.TreeRequest.LeftTreeRevision = oid.String()

			oid, err = resolveObjectWithType(
				ctx,
				gitRepo,
				objectInfoReader,
				t.TreeRequest.GetRightTreeRevision(),
				"tree",
			)
			if err != nil {
				return structerr.NewInternal("resolving right tree: %w", err)
			}
			t.TreeRequest.RightTreeRevision = oid.String()
		}
	}

	return nil
}
