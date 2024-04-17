package diff

import (
	"context"
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/diff"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) DiffBlobs(request *gitalypb.DiffBlobsRequest, stream gitalypb.DiffService_DiffBlobsServer) error {
	ctx := stream.Context()

	if err := s.locator.ValidateRepository(request.GetRepository()); err != nil {
		return err
	}

	repo := s.localrepo(request.GetRepository())

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return structerr.NewInternal("detecting object format: %w", err)
	}

	if err := s.validateBlobPairs(ctx, repo, objectHash, request.BlobPairs); err != nil {
		return err
	}

	var cmdOpts []git.Option

	switch request.GetWhitespaceChanges() {
	case gitalypb.DiffBlobsRequest_WHITESPACE_CHANGES_IGNORE_ALL:
		cmdOpts = append(cmdOpts, git.Flag{Name: "--ignore-all-space"})
	case gitalypb.DiffBlobsRequest_WHITESPACE_CHANGES_IGNORE:
		cmdOpts = append(cmdOpts, git.Flag{Name: "--ignore-space-change"})
	}

	if request.GetDiffMode() == gitalypb.DiffBlobsRequest_DIFF_MODE_WORD {
		cmdOpts = append(cmdOpts, git.Flag{Name: "--word-diff=porcelain"})
	}

	for _, blobPair := range request.BlobPairs {
		// Each diff gets computed using an independent Git process and diff parser. Ideally a
		// single Git process could be used to process each blob pair, but unfortunately Git
		// does not yet have a means to accomplish this.
		blobDiff, err := diffBlob(ctx, repo, objectHash, blobPair, diff.Limits{}, cmdOpts)
		if err != nil {
			return structerr.NewInternal("generating diff: %w", err)
		}

		if err := s.sendDiff(stream, blobDiff); err != nil {
			return structerr.NewInternal("sending diff: %w", err)
		}
	}

	return nil
}

func diffBlob(ctx context.Context,
	repo *localrepo.Repo,
	objectHash git.ObjectHash,
	blobPair *gitalypb.DiffBlobsRequest_BlobPair,
	limits diff.Limits,
	opts []git.Option,
) (*diff.Diff, error) {
	gitCmd := git.Command{
		Name: "diff",
		Flags: []git.Option{
			// The diff parser requires raw output even if only a single diff is generated.
			git.Flag{Name: "--patch-with-raw"},
			git.Flag{Name: fmt.Sprintf("--abbrev=%d", objectHash.EncodedLen())},
		},
		Args: []string{blobPair.LeftOid, blobPair.RightOid},
	}

	gitCmd.Flags = append(gitCmd.Flags, opts...)

	cmd, err := repo.Exec(ctx, gitCmd, git.WithSetupStdout())
	if err != nil {
		return nil, fmt.Errorf("spawning git-diff: %w", err)
	}

	diffParser := diff.NewDiffParser(objectHash, cmd, limits)

	// Since a new parser is used for each computed diff, only a single diff should be generated.
	if !diffParser.Parse() {
		if diffParser.Err() != nil {
			return nil, diffParser.Err()
		}

		// Computing a diff using the same blob ID is not supported and results in an error. In this
		// scenario the `--raw` option would not produce any output and thus the parser thinks there
		// is no diffs to parse.
		return nil, errors.New("diff parser finished unexpectedly")
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("waiting for git-diff: %w", err)
	}

	return diffParser.Diff(), nil
}

func (s *server) sendDiff(stream gitalypb.DiffService_DiffBlobsServer, diff *diff.Diff) error {
	response := &gitalypb.DiffBlobsResponse{
		LeftBlobId:  diff.FromID,
		RightBlobId: diff.ToID,
		Binary:      diff.Binary,
	}

	for {
		if len(diff.Patch) > s.MsgSizeThreshold {
			response.Patch = diff.Patch[:s.MsgSizeThreshold]
			diff.Patch = diff.Patch[s.MsgSizeThreshold:]
		} else {
			response.Patch = diff.Patch
			response.Status = gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH
			diff.Patch = nil
		}

		if err := stream.Send(response); err != nil {
			return fmt.Errorf("send: %w", err)
		}

		if len(diff.Patch) == 0 {
			break
		}

		response = &gitalypb.DiffBlobsResponse{}
	}

	return nil
}

func (s *server) validateBlobPairs(
	ctx context.Context,
	repo *localrepo.Repo,
	objectHash git.ObjectHash,
	blobPairs []*gitalypb.DiffBlobsRequest_BlobPair,
) error {
	reader, readerCancel, err := s.catfileCache.ObjectInfoReader(ctx, repo)
	if err != nil {
		return fmt.Errorf("retrieving object reader: %w", err)
	}
	defer readerCancel()

	for _, blobPair := range blobPairs {
		if err := objectHash.ValidateHex(blobPair.LeftOid); err != nil {
			return structerr.NewInvalidArgument("left blob ID is invalid hash")
		}

		if info, err := reader.Info(ctx, git.Revision(blobPair.LeftOid)); err != nil {
			return err
		} else if !info.IsBlob() {
			return structerr.NewInvalidArgument("left blob ID is not blob")
		}

		if err := objectHash.ValidateHex(blobPair.RightOid); err != nil {
			return structerr.NewInvalidArgument("right blob ID is invalid hash")
		}

		if info, err := reader.Info(ctx, git.Revision(blobPair.RightOid)); err != nil {
			return err
		} else if !info.IsBlob() {
			return structerr.NewInvalidArgument("right blob ID is not blob")
		}
	}

	return nil
}
