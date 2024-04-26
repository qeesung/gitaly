package hook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/env"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// NotAllowedError is needed to report internal API errors that
// are made by the pre-receive hook.
type NotAllowedError struct {
	// Message is the error message returned by Rails.
	Message string
	// Protocol is the protocol used.
	Protocol string
	// userID is the ID of the user as whom we have performed access checks.
	UserID string
	// Changes is the changes we have requested.
	Changes []byte
}

func (e NotAllowedError) Error() string {
	return fmt.Sprintf("GitLab: %s", e.Message)
}

func getRelativeObjectDirs(repoPath, gitObjectDir, gitAlternateObjectDirs string) (string, []string, error) {
	repoPathReal, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		return "", nil, err
	}

	gitObjDirRel, err := filepath.Rel(repoPathReal, gitObjectDir)
	if err != nil {
		return "", nil, err
	}

	var gitAltObjDirsRel []string

	for _, gitAltObjDirAbs := range strings.Split(gitAlternateObjectDirs, ":") {
		gitAltObjDirRel, err := filepath.Rel(repoPathReal, gitAltObjDirAbs)
		if err != nil {
			return "", nil, err
		}

		gitAltObjDirsRel = append(gitAltObjDirsRel, gitAltObjDirRel)
	}

	return gitObjDirRel, gitAltObjDirsRel, nil
}

// PreReceiveHook will try to authenticate the changes against the GitLab API.
// If successful, it will execute custom hooks with the given parameters, push
// options and environment.
func (m *GitLabHookManager) PreReceiveHook(ctx context.Context, repo *gitalypb.Repository, pushOptions, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	payload, err := git.HooksPayloadFromEnv(env)
	if err != nil {
		return structerr.NewInternal("extracting hooks payload: %w", err)
	}

	changes, err := io.ReadAll(stdin)
	if err != nil {
		return structerr.NewInternal("reading stdin from request: %w", err)
	}

	// Only the primary should execute hooks and increment reference counters.
	if isPrimary(payload) {
		if err := m.preReceiveHook(ctx, payload, repo, pushOptions, env, changes, stdout, stderr); err != nil {
			m.logger.WithError(err).WarnContext(ctx, "stopping transaction because pre-receive hook failed")

			// If the pre-receive hook declines the push, then we need to stop any
			// secondaries voting on the transaction.
			if err := m.stopTransaction(ctx, payload); err != nil {
				m.logger.WithError(err).ErrorContext(ctx, "failed stopping transaction in pre-receive hook")
			}

			return err
		}
	}

	if err := m.synchronizeHookExecution(ctx, payload, "pre-receive"); err != nil {
		return fmt.Errorf("synchronizing pre-receive hook: %w", err)
	}

	return nil
}

func (m *GitLabHookManager) preReceiveHook(ctx context.Context, payload git.HooksPayload, repo *gitalypb.Repository, pushOptions, envs []string, changes []byte, stdout, stderr io.Writer) error {
	repoPath, err := m.locator.GetRepoPath(repo)
	if err != nil {
		return structerr.NewInternal("getting repo path: %w", err)
	}

	if gitObjDir, gitAltObjDirs := env.ExtractValue(envs, "GIT_OBJECT_DIRECTORY"), env.ExtractValue(envs, "GIT_ALTERNATE_OBJECT_DIRECTORIES"); gitObjDir != "" && gitAltObjDirs != "" {
		gitObjectDirRel, gitAltObjectDirRel, err := getRelativeObjectDirs(repoPath, gitObjDir, gitAltObjDirs)
		if err != nil {
			return structerr.NewInternal("getting relative git object directories: %w", err)
		}

		repo.GitObjectDirectory = gitObjectDirRel
		repo.GitAlternateObjectDirectories = gitAltObjectDirRel
	}

	if len(changes) == 0 {
		return structerr.NewInternal("hook got no reference updates")
	}

	if repo.GetGlRepository() == "" {
		return structerr.NewInternal("repository not set")
	}
	if payload.UserDetails == nil {
		return structerr.NewInternal("payload has no receive hooks info")
	}
	if payload.UserDetails.UserID == "" {
		return structerr.NewInternal("user ID not set")
	}
	if payload.UserDetails.Protocol == "" {
		return structerr.NewInternal("protocol not set")
	}

	params := gitlab.AllowedParams{
		RepoPath:                      repoPath,
		RelativePath:                  repo.RelativePath,
		GitObjectDirectory:            repo.GitObjectDirectory,
		GitAlternateObjectDirectories: repo.GitAlternateObjectDirectories,
		GLRepository:                  repo.GetGlRepository(),
		GLID:                          payload.UserDetails.UserID,
		GLProtocol:                    payload.UserDetails.Protocol,
		Changes:                       string(changes),
		PushOptions:                   pushOptions,
	}

	allowed, message, err := m.gitlabClient.Allowed(ctx, params)
	if err != nil {
		// This logic is broken because we just return every potential error to the
		// caller, even though we cannot tell whether the error message stems from
		// the API or if it is a generic error. Ideally, we'd be able to tell
		// whether the error was a PermissionDenied error and only then return
		// the error message as GitLab message. But this will require upstream
		// changes in gitlab-shell first.
		return NotAllowedError{
			Message:  err.Error(),
			UserID:   payload.UserDetails.UserID,
			Protocol: payload.UserDetails.Protocol,
			Changes:  changes,
		}
	}
	// Due to above comment, it means that this code won't ever be executed: when there
	// was an access error, then we would see an HTTP code which doesn't indicate
	// success and thus get an error from `Allowed()`.
	if !allowed {
		return NotAllowedError{
			Message:  message,
			UserID:   payload.UserDetails.UserID,
			Protocol: payload.UserDetails.Protocol,
			Changes:  changes,
		}
	}

	executor, err := m.newCustomHooksExecutor(repo, "pre-receive")
	if err != nil {
		return fmt.Errorf("creating custom hooks executor: %w", err)
	}

	customEnv, err := m.customHooksEnv(ctx, payload, pushOptions, envs)
	if err != nil {
		return structerr.NewInternal("constructing custom hook environment: %w", err)
	}

	if err = executor(
		ctx,
		nil,
		customEnv,
		bytes.NewReader(changes),
		stdout,
		stderr,
	); err != nil {
		return fmt.Errorf("executing custom hooks: %w", err)
	}

	// reference counter
	ok, err := m.gitlabClient.PreReceive(ctx, repo.GetGlRepository())
	if err != nil {
		return structerr.NewInternal("calling pre_receive endpoint: %w", err)
	}

	if !ok {
		return errors.New("")
	}

	return nil
}
