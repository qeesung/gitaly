package commit

import (
	"context"
	"errors"
	"fmt"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

func (s *server) ListFiles(in *gitalypb.ListFilesRequest, stream gitalypb.CommitService_ListFilesServer) error {
	ctx := stream.Context()

	s.logger.WithFields(log.Fields{
		"Revision": in.GetRevision(),
	}).DebugContext(ctx, "ListFiles")

	if err := validateListFilesRequest(ctx, s.locator, in); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(in.GetRepository())
	if _, err := repo.Path(ctx); err != nil {
		return err
	}

	revision := string(in.GetRevision())
	if len(revision) == 0 {
		defaultBranch, err := repo.GetDefaultBranch(ctx)
		if err != nil {
			return structerr.NewNotFound("revision not found %q", revision)
		}

		if len(defaultBranch) == 0 {
			return structerr.NewFailedPrecondition("repository does not have a default branch")
		}

		revision = defaultBranch.String()
	}

	contained, err := s.localrepo(repo).HasRevision(ctx, git.Revision(revision))
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	if !contained {
		return stream.Send(&gitalypb.ListFilesResponse{})
	}

	if err := s.listFiles(repo, revision, stream); err != nil {
		return structerr.NewInternal("%w", err)
	}

	return nil
}

func validateListFilesRequest(ctx context.Context, locator storage.Locator, in *gitalypb.ListFilesRequest) error {
	if err := locator.ValidateRepository(ctx, in.GetRepository()); err != nil {
		return err
	}
	if err := git.ValidateRevision(in.Revision, git.AllowEmptyRevision()); err != nil {
		return err
	}
	return nil
}

func (s *server) listFiles(repo git.RepositoryExecutor, revision string, stream gitalypb.CommitService_ListFilesServer) error {
	ctx := stream.Context()

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object hash: %w", err)
	}

	cmd, err := repo.Exec(ctx, git.Command{
		Name: "ls-tree",
		Flags: []git.Option{
			git.Flag{Name: "-z"},
			git.Flag{Name: "-r"},
			git.Flag{Name: "--full-tree"},
			git.Flag{Name: "--full-name"},
		},
		Args: []string{revision},
	}, git.WithSetupStdout())
	if err != nil {
		return err
	}

	sender := chunk.New(&listFilesSender{stream: stream})

	for parser := localrepo.NewParser(cmd, objectHash); ; {
		entry, err := parser.NextEntry()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		if entry.Type != localrepo.Blob {
			continue
		}

		if err := sender.Send(&gitalypb.ListFilesResponse{Paths: [][]byte{[]byte(entry.Path)}}); err != nil {
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
func (s *listFilesSender) Append(m proto.Message) {
	s.response.Paths = append(s.response.Paths, m.(*gitalypb.ListFilesResponse).Paths...)
}
