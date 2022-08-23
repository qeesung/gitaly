package git2go

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	glog "gitlab.com/gitlab-org/gitaly/v15/internal/log"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/labkit/correlation"
)

var (
	// ErrInvalidArgument is returned in case the merge arguments are invalid.
	ErrInvalidArgument = errors.New("invalid parameters")

	// BinaryName is the name of the gitaly-git2go binary.
	BinaryName = "gitaly-git2go"
)

// Executor executes gitaly-git2go.
type Executor struct {
	binaryPath          string
	signingKey          string
	gitCmdFactory       git.CommandFactory
	locator             storage.Locator
	logFormat, logLevel string
}

// NewExecutor returns a new gitaly-git2go executor using binaries as configured in the given
// configuration.
func NewExecutor(cfg config.Cfg, gitCmdFactory git.CommandFactory, locator storage.Locator) *Executor {
	return &Executor{
		binaryPath:    cfg.BinaryPath(BinaryName),
		signingKey:    cfg.Git.SigningKey,
		gitCmdFactory: gitCmdFactory,
		locator:       locator,
		logFormat:     cfg.Logging.Format,
		logLevel:      cfg.Logging.Level,
	}
}

func (b *Executor) run(ctx context.Context, repo repository.GitRepo, stdin io.Reader, subcmd string, args ...string) (*bytes.Buffer, error) {
	repoPath, err := b.locator.GetRepoPath(repo)
	if err != nil {
		return nil, fmt.Errorf("gitaly-git2go: %w", err)
	}

	var enabledFeatureFlags, disabledFeatureFlags []string

	for flag, value := range featureflag.FromContext(ctx) {
		switch value {
		case true:
			enabledFeatureFlags = append(enabledFeatureFlags, flag.MetadataKey())
		case false:
			disabledFeatureFlags = append(disabledFeatureFlags, flag.MetadataKey())
		}
	}

	env := alternates.Env(repoPath, repo.GetGitObjectDirectory(), repo.GetGitAlternateObjectDirectories())
	env = append(env, b.gitCmdFactory.GetExecutionEnvironment(ctx).EnvironmentVariables...)

	// Pass the log output directly to gitaly-git2go. No need to reinterpret
	// these logs as long as the destination is an append-only file. See
	// https://pkg.go.dev/github.com/sirupsen/logrus#readme-thread-safety
	log := glog.Default().Logger.Out

	args = append([]string{
		"-log-format", b.logFormat,
		"-log-level", b.logLevel,
		"-correlation-id", correlation.ExtractFromContext(ctx),
		"-enabled-feature-flags", strings.Join(enabledFeatureFlags, ","),
		"-disabled-feature-flags", strings.Join(disabledFeatureFlags, ","),
		subcmd,
	}, args...)

	var stdout bytes.Buffer
	cmd, err := command.New(ctx, append([]string{b.binaryPath}, args...),
		command.WithStdin(stdin),
		command.WithStdout(&stdout),
		command.WithStderr(log),
		command.WithEnvironment(env),
		command.WithCommandName("gitaly-git2go", subcmd),
	)
	if err != nil {
		return nil, err
	}

	if err := cmd.Wait(); err != nil {
		return nil, err
	}

	return &stdout, nil
}

// runWithGob runs the specified gitaly-git2go cmd with the request gob-encoded
// as input and returns the commit ID as string or an error.
func (b *Executor) runWithGob(ctx context.Context, repo repository.GitRepo, cmd string, request interface{}) (git.ObjectID, error) {
	input := &bytes.Buffer{}
	if err := gob.NewEncoder(input).Encode(request); err != nil {
		return "", fmt.Errorf("%s: %w", cmd, err)
	}

	output, err := b.run(ctx, repo, input, cmd)
	if err != nil {
		return "", fmt.Errorf("%s: %w", cmd, err)
	}

	var result Result
	if err := gob.NewDecoder(output).Decode(&result); err != nil {
		return "", fmt.Errorf("%s: %w", cmd, err)
	}

	if result.Err != nil {
		return "", fmt.Errorf("%s: %w", cmd, result.Err)
	}

	commitID, err := git.ObjectHashSHA1.FromHex(result.CommitID)
	if err != nil {
		return "", fmt.Errorf("could not parse commit ID: %w", err)
	}

	return commitID, nil
}
