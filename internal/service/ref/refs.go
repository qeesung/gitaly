package ref

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper/lines"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"golang.org/x/net/context"
)

var (
	master = []byte("refs/heads/master")

	// We declare the following functions in variables so that we can override them in our tests
	headReference = _headReference
	// FindBranchNames is exported to be used in other packages
	FindBranchNames = _findBranchNames
)

type findRefsOpts struct {
	cmdArgs []string
	delim   []byte
}

func findRefs(ctx context.Context, writer lines.Sender, repo *pb.Repository, patterns []string, opts *findRefsOpts) error {
	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"Patterns": patterns,
	}).Debug("FindRefs")

	baseArgs := []string{"for-each-ref"}

	var args []string
	if len(opts.cmdArgs) == 0 {
		args = append(baseArgs, "--format=%(refname)") // Default format
	} else {
		args = append(baseArgs, opts.cmdArgs...)
	}

	args = append(args, patterns...)
	cmd, err := git.Command(ctx, repo, args...)
	if err != nil {
		return err
	}

	if err := lines.Send(cmd, writer, opts.delim); err != nil {
		return err
	}

	return cmd.Wait()
}

// FindAllBranchNames creates a stream of ref names for all branches in the given repository
func (s *server) FindAllBranchNames(in *pb.FindAllBranchNamesRequest, stream pb.RefService_FindAllBranchNamesServer) error {
	return findRefs(stream.Context(), newFindAllBranchNamesWriter(stream), in.Repository, []string{"refs/heads"}, &findRefsOpts{})
}

// FindAllTagNames creates a stream of ref names for all tags in the given repository
func (s *server) FindAllTagNames(in *pb.FindAllTagNamesRequest, stream pb.RefService_FindAllTagNamesServer) error {
	return findRefs(stream.Context(), newFindAllTagNamesWriter(stream), in.Repository, []string{"refs/tags"}, &findRefsOpts{})
}

func (s *server) FindAllTags(in *pb.FindAllTagsRequest, stream pb.RefService_FindAllTagsServer) error {
	ctx := stream.Context()

	client, err := s.RefServiceClient(ctx)
	if err != nil {
		return err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, in.GetRepository())
	if err != nil {
		return err
	}

	rubyStream, err := client.FindAllTags(clientCtx, in)
	if err != nil {
		return err
	}

	return rubyserver.Proxy(func() error {
		resp, err := rubyStream.Recv()
		if err != nil {
			md := rubyStream.Trailer()
			stream.SetTrailer(md)
			return err
		}
		return stream.Send(resp)
	})
}

func _findBranchNames(ctx context.Context, repo *pb.Repository) ([][]byte, error) {
	var names [][]byte

	cmd, err := git.Command(ctx, repo, "for-each-ref", "refs/heads", "--format=%(refname)")
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(cmd)
	for scanner.Scan() {
		names, _ = lines.CopyAndAppend(names, scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading standard input: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		return nil, err
	}

	return names, nil
}

func _headReference(ctx context.Context, repo *pb.Repository) ([]byte, error) {
	var headRef []byte

	cmd, err := git.Command(ctx, repo, "rev-parse", "--symbolic-full-name", "HEAD")
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(cmd)
	scanner.Scan()
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	headRef = scanner.Bytes()

	if err := cmd.Wait(); err != nil {
		// If the ref pointed at by HEAD doesn't exist, the rev-parse fails
		// returning the string `"HEAD"`, so we return `nil` without error.
		if bytes.Equal(headRef, []byte("HEAD")) {
			return nil, nil
		}

		return nil, err
	}

	return headRef, nil
}

// DefaultBranchName looks up the name of the default branch given a repoPath
func DefaultBranchName(ctx context.Context, repo *pb.Repository) ([]byte, error) {
	branches, err := FindBranchNames(ctx, repo)

	if err != nil {
		return nil, err
	}

	// Return empty ref name if there are no branches
	if len(branches) == 0 {
		return nil, nil
	}

	// Return first branch name if there's only one
	if len(branches) == 1 {
		return branches[0], nil
	}

	hasMaster := false
	headRef, err := headReference(ctx, repo)
	if err != nil {
		return nil, err
	}

	for _, branch := range branches {
		// Return HEAD if it exists and corresponds to a branch
		if headRef != nil && bytes.Equal(headRef, branch) {
			return headRef, nil
		}
		if bytes.Equal(branch, master) {
			hasMaster = true
		}
	}
	// Return `ref/names/master` if it exists
	if hasMaster {
		return master, nil
	}
	// If all else fails, return the first branch name
	return branches[0], nil
}

// FindDefaultBranchName returns the default branch name for the given repository
func (s *server) FindDefaultBranchName(ctx context.Context, in *pb.FindDefaultBranchNameRequest) (*pb.FindDefaultBranchNameResponse, error) {
	grpc_logrus.Extract(ctx).Debug("FindDefaultBranchName")

	defaultBranchName, err := DefaultBranchName(ctx, in.Repository)
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, grpc.Errorf(codes.Internal, err.Error())
	}

	return &pb.FindDefaultBranchNameResponse{Name: defaultBranchName}, nil
}

func parseSortKey(sortKey pb.FindLocalBranchesRequest_SortBy) string {
	switch sortKey {
	case pb.FindLocalBranchesRequest_NAME:
		return "refname"
	case pb.FindLocalBranchesRequest_UPDATED_ASC:
		return "committerdate"
	case pb.FindLocalBranchesRequest_UPDATED_DESC:
		return "-committerdate"
	}

	panic("never reached") // famous last words
}

// FindLocalBranches creates a stream of branches for all local branches in the given repository
func (s *server) FindLocalBranches(in *pb.FindLocalBranchesRequest, stream pb.RefService_FindLocalBranchesServer) error {
	writer := newFindLocalBranchesWriter(stream)
	opts := &findRefsOpts{
		cmdArgs: []string{
			// %00 inserts the null character into the output (see for-each-ref docs)
			"--format=" + strings.Join(localBranchFormatFields, "%00"),
			"--sort=" + parseSortKey(in.GetSortBy()),
		},
	}

	return findRefs(stream.Context(), writer, in.Repository, []string{"refs/heads"}, opts)
}

func (s *server) FindAllBranches(in *pb.FindAllBranchesRequest, stream pb.RefService_FindAllBranchesServer) error {
	opts := &findRefsOpts{
		cmdArgs: []string{
			// %00 inserts the null character into the output (see for-each-ref docs)
			"--format=" + strings.Join(localBranchFormatFields, "%00"),
		},
	}
	writer := newFindAllBranchesWriter(stream)

	return findRefs(stream.Context(), writer, in.Repository, []string{"refs/heads", "refs/remotes"}, opts)
}
