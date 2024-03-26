package repository

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func (s *server) GetInfoAttributes(in *gitalypb.GetInfoAttributesRequest, stream gitalypb.RepositoryService_GetInfoAttributesServer) (returnedErr error) {
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(repository); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}
	repoPath, err := s.locator.GetRepoPath(repository)
	if err != nil {
		return err
	}

	// In git 2.43.0+, gitattributes supports reading from HEAD:.gitattributes,
	// so info/attributes is no longer needed. To make sure info/attributes file is cleaned up,
	// we delete it if it exists when reading from HEAD:.gitattributes is called.
	// This logic can be removed when ApplyGitattributes and GetInfoAttributes PRC are totally removed from
	// the code base.
	if deletionErr := deleteInfoAttributesFile(repoPath); deletionErr != nil {
		return fmt.Errorf("fail to delete info/gitattributes file at %s: %w", repoPath, deletionErr)
	}

	repo := s.localrepo(in.GetRepository())
	ctx := stream.Context()
	var stderr strings.Builder
	// Call cat-file -p HEAD:.gitattributes instead of cat info/attributes
	catFileCmd, err := repo.Exec(ctx, git.Command{
		Name: "cat-file",
		Flags: []git.Option{
			git.Flag{Name: "-p"},
		},
		Args: []string{"HEAD:.gitattributes"},
	},
		git.WithSetupStdout(),
		git.WithStderr(&stderr),
	)
	if err != nil {
		return structerr.NewInternal("failure to read HEAD:.gitattributes: %w", err)
	}
	defer func() {
		if err := catFileCmd.Wait(); err != nil {
			s.logger.Error("git cat-file command error: " + stderr.String())
			if returnedErr != nil {
				returnedErr = structerr.NewInternal("failure to read HEAD:.gitattributes: %w", err)
			}
		}
	}()

	buf := bufio.NewReader(catFileCmd)
	_, err = buf.Peek(1)
	if errors.Is(err, io.EOF) {
		return stream.Send(&gitalypb.GetInfoAttributesResponse{})
	}

	sw := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.GetInfoAttributesResponse{
			Attributes: p,
		})
	})
	_, err = io.Copy(sw, buf)

	return err
}
