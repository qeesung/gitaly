package commit

import (
	"io"

	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/lstree"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/helper/chunker"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) ListFiles(in *gitalypb.ListFilesRequest, stream gitalypb.CommitService_ListFilesServer) error {
	grpc_logrus.Extract(stream.Context()).WithFields(log.Fields{
		"Revision": in.GetRevision(),
	}).Debug("ListFiles")

	repo := in.Repository
	if _, err := helper.GetRepoPath(repo); err != nil {
		return err
	}

	revision := in.GetRevision()
	if len(revision) == 0 {
		var err error

		revision, err = defaultBranchName(stream.Context(), repo)
		if err != nil {
			if _, ok := status.FromError(err); ok {
				return err
			}
			return status.Errorf(codes.NotFound, "Revision not found %q", in.GetRevision())
		}
	}
	if !git.IsValidRef(stream.Context(), repo, string(revision)) {
		return stream.Send(&gitalypb.ListFilesResponse{})
	}

	cmd, err := git.Command(stream.Context(), repo, "ls-tree", "-z", "-r", "--full-tree", "--full-name", "--", string(revision))
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return err
		}
		return status.Errorf(codes.Internal, err.Error())
	}

	sender := chunker.New(&listFilesSender{stream: stream})

	for parser := lstree.NewParser(cmd); ; {
		entry, err := parser.NextEntry()
		if err != nil {
			if err == io.EOF {
				break // happy path
			}
			return err
		}

		if entry.Type != lstree.Blob {
			continue
		}

		if err := sender.Send([]byte(entry.Path)); err != nil {
			return err
		}
	}

	return sender.Flush()
}

type listFilesSender struct {
	stream   gitalypb.CommitService_ListFilesServer
	response *gitalypb.ListFilesResponse
}

func (s *listFilesSender) Reset()      { s.response = &gitalypb.ListFilesResponse{} }
func (s *listFilesSender) Send() error { return s.stream.Send(s.response) }
func (s *listFilesSender) Append(it chunker.Item) {
	s.response.Paths = append(s.response.Paths, it.([]byte))
}
