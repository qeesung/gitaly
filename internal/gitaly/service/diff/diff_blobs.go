package diff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/quarantine"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/diff"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) DiffBlobs(request *gitalypb.DiffBlobsRequest, stream gitalypb.DiffService_DiffBlobsServer) error {
	ctx := stream.Context()

	if err := s.locator.ValidateRepository(ctx, request.GetRepository()); err != nil {
		return err
	}

	// Unfortunately, git-diff(1) does not support generating a blob diff using a null OID as an
	// input argument. When a blob is added/deleted, there is no pre-image/post-image respectively.
	// To generate diffs for additions and deletions, the empty blob ID is used as either the left
	// of right blob pair. Unlike an empty tree object, an empty blob object is not special cased
	// and must exist in the repository to be used. Since the DiffBlobs RPC is read-only, we create
	// a quarantine directory to stage an empty blob object for use with diff generation only.
	quarantineDir, err := quarantine.New(ctx, request.GetRepository(), s.logger, s.locator)
	if err != nil {
		return structerr.NewInternal("creating quarantine directory: %w", err)
	}

	repo := s.localrepo(quarantineDir.QuarantinedRepo())

	if _, err := repo.WriteBlob(ctx, strings.NewReader(""), localrepo.WriteBlobConfig{}); err != nil {
		return structerr.NewInternal("writing empty blob: %w", err)
	}

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

	var limits diff.Limits
	if request.PatchBytesLimit > 0 {
		limits.EnforceLimits = true
		limits.PatchLimitsOnly = true
		limits.MaxPatchBytes = int(request.PatchBytesLimit)
	}

	for _, blobPair := range request.BlobPairs {
		// Each diff gets computed using an independent Git process and diff parser. Ideally a
		// single Git process could be used to process each blob pair, but unfortunately Git
		// does not yet have a means to accomplish this.
		blobDiff, err := diffBlob(ctx, repo, objectHash, blobPair, limits, cmdOpts)
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
	left := string(blobPair.LeftBlob)
	right := string(blobPair.RightBlob)

	emptyBlob, err := emptyBlobID(objectHash)
	if err != nil {
		return nil, err
	}

	// Rewrite null OIDs to an empty blob ID so diffs can be generated for additions and deletions.
	if objectHash.IsZeroOID(git.ObjectID(left)) {
		left = emptyBlob.String()
	}

	if objectHash.IsZeroOID(git.ObjectID(right)) {
		right = emptyBlob.String()
	}

	gitCmd := git.Command{
		Name: "diff",
		Flags: []git.Option{
			// The diff parser requires raw output even if only a single diff is generated.
			git.Flag{Name: "--patch-with-raw"},
			git.Flag{Name: fmt.Sprintf("--abbrev=%d", objectHash.EncodedLen())},
		},
		Args: []string{left, right},
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

	blobDiff := diffParser.Diff()

	// If a null OID was initially requested, rewrite the empty blob ID back to a null OID.
	if objectHash.IsZeroOID(git.ObjectID(blobPair.LeftBlob)) {
		blobDiff.FromID = objectHash.ZeroOID.String()
	}

	if objectHash.IsZeroOID(git.ObjectID(blobPair.RightBlob)) {
		blobDiff.ToID = objectHash.ZeroOID.String()
	}

	return blobDiff, nil
}

func (s *server) sendDiff(stream gitalypb.DiffService_DiffBlobsServer, diff *diff.Diff) error {
	response := &gitalypb.DiffBlobsResponse{
		LeftBlobId:          diff.FromID,
		RightBlobId:         diff.ToID,
		Binary:              diff.Binary,
		OverPatchBytesLimit: diff.TooLarge,
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
		leftNullOID := objectHash.IsZeroOID(git.ObjectID(blobPair.LeftBlob))
		rightNullOID := objectHash.IsZeroOID(git.ObjectID(blobPair.RightBlob))

		if leftNullOID && rightNullOID {
			return structerr.NewInvalidArgument("left and right blob cannot both be null OIDs")
		}

		// Null blob IDs do not exist in the repository.
		if !leftNullOID {
			if err := validateBlob(ctx, reader, objectHash, blobPair.LeftBlob); err != nil {
				return structerr.NewInvalidArgument("validating left blob: %w", err).WithMetadata(
					"revision",
					string(blobPair.LeftBlob),
				)
			}
		}

		if !rightNullOID {
			if err := validateBlob(ctx, reader, objectHash, blobPair.RightBlob); err != nil {
				return structerr.NewInvalidArgument("validating right blob: %w", err).WithMetadata(
					"revision",
					string(blobPair.RightBlob),
				)
			}
		}
	}

	return nil
}

func validateBlob(ctx context.Context, reader catfile.ObjectInfoReader, objectHash git.ObjectHash, revision []byte) error {
	// Since only blobs are allowed, only path-scoped revisions and blob IDs are accepted.
	if bytes.Contains(revision, []byte(":")) {
		if err := git.ValidateRevision(revision, git.AllowPathScopedRevision()); err != nil {
			return fmt.Errorf("validating path-scoped revision: %w", err)
		}
	} else {
		if err := objectHash.ValidateHex(string(revision)); err != nil {
			return fmt.Errorf("validating blob ID: %w", err)
		}
	}

	if info, err := reader.Info(ctx, git.Revision(revision)); err != nil {
		return fmt.Errorf("getting revision info: %w", err)
	} else if !info.IsBlob() {
		return errors.New("revision is not blob")
	}

	return nil
}

func emptyBlobID(objectHash git.ObjectHash) (git.ObjectID, error) {
	switch objectHash.Format {
	case git.ObjectHashSHA1.Format:
		return "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391", nil
	case git.ObjectHashSHA256.Format:
		return "473a0f4c3be8a93681a267e3b1e9a7dcda1185436fe141f7749120a303721813", nil
	default:
		return "", fmt.Errorf("unknown object format: %q", objectHash.Format)
	}
}
