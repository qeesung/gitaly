package testcfg

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
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
		require.NoError(tb, os.Mkdir(cfg.BinDir, perm.PrivateDir))
	}

	if cfg.Logging.Dir == "" {
		logDir := testhelper.CreateTestLogDir(tb)
		if len(logDir) != 0 {
			cfg.Logging.Dir = logDir
		} else {
			cfg.Logging.Dir = filepath.Join(root, "log.d")
			require.NoError(tb, os.Mkdir(cfg.Logging.Dir, perm.PrivateDir))
		}
	}

	if cfg.GitlabShell.Dir == "" {
		cfg.GitlabShell.Dir = filepath.Join(root, "shell.d")
		require.NoError(tb, os.Mkdir(cfg.GitlabShell.Dir, perm.PrivateDir))
	}

	if cfg.RuntimeDir == "" {
		cfg.RuntimeDir = filepath.Join(root, "runtime.d")
		require.NoError(tb, os.Mkdir(cfg.RuntimeDir, perm.PrivateDir))
		require.NoError(tb, os.Mkdir(cfg.InternalSocketDir(), perm.PrivateDir))
	}

	if len(cfg.Storages) != 0 && len(gc.storages) != 0 {
		require.FailNow(tb, "invalid configuration build setup: fix storages configured")
	}

	cfg.PackObjectsCache.Enabled = gc.packObjectsCacheEnabled

	// The tests don't require GitLab API to be accessible, but as it is required to pass
	// validation, so the artificial values are set to pass.
	if cfg.Gitlab.URL == "" {
		cfg.Gitlab.URL = "https://test.stub.gitlab.com"
	}

	if cfg.Gitlab.SecretFile == "" {
		cfg.Gitlab.SecretFile = filepath.Join(root, "gitlab", "http.secret")
		require.NoError(tb, os.MkdirAll(filepath.Dir(cfg.Gitlab.SecretFile), perm.PrivateDir))
		require.NoError(tb, os.WriteFile(cfg.Gitlab.SecretFile, nil, perm.PublicFile))
	}

	// cfg.SetDefaults() should only be called after cfg.Gitlab.SecretFile is set (so it doesn't override it with
	// its own) and before cfg.Storages is set (so it doesn't attempt to attach a storage to cfg.DailyMaintenance).
	require.NoError(tb, cfg.SetDefaults())

	if len(cfg.Storages) == 0 {
		storagesDir := filepath.Join(root, "storages.d")
		require.NoError(tb, os.Mkdir(storagesDir, perm.PrivateDir))

		if len(gc.storages) == 0 {
			gc.storages = []string{"default"}
		}

		// creation of the required storages (empty storage directories)
		cfg.Storages = make([]config.Storage, len(gc.storages))
		for i, storageName := range gc.storages {
			storagePath := filepath.Join(storagesDir, storageName)
			require.NoError(tb, os.MkdirAll(storagePath, perm.PrivateDir))
			cfg.Storages[i].Name = storageName
			cfg.Storages[i].Path = storagePath
		}
	}

	cfg.Transactions.Enabled = testhelper.IsWALEnabled()

	require.NoError(tb, cfg.Validate())

	return cfg
}

// Build creates a minimal configuration setup.
func Build(tb testing.TB, opts ...Option) config.Cfg {
	cfgBuilder := NewGitalyCfgBuilder(opts...)

	return cfgBuilder.Build(tb)
}

// WriteTemporaryGitalyConfigFile writes the given Gitaly configuration into a temporary file and
// returns its path.
func WriteTemporaryGitalyConfigFile(tb testing.TB, cfg config.Cfg) string {
	tb.Helper()

	path := filepath.Join(testhelper.TempDir(tb), "config.toml")

	contents, err := toml.Marshal(cfg)
	require.NoError(tb, err)
	require.NoError(tb, os.WriteFile(path, contents, perm.SharedFile))

	return path
}
