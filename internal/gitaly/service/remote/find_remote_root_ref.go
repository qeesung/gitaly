package remote

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const headPrefix = "HEAD branch: "

func (s *server) findRemoteRootRefCmd(ctx context.Context, request *gitalypb.FindRemoteRootRefRequest) (*command.Command, error) {
	remoteURL := request.GetRemoteUrl()
	var config []git.ConfigPair

	if resolvedAddress := request.GetResolvedAddress(); resolvedAddress != "" {
		modifiedURL, resolveConfig, err := git.GetURLAndResolveConfig(remoteURL, resolvedAddress)
		if err != nil {
			return nil, structerr.NewInvalidArgument("couldn't get curloptResolve config: %w", err)
		}

		remoteURL = modifiedURL
		config = append(config, resolveConfig...)
	}

	config = append(config, git.ConfigPair{Key: "remote.inmemory.url", Value: remoteURL})

	if authHeader := request.GetHttpAuthorizationHeader(); authHeader != "" {
		config = append(config, git.ConfigPair{
			Key:   fmt.Sprintf("http.%s.extraHeader", request.RemoteUrl),
			Value: "Authorization: " + authHeader,
		})
	}

	return s.gitCmdFactory.New(ctx, request.Repository,
		git.Command{
			Name:   "remote",
			Action: "show",
			Args:   []string{"inmemory"},
		},
		git.WithDisabledHooks(),
		git.WithConfigEnv(config...),
		git.WithSetupStdout(),
	)
}

func (s *server) findRemoteRootRef(ctx context.Context, request *gitalypb.FindRemoteRootRefRequest) (string, error) {
	cmd, err := s.findRemoteRootRefCmd(ctx, request)
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(cmd)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, headPrefix) {
			rootRef := strings.TrimPrefix(line, headPrefix)
			if rootRef == "(unknown)" {
				return "", structerr.NewNotFound("no remote HEAD found")
			}
			return rootRef, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	if err := cmd.Wait(); err != nil {
		return "", err
	}

	return "", structerr.NewNotFound("couldn't query the remote HEAD")
}

// FindRemoteRootRef queries the remote to determine its HEAD
func (s *server) FindRemoteRootRef(ctx context.Context, in *gitalypb.FindRemoteRootRefRequest) (*gitalypb.FindRemoteRootRefResponse, error) {
	if in.GetRemoteUrl() == "" {
		return nil, structerr.NewInvalidArgument("missing remote URL")
	}
	if err := s.locator.ValidateRepository(ctx, in.GetRepository()); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	ref, err := s.findRemoteRootRef(ctx, in)
	if err != nil {
		return nil, structerr.NewInternal("%w", err)
	}

	return &gitalypb.FindRemoteRootRefResponse{Ref: ref}, nil
}
