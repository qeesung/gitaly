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
func (server) RefExists(ctx context.Context, in *pb.RefExistsRequest) (*pb.RefExistsResponse, error) {
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
	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"ref": ref,
	}).Debug("refExists")

	if !isValidRefName(ref) {
		return false, grpc.Errorf(codes.InvalidArgument, "invalid refname")
	}

	osCommand := exec.Command(helper.GitPath(), "--git-dir", repoPath, "show-ref", "--verify", "--quiet", ref)
	cmd, err := helper.NewCommand(ctx, osCommand, nil, ioutil.Discard, nil)
	if err != nil {
		return false, grpc.Errorf(codes.Internal, err.Error())
	}
	defer cmd.Close()

	err = cmd.Wait()
	if err == nil {
		// Exit code 0: the ref exists
		return true, nil
	}

	if code, ok := helper.ExitStatus(err); ok && code == 1 {
		// Exit code 1: the ref does not exist
		return false, nil
	}

	// This will normally occur when exit code > 1
	return false, grpc.Errorf(codes.Internal, err.Error())
}

func isValidRefName(refName string) bool {
	return strings.HasPrefix(refName, "refs/")
}
