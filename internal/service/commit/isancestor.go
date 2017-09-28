package commit

import (
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"golang.org/x/net/context"

	log "github.com/sirupsen/logrus"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git"
)

func (s *server) CommitIsAncestor(ctx context.Context, in *pb.CommitIsAncestorRequest) (*pb.CommitIsAncestorResponse, error) {
	if in.AncestorId == "" {
		return nil, grpc.Errorf(codes.InvalidArgument, "Bad Request (empty ancestor sha)")
	}
	if in.ChildId == "" {
		return nil, grpc.Errorf(codes.InvalidArgument, "Bad Request (empty child sha)")
	}

	ret, err := commitIsAncestorName(ctx, in.Repository, in.AncestorId, in.ChildId)
	return &pb.CommitIsAncestorResponse{Value: ret}, err
}

// Assumes that `path`, `ancestorID` and `childID` are populated :trollface:
func commitIsAncestorName(ctx context.Context, repo *pb.Repository, ancestorID, childID string) (bool, error) {
	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"ancestorSha": ancestorID,
		"childSha":    childID,
	}).Debug("commitIsAncestor")

	cmd, err := git.Command(ctx, repo, "merge-base", "--is-ancestor", ancestorID, childID)
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return false, err
		}
		return false, grpc.Errorf(codes.Internal, err.Error())
	}

	return cmd.Wait() == nil, nil
}
