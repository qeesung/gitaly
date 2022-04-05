package commit

import (
	"fmt"
	"io"
	"regexp"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v14/streamio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var validBlameRange = regexp.MustCompile(`\A\d+,\d+\z`)

func (s *server) RawBlame(in *gitalypb.RawBlameRequest, stream gitalypb.CommitService_RawBlameServer) error {
	if err := validateRawBlameRequest(in); err != nil {
		return status.Errorf(codes.InvalidArgument, "RawBlame: %v", err)
	}

	ctx := stream.Context()
	revision := string(in.GetRevision())
	path := string(in.GetPath())
	blameRange := string(in.GetRange())

	flags := []git.Option{git.Flag{Name: "-p"}}
	if blameRange != "" {
		flags = append(flags, git.ValueFlag{Name: "-L", Value: blameRange})
	}

	cmd, err := s.gitCmdFactory.New(ctx, in.Repository, git.SubCmd{
		Name:        "blame",
		Flags:       flags,
		Args:        []string{revision},
		PostSepArgs: []string{path},
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return err
		}
		return status.Errorf(codes.Internal, "RawBlame: cmd: %v", err)
	}

	sw := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.RawBlameResponse{Data: p})
	})

	_, err = io.Copy(sw, cmd)
	if err != nil {
		return status.Errorf(codes.Unavailable, "RawBlame: send: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		ctxlogrus.Extract(ctx).WithError(err).Info("ignoring git-blame error")
	}

	return nil
}

func validateRawBlameRequest(in *gitalypb.RawBlameRequest) error {
	if err := git.ValidateRevision(in.Revision); err != nil {
		return err
	}

	if len(in.GetPath()) == 0 {
		return fmt.Errorf("empty Path")
	}

	blameRange := in.GetRange()
	if len(blameRange) > 0 && !validBlameRange.Match(blameRange) {
		return fmt.Errorf("invalid Range")
	}

	return nil
}
