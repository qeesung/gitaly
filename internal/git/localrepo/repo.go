package localrepo

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

// Repo represents a local Git repository.
type Repo struct {
	repository.GitRepo
	locator       storage.Locator
	gitCmdFactory git.CommandFactory
	catfileCache  catfile.Cache
}

// New creates a new Repo from its protobuf representation.
func New(locator storage.Locator, gitCmdFactory git.CommandFactory, catfileCache catfile.Cache, repo repository.GitRepo) *Repo {
	return &Repo{
		GitRepo:       repo,
		locator:       locator,
		gitCmdFactory: gitCmdFactory,
		catfileCache:  catfileCache,
	}
}

// NewTestRepo constructs a Repo. It is intended as a helper function for tests which assembles
// dependencies ad-hoc from the given config.
func NewTestRepo(t testing.TB, cfg config.Cfg, repo repository.GitRepo, factoryOpts ...git.ExecCommandFactoryOption) *Repo {
	t.Helper()

	if cfg.SocketPath != testcfg.UnconfiguredSocketPath {
		repo = gittest.RewrittenRepository(testhelper.Context(t), t, cfg, &gitalypb.Repository{
			StorageName:                   repo.GetStorageName(),
			RelativePath:                  repo.GetRelativePath(),
			GitObjectDirectory:            repo.GetGitObjectDirectory(),
			GitAlternateObjectDirectories: repo.GetGitAlternateObjectDirectories(),
		})
	}

	gitCmdFactory, cleanup, err := git.NewExecCommandFactory(cfg, factoryOpts...)
	t.Cleanup(cleanup)
	require.NoError(t, err)

	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	locator := config.NewLocator(cfg)

	return New(locator, gitCmdFactory, catfileCache, repo)
}

// Exec creates a git command with the given args and Repo, executed in the
// Repo. It validates the arguments in the command before executing.
func (repo *Repo) Exec(ctx context.Context, cmd git.Cmd, opts ...git.CmdOpt) (*command.Command, error) {
	return repo.gitCmdFactory.New(ctx, repo, cmd, opts...)
}

// ExecAndWait is similar to Exec, but waits for the command to exit before
// returning.
func (repo *Repo) ExecAndWait(ctx context.Context, cmd git.Cmd, opts ...git.CmdOpt) error {
	command, err := repo.Exec(ctx, cmd, opts...)
	if err != nil {
		return err
	}

	return command.Wait()
}

// GitVersion returns the Git version in use.
func (repo *Repo) GitVersion(ctx context.Context) (git.Version, error) {
	return repo.gitCmdFactory.GitVersion(ctx)
}

func errorWithStderr(err error, stderr []byte) error {
	if len(stderr) == 0 {
		return err
	}
	return fmt.Errorf("%w, stderr: %q", err, stderr)
}

// repoSizeConfig can be used to pass in different options to
// git rev-list in determining the size of a repository.
type repoSizeConfig struct {
	// Excludes is a list of ref glob patterns to exclude from the size
	// calculation.
	Excludes []string
}

// RepoSizeOption is an option which can be passed to Size
type RepoSizeOption func(*repoSizeConfig)

// WithExcludes is an option for Size that excludes certain refs from the size
// calculation. The format must be a glob pattern.
// see https://git-scm.com/docs/git-rev-list#Documentation/git-rev-list.txt---excludeltglob-patterngt
func WithExcludes(excludes ...string) RepoSizeOption {
	return func(cfg *repoSizeConfig) {
		cfg.Excludes = excludes
	}
}

// Size calculates the size of all reachable objects in bytes
func (repo *Repo) Size(ctx context.Context, opts ...RepoSizeOption) (int64, error) {
	var stdout bytes.Buffer

	var cfg repoSizeConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	var options []git.Option
	for _, exclude := range cfg.Excludes {
		options = append(
			options,
			git.Flag{Name: fmt.Sprintf("--exclude=%s", exclude)},
		)
	}

	options = append(options,
		git.Flag{Name: "--all"},
		git.Flag{Name: "--objects"},
		git.Flag{Name: "--use-bitmap-index"},
		git.Flag{Name: "--disk-usage"},
	)

	if err := repo.ExecAndWait(ctx,
		git.SubCmd{
			Name:  "rev-list",
			Flags: options,
		},
		git.WithStdout(&stdout),
	); err != nil {
		return -1, err
	}

	size, err := strconv.ParseInt(strings.TrimSuffix(stdout.String(), "\n"), 10, 64)
	if err != nil {
		return -1, err
	}

	return size, nil
}
