package repository

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) CalculateChecksum(ctx context.Context, in *gitalypb.CalculateChecksumRequest) (*gitalypb.CalculateChecksumResponse, error) {
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}
	repo := repository
	repoPath, err := s.locator.GetRepoPath(ctx, repo)
	if err != nil {
		return nil, err
	}

	cmd, err := s.gitCmdFactory.New(ctx, repo, git.Command{
		Name: "show-ref",
		Flags: []git.Option{
			git.Flag{Name: "--head"},
		},
	}, git.WithSetupStdout())
	if err != nil {
		return nil, structerr.NewInternal("gitCommand: %w", err)
	}

	var checksum git.Checksum

	scanner := bufio.NewScanner(cmd)
	for scanner.Scan() {
		checksum.AddBytes(scanner.Bytes())
	}

	if err := scanner.Err(); err != nil {
		return nil, structerr.NewInternal("%w", err)
	}

	if err := cmd.Wait(); checksum.IsZero() || err != nil {
		if s.isValidRepo(ctx, repo) {
			return &gitalypb.CalculateChecksumResponse{Checksum: git.ZeroChecksum}, nil
		}

		return nil, structerr.NewDataLoss("not a git repository '%s'", repoPath)
	}

	return &gitalypb.CalculateChecksumResponse{Checksum: hex.EncodeToString(checksum.Bytes())}, nil
}

func (s *server) isValidRepo(ctx context.Context, repo *gitalypb.Repository) bool {
	stdout := &bytes.Buffer{}
	cmd, err := s.gitCmdFactory.New(ctx, repo,
		git.Command{
			Name: "rev-parse",
			Flags: []git.Option{
				git.Flag{Name: "--is-bare-repository"},
			},
		},
		git.WithStdout(stdout),
	)
	if err != nil {
		return false
	}

	if err := cmd.Wait(); err != nil {
		return false
	}

	return strings.EqualFold(strings.TrimRight(stdout.String(), "\n"), "true")
}
