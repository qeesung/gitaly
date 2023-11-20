package commit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

var maxNumStatBatchSize = 10

func (s *server) ListLastCommitsForTree(in *gitalypb.ListLastCommitsForTreeRequest, stream gitalypb.CommitService_ListLastCommitsForTreeServer) error {
	if err := validateListLastCommitsForTreeRequest(s.locator, in); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	if err := s.listLastCommitsForTree(in, stream); err != nil {
		return structerr.NewInternal("%w", err)
	}

	return nil
}

func (s *server) listLastCommitsForTree(in *gitalypb.ListLastCommitsForTreeRequest, stream gitalypb.CommitService_ListLastCommitsForTreeServer) error {
	ctx := stream.Context()
	repo := s.localrepo(in.GetRepository())

	if _, err := repo.Path(); err != nil {
		return err
	}

	cmd, parser, err := newLSTreeParser(ctx, repo, in)
	if err != nil {
		return err
	}

	objectReader, cancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return err
	}
	defer cancel()

	batch := make([]*gitalypb.ListLastCommitsForTreeResponse_CommitForTree, 0, maxNumStatBatchSize)
	entries, err := getLSTreeEntries(parser)
	if err != nil {
		return err
	}

	offset := int(in.GetOffset())
	if offset >= len(entries) {
		offset = 0
		entries = localrepo.Entries{}
	}

	limit := offset + int(in.GetLimit())
	if limit > len(entries) {
		limit = len(entries)
	}

	for _, entry := range entries[offset:limit] {
		commit, err := log.LastCommitForPath(ctx, s.gitCmdFactory, objectReader, repo, git.Revision(in.GetRevision()), entry.Path, in.GetGlobalOptions())
		if err != nil {
			return err
		}

		commitForTree := &gitalypb.ListLastCommitsForTreeResponse_CommitForTree{
			PathBytes: []byte(entry.Path),
			Commit:    commit.GitCommit,
		}

		batch = append(batch, commitForTree)
		if len(batch) == maxNumStatBatchSize {
			if err := sendCommitsForTree(batch, stream); err != nil {
				return err
			}

			batch = batch[0:0]
		}
	}

	if err := cmd.Wait(); err != nil {
		return err
	}

	return sendCommitsForTree(batch, stream)
}

func getLSTreeEntries(parser *localrepo.Parser) (localrepo.Entries, error) {
	entries := localrepo.Entries{}

	for {
		entry, err := parser.NextEntry()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, err
		}

		entries = append(entries, *entry)
	}

	sort.Stable(entries)

	return entries, nil
}

func newLSTreeParser(
	ctx context.Context,
	repo git.RepositoryExecutor,
	in *gitalypb.ListLastCommitsForTreeRequest,
) (*command.Command, *localrepo.Parser, error) {
	path := string(in.GetPath())
	if path == "" || path == "/" {
		path = "."
	}

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("detecting object hash: %w", err)
	}

	opts := git.ConvertGlobalOptions(in.GetGlobalOptions())
	cmd, err := repo.Exec(ctx, git.Command{
		Name:        "ls-tree",
		Flags:       []git.Option{git.Flag{Name: "-z"}, git.Flag{Name: "--full-name"}},
		Args:        []string{in.GetRevision()},
		PostSepArgs: []string{path},
	}, append(opts, git.WithSetupStdout())...)
	if err != nil {
		return nil, nil, err
	}

	return cmd, localrepo.NewParser(cmd, objectHash), nil
}

func sendCommitsForTree(batch []*gitalypb.ListLastCommitsForTreeResponse_CommitForTree, stream gitalypb.CommitService_ListLastCommitsForTreeServer) error {
	if len(batch) == 0 {
		return nil
	}

	if err := stream.Send(&gitalypb.ListLastCommitsForTreeResponse{Commits: batch}); err != nil {
		return err
	}

	return nil
}

func validateListLastCommitsForTreeRequest(locator storage.Locator, in *gitalypb.ListLastCommitsForTreeRequest) error {
	if err := locator.ValidateRepository(in.GetRepository()); err != nil {
		return err
	}
	if err := git.ValidateRevision([]byte(in.Revision)); err != nil {
		return err
	}
	if in.GetOffset() < 0 {
		return fmt.Errorf("offset negative")
	}
	if in.GetLimit() < 0 {
		return fmt.Errorf("limit negative")
	}
	return nil
}
