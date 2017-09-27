package smarthttp

import (
	"context"
	"fmt"
	"io"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	log "github.com/sirupsen/logrus"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/streamio"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) InfoRefsUploadPack(in *pb.InfoRefsRequest, stream pb.SmartHTTPService_InfoRefsUploadPackServer) error {
	w := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&pb.InfoRefsResponse{Data: p})
	})
	return handleInfoRefs(stream.Context(), "upload-pack", in.Repository, w)
}

func (s *server) InfoRefsReceivePack(in *pb.InfoRefsRequest, stream pb.SmartHTTPService_InfoRefsReceivePackServer) error {
	w := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&pb.InfoRefsResponse{Data: p})
	})
	return handleInfoRefs(stream.Context(), "receive-pack", in.Repository, w)
}

func handleInfoRefs(ctx context.Context, service string, repo *pb.Repository, w io.Writer) error {
	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"service": service,
	}).Debug("handleInfoRefs")

	repoPath, err := helper.GetRepoPath(repo)
	if err != nil {
		return err
	}

	cmd, err := git.Command(ctx, repo, service, "--stateless-rpc", "--advertise-refs", repoPath)
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return err
		}
		return grpc.Errorf(codes.Internal, "GetInfoRefs: cmd: %v", err)
	}

	if err := pktLine(w, fmt.Sprintf("# service=git-%s\n", service)); err != nil {
		return grpc.Errorf(codes.Internal, "GetInfoRefs: pktLine: %v", err)
	}

	if err := pktFlush(w); err != nil {
		return grpc.Errorf(codes.Internal, "GetInfoRefs: pktFlush: %v", err)
	}

	if _, err := io.Copy(w, cmd); err != nil {
		return grpc.Errorf(codes.Internal, "GetInfoRefs: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		return grpc.Errorf(codes.Internal, "GetInfoRefs: %v", err)
	}

	return nil
}

func pktLine(w io.Writer, s string) error {
	_, err := fmt.Fprintf(w, "%04x%s", len(s)+4, s)
	return err
}

func pktFlush(w io.Writer) error {
	_, err := fmt.Fprint(w, "0000")
	return err
}
