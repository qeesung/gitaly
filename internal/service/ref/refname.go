package ref

import (
	"bufio"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	log "github.com/sirupsen/logrus"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"golang.org/x/net/context"
)

// FindRefName returns a ref that starts with the given prefix, if one exists.
//  If there is more than one such ref there is no guarantee which one is
//  returned or that the same one is returned on each call.
func (s *server) FindRefName(ctx context.Context, in *pb.FindRefNameRequest) (*pb.FindRefNameResponse, error) {
	if in.CommitId == "" {
		return nil, grpc.Errorf(codes.InvalidArgument, "Bad Request (empty commit sha)")
	}

	ref, err := findRefName(ctx, in.Repository, in.CommitId, string(in.Prefix))
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, grpc.Errorf(codes.Internal, err.Error())
	}

	return &pb.FindRefNameResponse{Name: []byte(ref)}, nil
}

// We assume `repo` and `commitID` and `prefix` are non-empty
func findRefName(ctx context.Context, repo *pb.Repository, commitID, prefix string) (string, error) {
	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"commitSha": commitID,
		"prefix":    prefix,
	}).Debug("findRefName")

	cmd, err := git.Command(ctx, repo, "for-each-ref", "--format=%(refname)", "--count=1", prefix, "--contains", commitID)
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(cmd)
	scanner.Scan()
	if err := scanner.Err(); err != nil {
		return "", err
	}
	refName := scanner.Text()

	if err := cmd.Wait(); err != nil {
		// We're suppressing the error since invalid commits isn't an error
		//  according to Rails
		return "", nil
	}

	// Trailing spaces are not allowed per the documentation
	//  https://www.kernel.org/pub/software/scm/git/docs/git-check-ref-format.html
	return strings.TrimSpace(refName), nil
}
