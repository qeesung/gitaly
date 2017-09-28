package ref

import (
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"golang.org/x/net/context"
)

// RefExists returns true if the given reference exists. The ref must start with the string `ref/`
func (server) RefExists(ctx context.Context, in *pb.RefExistsRequest) (*pb.RefExistsResponse, error) {
	ref := string(in.Ref)
	exists, err := refExists(ctx, in.Repository, ref)
	if err != nil {
		return nil, err
	}

	return &pb.RefExistsResponse{Value: exists}, nil
}

func refExists(ctx context.Context, repo *pb.Repository, ref string) (bool, error) {
	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"ref": ref,
	}).Debug("refExists")

	if !isValidRefName(ref) {
		return false, grpc.Errorf(codes.InvalidArgument, "invalid refname")
	}

	cmd, err := git.Command(ctx, repo, "show-ref", "--verify", "--quiet", ref)
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return false, err
		}
		return false, grpc.Errorf(codes.Internal, err.Error())
	}

	err = cmd.Wait()
	if err == nil {
		// Exit code 0: the ref exists
		return true, nil
	}

	if code, ok := command.ExitStatus(err); ok && code == 1 {
		// Exit code 1: the ref does not exist
		return false, nil
	}

	// This will normally occur when exit code > 1
	return false, grpc.Errorf(codes.Internal, err.Error())
}

func isValidRefName(refName string) bool {
	return strings.HasPrefix(refName, "refs/")
}
