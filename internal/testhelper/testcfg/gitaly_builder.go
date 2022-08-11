package testcfg

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

// UnconfiguredSocketPath is used to bypass config validation errors
// when building the configuration. The socket path is now known yet
// at the time of building the configuration and is substituted later
// when the service is actually spun up.
const UnconfiguredSocketPath = "it is a stub to bypass Validate method"

// Option is a configuration option for the builder.
type Option func(*GitalyCfgBuilder)

// WithBase allows use cfg as a template for start building on top of.
// override parameter signals if settings of the cfg can be overridden or not
// (if setting has a default value it is considered "not configured" and can be
// set despite flag value).
func WithBase(cfg config.Cfg) Option {
	return func(builder *GitalyCfgBuilder) {
		builder.cfg = cfg
	}
}

// WithStorages allows to configure list of storages under this gitaly instance.
// All storages will have a test repository by default.
func WithStorages(name string, names ...string) Option {
	return func(builder *GitalyCfgBuilder) {
		builder.storages = append([]string{name}, names...)
	}
}

// WithRealLinguist suppress stubbing of the linguist language detection.
func WithRealLinguist() Option {
	return func(builder *GitalyCfgBuilder) {
		builder.realLinguist = true
	}
}

// WithPackObjectsCacheEnabled enables the pack object cache.
func WithPackObjectsCacheEnabled() Option {
	return func(builder *GitalyCfgBuilder) {
		builder.packObjectsCacheEnabled = true
	}
}

// NewGitalyCfgBuilder returns gitaly configuration builder with configured set of options.
func NewGitalyCfgBuilder(opts ...Option) GitalyCfgBuilder {
	cfgBuilder := GitalyCfgBuilder{}

	for _, opt := range opts {
		opt(&cfgBuilder)
	}

	return cfgBuilder
}

// GitalyCfgBuilder automates creation of the gitaly configuration and filesystem structure required.
type GitalyCfgBuilder struct {
	cfg config.Cfg

	storages                []string
	realLinguist            bool
	packObjectsCacheEnabled bool
}

// Build setups required filesystem structure, creates and returns configuration of the gitaly service.
func (gc *GitalyCfgBuilder) Build(tb testing.TB) config.Cfg {
	tb.Helper()

	cfg := gc.cfg
	if cfg.SocketPath == "" {
		cfg.SocketPath = UnconfiguredSocketPath
	}

	root := testhelper.TempDir(tb)

	if cfg.BinDir == "" {
		cfg.BinDir = filepath.Join(root, "bin.d")
		require.NoError(tb, os.Mkdir(cfg.BinDir, 0o755))
	}

	if cfg.Ruby.Dir == "" {
		_, currentFile, _, ok := runtime.Caller(0)
		require.True(tb, ok, "could not get caller info")
		cfg.Ruby.Dir = filepath.Join(filepath.Dir(currentFile), "../../../ruby")
	}

	if cfg.Logging.Dir == "" {
		cfg.Logging.Dir = filepath.Join(root, "log.d")
		require.NoError(tb, os.Mkdir(cfg.Logging.Dir, 0o755))
	}

	if cfg.GitlabShell.Dir == "" {
		cfg.GitlabShell.Dir = filepath.Join(root, "shell.d")
		require.NoError(tb, os.Mkdir(cfg.GitlabShell.Dir, 0o755))
	}

	if cfg.RuntimeDir == "" {
		cfg.RuntimeDir = filepath.Join(root, "runtime.d")
		require.NoError(tb, os.Mkdir(cfg.RuntimeDir, 0o700))
		require.NoError(tb, os.Mkdir(cfg.InternalSocketDir(), 0o755))
	}

	if len(cfg.Storages) != 0 && len(gc.storages) != 0 {
		require.FailNow(tb, "invalid configuration build setup: fix storages configured")
	}

	if len(cfg.Storages) == 0 {
		storagesDir := filepath.Join(root, "storages.d")
		require.NoError(tb, os.Mkdir(storagesDir, 0o755))

		if len(gc.storages) == 0 {
			gc.storages = []string{"default"}
		}

		// creation of the required storages (empty storage directories)
		cfg.Storages = make([]config.Storage, len(gc.storages))
		for i, storageName := range gc.storages {
			storagePath := filepath.Join(storagesDir, storageName)
			require.NoError(tb, os.MkdirAll(storagePath, 0o755))
			cfg.Storages[i].Name = storageName
			cfg.Storages[i].Path = storagePath
		}
	}

	if !gc.realLinguist {
		if cfg.Ruby.LinguistLanguagesPath == "" {
			// set a stub to prevent a long ruby process to run where it is not needed
			cfg.Ruby.LinguistLanguagesPath = filepath.Join(root, "linguist_languages.json")
			require.NoError(tb, os.WriteFile(cfg.Ruby.LinguistLanguagesPath, []byte(`{}`), 0o655))
		}
	}

	cfg.PackObjectsCache.Enabled = gc.packObjectsCacheEnabled
	// Ignore the gitconfig so that tests aren't impacted by any configuration the user happens
	// to have lying around.
	cfg.Git.IgnoreGitconfig = true

	require.NoError(tb, cfg.Validate())

	return cfg
}

// BuildWithRepoAt setups required filesystem structure, creates and returns configuration of the gitaly service,
// clones test repository into each configured storage the provided relative path.
func (gc *GitalyCfgBuilder) BuildWithRepoAt(tb testing.TB, relativePath string) (config.Cfg, []*gitalypb.Repository) {
	tb.Helper()

	ctx := testhelper.Context(tb)
	cfg := gc.Build(tb)

	// clone the test repo to the each storage
	repos := make([]*gitalypb.Repository, len(cfg.Storages))
	for i, gitalyStorage := range cfg.Storages {
		repo, _ := gittest.CreateRepository(ctx, tb, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
			Storage:                gitalyStorage,
			RelativePath:           relativePath,
			Seed:                   gittest.SeedGitLabTest,
		})

		repos[i] = repo
		repos[i].StorageName = gitalyStorage.Name
	}

	return cfg, repos
}

// Build creates a minimal configuration setup with no options and returns it with cleanup function.
func Build(tb testing.TB, opts ...Option) config.Cfg {
	cfgBuilder := NewGitalyCfgBuilder(opts...)

	return cfgBuilder.Build(tb)
}

// BuildWithRepo creates a minimal configuration setup with no options.
// It also clones test repository at the storage and returns it with the full path to the repository.
func BuildWithRepo(tb testing.TB, opts ...Option) (config.Cfg, *gitalypb.Repository, string) {
	cfgBuilder := NewGitalyCfgBuilder(opts...)

	cfg, repos := cfgBuilder.BuildWithRepoAt(tb, tb.Name())
	repoPath := filepath.Join(cfg.Storages[0].Path, repos[0].RelativePath)
	return cfg, repos[0], repoPath
}
