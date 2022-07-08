package localrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
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
	// ExcludeRefs is a list of ref glob patterns to exclude from the size
	// calculation.
	ExcludeRefs []string
	// ExcludeAlternates will exclude objects in the alternates directory
	// from being counted towards the total size of the repository.
	ExcludeAlternates bool
}

// RepoSizeOption is an option which can be passed to Size
type RepoSizeOption func(*repoSizeConfig)

// WithExcludeRefs is an option for Size that excludes certain refs from the size
// calculation. The format must be a glob pattern.
// see https://git-scm.com/docs/git-rev-list#Documentation/git-rev-list.txt---excludeltglob-patterngt
func WithExcludeRefs(excludeRefs ...string) RepoSizeOption {
	return func(cfg *repoSizeConfig) {
		cfg.ExcludeRefs = excludeRefs
	}
}

// WithoutAlternates will exclude any objects in the alternate objects directory
func WithoutAlternates() RepoSizeOption {
	return func(cfg *repoSizeConfig) {
		cfg.ExcludeAlternates = true
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
	for _, refToExclude := range cfg.ExcludeRefs {
		options = append(
			options,
			git.Flag{Name: fmt.Sprintf("--exclude=%s", refToExclude)},
		)
	}

	if cfg.ExcludeAlternates {
		alternatesPath, err := repo.InfoAlternatesPath()
		if err != nil {
			return 0, fmt.Errorf("getting alternates path: %w", err)
		}

		// when excluding alternatives we need to be careful with using the bitmap index. If
		// the repository is indeed linked to an alternative object directory, then we know
		// that only the linked-to object directory will have bitmaps. Consequentially, this
		// bitmap will only ever cover objects that are part of the alternate repository and
		// can thus by definition not contain any objects that are only part of the repo
		// that is linking to it. Unfortunately, this case causes us to run into an edge
		// case in Git where the command takes significantly longer to compute the disk size
		// when using bitmaps compared to when not using bitmaps.
		//
		// To work around this case we thus don't use a bitmap index in case we find that
		// the repository has an alternates file.
		if _, err := os.Stat(alternatesPath); err != nil && errors.Is(err, fs.ErrNotExist) {
			// The alternates file does not exist. We can thus use the bitmap index and
			// don't have to specify `--not --alternate-refs` given that there aren't
			// any anyway.
			options = append(options, git.Flag{Name: "--use-bitmap-index"})
		} else {
			// We either have a bitmap index or we have run into any error that is not
			// `fs.ErrNotExist`. In that case we don't use a bitmap index, but will
			// exclude objects reachable from alternate refs.
			options = append(options,
				git.Flag{Name: "--not"},
				git.Flag{Name: "--alternate-refs"},
				git.Flag{Name: "--not"},
			)
		}
	} else {
		// If we don't exclude objects reachable from alternate refs we can always enable
		// use of the bitmap index.
		options = append(options, git.Flag{Name: "--use-bitmap-index"})
	}

	options = append(options,
		git.Flag{Name: "--all"},
		git.Flag{Name: "--objects"},
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

// StorageTempDir returns the temporary dir for the storage where the repo is on.
// When this directory does not exist yet, it's being created.
func (repo *Repo) StorageTempDir() (string, error) {
	tempPath, err := repo.locator.TempDir(repo.GetStorageName())
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(tempPath, 0o755); err != nil {
		return "", err
	}

	return tempPath, nil
}
