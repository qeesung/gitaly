package ref

import (
	"fmt"
	"log"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"golang.org/x/net/context"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

// FindRefName returns a ref that starts with the given prefix, if one exists.
//  If there is more than one such ref there is no guarantee which one is
//  returned or that the same one is returned on each call.
func (s *server) FindRefName(ctx context.Context, in *pb.FindRefNameRequest) (*pb.FindRefNameResponse, error) {
	repoPath, err := helper.GetRepoPath(in.Repository)
	if err != nil {
		message := fmt.Sprintf("FindRefName: %v", err)
		log.Print(message)
		return nil, grpc.Errorf(codes.InvalidArgument, message)
	}
	if in.CommitId == "" {
		message := "Bad Request (empty commit sha)"
		log.Printf("FindRefName: %q", message)
		return nil, grpc.Errorf(codes.InvalidArgument, message)
	}

	ref, err := findRefName(repoPath, in.CommitId, string(in.Prefix))
	if err != nil {
		return nil, grpc.Errorf(codes.Internal, err.Error())
	}

	return &pb.FindRefNameResponse{Name: []byte(ref)}, nil
}

// We assume `path` and `commitID` and `prefix` are non-empty
func findRefName(path, commitID, prefix string) (string, error) {
	cmd := helper.GitCommand("git", "--git-dir", path, "for-each-ref", "--format=%(refname)", "--count=1", prefix, "--contains", commitID)

	log.Printf("findRefName: RepoPath=%q commitSha=%s prefix=%s", path, commitID, prefix)

	output, err := cmd.Output()

	line := string(output)
	if err != nil {
		return "", nil
	}

	// Trailing spaces are not allowed per the documentation
	//  https://www.kernel.org/pub/software/scm/git/docs/git-check-ref-format.html
	refName := strings.TrimSpace(line) // Remove new-line

	return refName, nil
}
