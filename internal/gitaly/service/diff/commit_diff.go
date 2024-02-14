package diff

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/diff"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) CommitDiff(in *gitalypb.CommitDiffRequest, stream gitalypb.DiffService_CommitDiffServer) error {
	ctx := stream.Context()

	s.logger.WithFields(log.Fields{
		"LeftCommitId":  in.LeftCommitId,
		"RightCommitId": in.RightCommitId,
		"Paths":         logPaths(in.Paths),
	}).DebugContext(ctx, "CommitDiff")

	if err := validateRequest(s.locator, in); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	leftSha := in.LeftCommitId
	rightSha := in.RightCommitId
	whitespaceChanges := in.GetWhitespaceChanges()
	paths := in.GetPaths()

	repo := s.localrepo(in.GetRepository())

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object format: %w", err)
	}

	cmd := git.Command{
		Name: "diff",
		Flags: []git.Option{
			git.Flag{Name: "--patch"},
			git.Flag{Name: "--raw"},
			git.Flag{Name: fmt.Sprintf("--abbrev=%d", objectHash.EncodedLen())},
			git.Flag{Name: "--full-index"},
			git.Flag{Name: "--find-renames=30%"},
			git.Flag{Name: "--root"},
		},
		Args: []string{leftSha, rightSha},
	}

	if whitespaceChanges == gitalypb.CommitDiffRequest_WHITESPACE_CHANGES_IGNORE_ALL {
		cmd.Flags = append(cmd.Flags, git.Flag{Name: "--ignore-all-space"})
	} else if whitespaceChanges == gitalypb.CommitDiffRequest_WHITESPACE_CHANGES_IGNORE {
		cmd.Flags = append(cmd.Flags, git.Flag{Name: "--ignore-space-change"})
	}

	if in.GetDiffMode() == gitalypb.CommitDiffRequest_WORDDIFF {
		cmd.Flags = append(cmd.Flags, git.Flag{Name: "--word-diff=porcelain"})
	}
	if len(paths) > 0 {
		for _, path := range paths {
			cmd.PostSepArgs = append(cmd.PostSepArgs, string(path))
		}
	}

	var limits diff.Limits
	if in.EnforceLimits {
		limits.EnforceLimits = true
		limits.MaxFiles = int(in.MaxFiles)
		limits.MaxLines = int(in.MaxLines)
		limits.MaxBytes = int(in.MaxBytes)
		limits.MaxPatchBytes = int(in.MaxPatchBytes)

		if len(in.MaxPatchBytesForFileExtension) > 0 {
			limits.MaxPatchBytesForFileExtension = map[string]int{}

			for extension, size := range in.MaxPatchBytesForFileExtension {
				limits.MaxPatchBytesForFileExtension[extension] = int(size)
			}
		}
	}
	limits.CollapseDiffs = in.CollapseDiffs
	limits.CollectAllPaths = in.CollectAllPaths
	limits.SafeMaxFiles = int(in.SafeMaxFiles)
	limits.SafeMaxLines = int(in.SafeMaxLines)
	limits.SafeMaxBytes = int(in.SafeMaxBytes)

	return s.eachDiff(ctx, repo, cmd, limits, func(diff *diff.Diff) error {
		response := &gitalypb.CommitDiffResponse{
			FromPath:       diff.FromPath,
			ToPath:         diff.ToPath,
			FromId:         diff.FromID,
			ToId:           diff.ToID,
			OldMode:        diff.OldMode,
			NewMode:        diff.NewMode,
			Binary:         diff.Binary,
			OverflowMarker: diff.OverflowMarker,
			Collapsed:      diff.Collapsed,
			TooLarge:       diff.TooLarge,
		}

		if len(diff.Patch) <= s.MsgSizeThreshold {
			response.RawPatchData = diff.Patch
			response.EndOfPatch = true

			if err := stream.Send(response); err != nil {
				return structerr.NewAborted("send: %w", err)
			}
		} else {
			patch := diff.Patch

			for len(patch) > 0 {
				if len(patch) > s.MsgSizeThreshold {
					response.RawPatchData = patch[:s.MsgSizeThreshold]
					patch = patch[s.MsgSizeThreshold:]
				} else {
					response.RawPatchData = patch
					response.EndOfPatch = true
					patch = nil
				}

				if err := stream.Send(response); err != nil {
					return structerr.NewAborted("send: %w", err)
				}

				// Use a new response so we don't send other fields (FromPath, ...) over and over
				response = &gitalypb.CommitDiffResponse{}
			}
		}

		return nil
	})
}
