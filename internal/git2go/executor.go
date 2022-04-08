package git2go

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v14/internal/command"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/storage"
	glog "gitlab.com/gitlab-org/gitaly/v14/internal/log"
	"gitlab.com/gitlab-org/gitaly/v14/internal/version"
	"gitlab.com/gitlab-org/labkit/correlation"
)

var (
	// ErrInvalidArgument is returned in case the merge arguments are invalid.
	ErrInvalidArgument = errors.New("invalid parameters")

	// BinaryName is a binary name with version suffix .
	BinaryName = "gitaly-git2go-" + version.GetModuleVersion()
)

// Executor executes gitaly-git2go.
type Executor struct {
	binaryPath          string
	gitCmdFactory       git.CommandFactory
	locator             storage.Locator
	logFormat, logLevel string
}

// NewExecutor returns a new gitaly-git2go executor using binaries as configured in the given
// configuration.
func NewExecutor(cfg config.Cfg, gitCmdFactory git.CommandFactory, locator storage.Locator) *Executor {
	return &Executor{
		binaryPath:    filepath.Join(cfg.BinDir, BinaryName),
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

	env := alternates.Env(repoPath, repo.GetGitObjectDirectory(), repo.GetGitAlternateObjectDirectories())

	// Pass the log output directly to gitaly-git2go. No need to reinterpret
	// these logs as long as the destination is an append-only file. See
	// https://pkg.go.dev/github.com/sirupsen/logrus#readme-thread-safety
	log := glog.Default().Logger.Out

	args = append([]string{
		"-log-format", b.logFormat,
		"-log-level", b.logLevel,
		"-correlation-id", correlation.ExtractFromContext(ctx),
		subcmd,
	}, args...)

	var stdout bytes.Buffer
	cmd, err := command.New(ctx, exec.Command(b.binaryPath, args...), stdin, &stdout, log, env...)
	if err != nil {
		return nil, err
	}

	cmd.SetMetricsSubCmd(subcmd)

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

	commitID, err := git.NewObjectIDFromHex(result.CommitID)
	if err != nil {
		return "", fmt.Errorf("could not parse commit ID: %w", err)
	}

	return commitID, nil
}
