package repository

import (
	"context"
	"fmt"
	"os"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) CreateFork(ctx context.Context, req *gitalypb.CreateForkRequest) (*gitalypb.CreateForkResponse, error) {
	targetRepository := req.Repository
	sourceRepository := req.SourceRepository

	if sourceRepository == nil {
		return nil, status.Errorf(codes.InvalidArgument, "CreateFork: empty SourceRepository")
	}
	if targetRepository == nil {
		return nil, status.Errorf(codes.InvalidArgument, "CreateFork: empty Repository")
	}

	if err := s.createRepository(ctx, targetRepository, func(repo *gitalypb.Repository) error {
		targetPath, err := s.locator.GetPath(repo)
		if err != nil {
			return err
		}

		// git-clone(1) doesn't allow for the target path to exist, so we have to
		// remove it first.
		if err := os.RemoveAll(targetPath); err != nil {
			return fmt.Errorf("removing target path: %w", err)
		}

		// Ideally we'd just fetch into the already-created repo, but that wouldn't
		// allow us to easily set up HEAD to point to the correct ref. We thus have
		// no easy choice but to use git-clone(1).
		var stderr strings.Builder
		cmd, err := s.gitCmdFactory.NewWithoutRepo(ctx,
			git.SubCmd{
				Name: "clone",
				Flags: []git.Option{
					git.Flag{Name: "--bare"},
					git.Flag{Name: "--quiet"},
				},
				Args: []string{
					git.InternalGitalyURL,
					targetPath,
				},
			},
			git.WithInternalFetch(&gitalypb.SSHUploadPackRequest{
				Repository: sourceRepository,
			}),
			git.WithConfig(git.ConfigPair{
				// Disable consistency checks for fetched objects when creating a
				// fork. We don't want to end up in a situation where it's
				// impossible to create forks we already have anyway because we have
				// e.g. retroactively tightened the consistency checks.
				Key: "fetch.fsckObjects", Value: "false",
			}),
			git.WithDisabledHooks(),
			git.WithStderr(&stderr),
		)
		if err != nil {
			return fmt.Errorf("spawning fetch: %w", err)
		}

		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("fetching source repo: %w, stderr: %q", err, stderr.String())
		}

		if err := s.removeOriginInRepo(ctx, repo); err != nil {
			return fmt.Errorf("removing origin remote: %w", err)
		}

		return nil
	}); err != nil {
		return nil, helper.ErrInternalf("creating fork: %w", err)
	}

	return &gitalypb.CreateForkResponse{}, nil
}
