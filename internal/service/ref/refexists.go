package ref

import (
	"io/ioutil"
	"os/exec"
	"strings"

	log "github.com/Sirupsen/logrus"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"golang.org/x/net/context"
)

// RefExists returns true if the given reference exists. The ref must start with the string `ref/`
func (s *server) RefExists(ctx context.Context, in *pb.RefExistsRequest) (*pb.RefExistsResponse, error) {
	repoPath, err := helper.GetRepoPath(in.Repository)
	if err != nil {
		return nil, err
	}

	ref := string(in.Ref)

	exists, err := refExists(ctx, repoPath, ref)

	if err != nil {
		return nil, err
	}

	return &pb.RefExistsResponse{Value: exists}, nil
}

func refExists(ctx context.Context, repoPath string, ref string) (bool, error) {
	if !isValidRefName(ref) {
		return false, grpc.Errorf(codes.InvalidArgument, "invalid refname")
	}

	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"ref": ref,
	}).Debug("refExists")

	osCommand := exec.Command(helper.GitPath(), "--git-dir", repoPath, "show-ref", "--verify", "--quiet", ref)
	cmd, err := helper.NewCommand(ctx, osCommand, nil, ioutil.Discard, nil)
	if err != nil {
		return false, grpc.Errorf(codes.Internal, err.Error())
	}
	defer cmd.Close()

	err = cmd.Wait()

	if err == nil {
		return true, nil
	}

	if code, ok := helper.ExitStatus(err); ok {
		switch code {
		case 1:
			return code == 0, nil
		default:
			return false, grpc.Errorf(codes.Internal, err.Error())
		}

	}

	return false, grpc.Errorf(codes.Internal, err.Error())
}

func isValidRefName(refName string) bool {
	return strings.HasPrefix(refName, "refs/")
}
