//go:build !gitaly_test_sha256

package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/cgroups"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/sentry"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestBinaryPath(t *testing.T) {
	cfg := Cfg{
		BinDir:     "bindir",
		RuntimeDir: "runtime",
	}

	require.Equal(t, "runtime/gitaly-hooks", cfg.BinaryPath("gitaly-hooks"))
	require.Equal(t, "bindir/gitaly-debug", cfg.BinaryPath("gitaly-debug"))
	require.Equal(t, "bindir", cfg.BinaryPath(""))
}

func TestLoadBrokenConfig(t *testing.T) {
	tmpFile := strings.NewReader(`path = "/tmp"\nname="foo"`)
	_, err := Load(tmpFile)
	assert.Error(t, err)
}

func TestLoadEmptyConfig(t *testing.T) {
	cfg, err := Load(strings.NewReader(``))
	require.NoError(t, err)

	expectedCfg := Cfg{
		Prometheus: prometheus.DefaultConfig(),
	}
	require.NoError(t, expectedCfg.setDefaults())

	// The runtime directory is a temporary path, so we need to take the value from the loaded
	// config. Furthermore, because `setDefaults()` would append the PID, we can't do so before
	// calling that function.
	expectedCfg.RuntimeDir = cfg.RuntimeDir

	require.Equal(t, expectedCfg, cfg)
}

func TestLoadURLs(t *testing.T) {
	tmpFile := strings.NewReader(`
[gitlab]
url = "unix:///tmp/test.socket"
relative_url_root = "/gitlab"`)

	cfg, err := Load(tmpFile)
	require.NoError(t, err)

	expectedCfg := Cfg{
		Gitlab: Gitlab{
			URL:             "unix:///tmp/test.socket",
			RelativeURLRoot: "/gitlab",
		},
	}
	require.NoError(t, expectedCfg.setDefaults())
	require.Equal(t, expectedCfg.Gitlab, cfg.Gitlab)
}

func TestLoadStorage(t *testing.T) {
	tmpFile := strings.NewReader(`[[storage]]
name = "default"
path = "/tmp/"`)

	cfg, err := Load(tmpFile)
	require.NoError(t, err)

	expectedCfg := Cfg{
		Storages: []Storage{
			{Name: "default", Path: "/tmp"},
		},
	}
	require.NoError(t, expectedCfg.setDefaults())
	require.Equal(t, expectedCfg.Storages, cfg.Storages)
}

func TestUncleanStoragePaths(t *testing.T) {
	cfg, err := Load(strings.NewReader(`[[storage]]
name="unclean-path-1"
path="/tmp/repos1//"

[[storage]]
name="unclean-path-2"
path="/tmp/repos2/subfolder/.."
`))
	require.NoError(t, err)

	require.Equal(t, []Storage{
		{Name: "unclean-path-1", Path: "/tmp/repos1"},
		{Name: "unclean-path-2", Path: "/tmp/repos2"},
	}, cfg.Storages)
}

func TestLoadMultiStorage(t *testing.T) {
	tmpFile := strings.NewReader(`[[storage]]
name="default"
path="/tmp/repos1"

[[storage]]
name="other"
path="/tmp/repos2/"`)

	cfg, err := Load(tmpFile)
	require.NoError(t, err)

	require.Equal(t, []Storage{
		{Name: "default", Path: "/tmp/repos1"},
		{Name: "other", Path: "/tmp/repos2"},
	}, cfg.Storages)
}

func TestLoadSentry(t *testing.T) {
	tmpFile := strings.NewReader(`[logging]
sentry_environment = "production"
sentry_dsn = "abc123"
ruby_sentry_dsn = "xyz456"`)

	cfg, err := Load(tmpFile)
	require.NoError(t, err)

	require.Equal(t, Logging{
		Sentry: Sentry(sentry.Config{
			Environment: "production",
			DSN:         "abc123",
		}),
		RubySentryDSN: "xyz456",
	}, cfg.Logging)
}

func TestLoadPrometheus(t *testing.T) {
	tmpFile := strings.NewReader(`
		prometheus_listen_addr=":9236"
		[prometheus]
		scrape_timeout       = "1s"
		grpc_latency_buckets = [0.0, 1.0, 2.0]
	`)

	cfg, err := Load(tmpFile)
	require.NoError(t, err)

	require.Equal(t, ":9236", cfg.PrometheusListenAddr)
	require.Equal(t, prometheus.Config{
		ScrapeTimeout:      time.Second,
		GRPCLatencyBuckets: []float64{0, 1, 2},
	}, cfg.Prometheus)
}

func TestLoadSocketPath(t *testing.T) {
	tmpFile := strings.NewReader(`socket_path="/tmp/gitaly.sock"`)

	cfg, err := Load(tmpFile)
	require.NoError(t, err)

	require.Equal(t, "/tmp/gitaly.sock", cfg.SocketPath)
}

func TestLoadListenAddr(t *testing.T) {
	tmpFile := strings.NewReader(`listen_addr=":8080"`)

	cfg, err := Load(tmpFile)
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.ListenAddr)
}

func TestValidateStorages(t *testing.T) {
	repositories := testhelper.TempDir(t)
	repositories2 := testhelper.TempDir(t)
	nestedRepositories := filepath.Join(repositories, "nested")
	require.NoError(t, os.MkdirAll(nestedRepositories, os.ModePerm))

	filePath := filepath.Join(testhelper.TempDir(t), "temporary-file")
	require.NoError(t, os.WriteFile(filePath, []byte{}, 0o666))

	invalidDir := filepath.Join(repositories, t.Name())

	testCases := []struct {
		desc      string
		storages  []Storage
		expErrMsg string
	}{
		{
			desc:      "no storages",
			expErrMsg: "no storage configurations found. Are you using the right format? https://gitlab.com/gitlab-org/gitaly/issues/397",
		},
		{
			desc: "just 1 storage",
			storages: []Storage{
				{Name: "default", Path: repositories},
			},
		},
		{
			desc: "multiple storages",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "other", Path: repositories2},
			},
		},
		{
			desc: "multiple storages pointing to same directory",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "other", Path: repositories},
				{Name: "third", Path: repositories},
			},
		},
		{
			desc: "nested paths 1",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "other", Path: repositories},
				{Name: "third", Path: nestedRepositories},
			},
			expErrMsg: `storage paths may not nest: "third" and "default"`,
		},
		{
			desc: "nested paths 2",
			storages: []Storage{
				{Name: "default", Path: nestedRepositories},
				{Name: "other", Path: repositories},
				{Name: "third", Path: repositories},
			},
			expErrMsg: `storage paths may not nest: "other" and "default"`,
		},
		{
			desc: "duplicate definition",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "default", Path: repositories},
			},
			expErrMsg: `storage "default" is defined more than once`,
		},
		{
			desc: "re-definition",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "default", Path: repositories2},
			},
			expErrMsg: `storage "default" is defined more than once`,
		},
		{
			desc: "empty name",
			storages: []Storage{
				{Name: "some", Path: repositories},
				{Name: "", Path: repositories},
			},
			expErrMsg: `empty storage name at declaration 2`,
		},
		{
			desc: "empty path",
			storages: []Storage{
				{Name: "some", Path: repositories},
				{Name: "default", Path: ""},
			},
			expErrMsg: `empty storage path for storage "default"`,
		},
		{
			desc: "non existing directory",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "nope", Path: invalidDir},
			},
			expErrMsg: fmt.Sprintf(`storage path %q for storage "nope" doesn't exist`, invalidDir),
		},
		{
			desc: "path points to the regular file",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "is_file", Path: filePath},
			},
			expErrMsg: fmt.Sprintf(`storage path %q for storage "is_file" is not a dir`, filePath),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := Cfg{Storages: tc.storages}

			err := cfg.validateStorages()
			if tc.expErrMsg != "" {
				assert.EqualError(t, err, tc.expErrMsg, "%+v", tc.storages)
				return
			}

			assert.NoError(t, err, "%+v", tc.storages)
		})
	}
}

func TestStoragePath(t *testing.T) {
	cfg := Cfg{Storages: []Storage{
		{Name: "default", Path: "/home/git/repositories1"},
		{Name: "other", Path: "/home/git/repositories2"},
		{Name: "third", Path: "/home/git/repositories3"},
	}}

	testCases := []struct {
		in, out string
		ok      bool
	}{
		{in: "default", out: "/home/git/repositories1", ok: true},
		{in: "third", out: "/home/git/repositories3", ok: true},
		{in: "", ok: false},
		{in: "foobar", ok: false},
	}

	for _, tc := range testCases {
		out, ok := cfg.StoragePath(tc.in)
		if !assert.Equal(t, tc.ok, ok, "%+v", tc) {
			continue
		}
		assert.Equal(t, tc.out, out, "%+v", tc)
	}
}

func TestLoadGit(t *testing.T) {
	tmpFile := strings.NewReader(`[git]
bin_path = "/my/git/path"
catfile_cache_size = 50

[[git.config]]
key = "first.key"
value = "first-value"

[[git.config]]
key = "second.key"
value = "second-value"
`)

	cfg, err := Load(tmpFile)
	require.NoError(t, err)

	require.Equal(t, Git{
		BinPath:          "/my/git/path",
		CatfileCacheSize: 50,
		Config: []GitConfig{
			{Key: "first.key", Value: "first-value"},
			{Key: "second.key", Value: "second-value"},
		},
	}, cfg.Git)
}

func TestValidateGitConfig(t *testing.T) {
	testCases := []struct {
		desc        string
		configPairs []GitConfig
		expectedErr error
	}{
		{
			desc: "empty config is valid",
		},
		{
			desc: "valid config entry",
			configPairs: []GitConfig{
				{Key: "foo.bar", Value: "value"},
			},
		},
		{
			desc: "missing key",
			configPairs: []GitConfig{
				{Value: "value"},
			},
			expectedErr: fmt.Errorf("invalid configuration key \"\": %w", errors.New("key cannot be empty")),
		},
		{
			desc: "key has no section",
			configPairs: []GitConfig{
				{Key: "foo", Value: "value"},
			},
			expectedErr: fmt.Errorf("invalid configuration key \"foo\": %w", errors.New("key must contain at least one section")),
		},
		{
			desc: "key with leading dot",
			configPairs: []GitConfig{
				{Key: ".foo.bar", Value: "value"},
			},
			expectedErr: fmt.Errorf("invalid configuration key \".foo.bar\": %w", errors.New("key must not start or end with a dot")),
		},
		{
			desc: "key with trailing dot",
			configPairs: []GitConfig{
				{Key: "foo.bar.", Value: "value"},
			},
			expectedErr: fmt.Errorf("invalid configuration key \"foo.bar.\": %w", errors.New("key must not start or end with a dot")),
		},
		{
			desc: "key has assignment",
			configPairs: []GitConfig{
				{Key: "foo.bar=value", Value: "value"},
			},
			expectedErr: fmt.Errorf("invalid configuration key \"foo.bar=value\": %w",
				errors.New("key cannot contain assignment")),
		},
		{
			desc: "missing value",
			configPairs: []GitConfig{
				{Key: "foo.bar"},
			},
			expectedErr: fmt.Errorf("invalid configuration value: \"\""),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := Cfg{BinDir: testhelper.TempDir(t), Git: Git{Config: tc.configPairs}}
			require.Equal(t, tc.expectedErr, cfg.validateGit())
		})
	}
}

func TestValidateShellPath(t *testing.T) {
	tmpDir := testhelper.TempDir(t)

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "bin"), 0o755))
	tmpFile := filepath.Join(tmpDir, "my-file")
	require.NoError(t, os.WriteFile(tmpFile, []byte{}, 0o666))

	testCases := []struct {
		desc      string
		path      string
		expErrMsg string
	}{
		{
			desc:      "When no Shell Path set",
			path:      "",
			expErrMsg: "gitlab-shell.dir: is not set",
		},
		{
			desc:      "When Shell Path set to non-existing path",
			path:      "/non/existing/path",
			expErrMsg: `gitlab-shell.dir: path doesn't exist: "/non/existing/path"`,
		},
		{
			desc:      "When Shell Path set to non-dir path",
			path:      tmpFile,
			expErrMsg: fmt.Sprintf(`gitlab-shell.dir: not a directory: %q`, tmpFile),
		},
		{
			desc:      "When Shell Path set to a valid directory",
			path:      tmpDir,
			expErrMsg: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := Cfg{GitlabShell: GitlabShell{Dir: tc.path}}
			err := cfg.validateShell()
			if tc.expErrMsg != "" {
				assert.EqualError(t, err, tc.expErrMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConfigureRuby(t *testing.T) {
	tmpDir := testhelper.TempDir(t)

	tmpFile := filepath.Join(tmpDir, "file")
	require.NoError(t, os.WriteFile(tmpFile, nil, 0o644))

	testCases := []struct {
		desc      string
		dir       string
		expErrMsg string
	}{
		{
			desc: "relative path",
			dir:  ".",
		},
		{
			desc: "ok",
			dir:  tmpDir,
		},
		{
			desc:      "empty",
			dir:       "",
			expErrMsg: "gitaly-ruby.dir: is not set",
		},
		{
			desc:      "does not exist",
			dir:       "/does/not/exist",
			expErrMsg: `gitaly-ruby.dir: path doesn't exist: "/does/not/exist"`,
		},
		{
			desc:      "exists but is not a directory",
			dir:       tmpFile,
			expErrMsg: fmt.Sprintf(`gitaly-ruby.dir: not a directory: %q`, tmpFile),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := Cfg{Ruby: Ruby{Dir: tc.dir}}

			err := cfg.ConfigureRuby()
			if tc.expErrMsg != "" {
				require.EqualError(t, err, tc.expErrMsg)
				return
			}

			require.NoError(t, err)

			dir := cfg.Ruby.Dir
			require.True(t, filepath.IsAbs(dir), "expected %q to be absolute path", dir)
		})
	}
}

func TestConfigureRubyNumWorkers(t *testing.T) {
	testCases := []struct {
		in, out int
	}{
		{in: -1, out: 2},
		{in: 0, out: 2},
		{in: 1, out: 2},
		{in: 2, out: 2},
		{in: 3, out: 3},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			cfg := Cfg{Ruby: Ruby{Dir: "/", NumWorkers: tc.in}}
			require.NoError(t, cfg.ConfigureRuby())
			require.Equal(t, tc.out, cfg.Ruby.NumWorkers)
		})
	}
}

func TestValidateListeners(t *testing.T) {
	testCases := []struct {
		desc string
		Cfg
		expErrMsg string
	}{
		{desc: "empty", expErrMsg: `at least one of socket_path, listen_addr or tls_listen_addr must be set`},
		{desc: "socket only", Cfg: Cfg{SocketPath: "/foo/bar"}},
		{desc: "tcp only", Cfg: Cfg{ListenAddr: "a.b.c.d:1234"}},
		{desc: "tls only", Cfg: Cfg{TLSListenAddr: "a.b.c.d:1234"}},
		{desc: "both socket and tcp", Cfg: Cfg{SocketPath: "/foo/bar", ListenAddr: "a.b.c.d:1234"}},
		{desc: "all addresses", Cfg: Cfg{SocketPath: "/foo/bar", ListenAddr: "a.b.c.d:1234", TLSListenAddr: "a.b.c.d:1234"}},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			err := tc.Cfg.validateListeners()
			if tc.expErrMsg != "" {
				require.EqualError(t, err, tc.expErrMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestLoadGracefulRestartTimeout(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		expected time.Duration
	}{
		{
			name:     "default value",
			expected: 1 * time.Minute,
		},
		{
			name:     "8m03s",
			config:   `graceful_restart_timeout = "8m03s"`,
			expected: 8*time.Minute + 3*time.Second,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpFile := strings.NewReader(test.config)

			cfg, err := Load(tmpFile)
			assert.NoError(t, err)

			assert.Equal(t, test.expected, cfg.GracefulRestartTimeout.Duration())
		})
	}
}

func TestGitlabShellDefaults(t *testing.T) {
	gitlabShellDir := "/dir"

	tmpFile := strings.NewReader(fmt.Sprintf(`[gitlab-shell]
dir = '%s'`, gitlabShellDir))
	cfg, err := Load(tmpFile)
	require.NoError(t, err)

	require.Equal(t, Gitlab{
		SecretFile: filepath.Join(gitlabShellDir, ".gitlab_shell_secret"),
	}, cfg.Gitlab)
	require.Equal(t, Hooks{
		CustomHooksDir: filepath.Join(gitlabShellDir, "hooks"),
	}, cfg.Hooks)
}

func TestSetupRuntimeDirectory_validateInternalSocket(t *testing.T) {
	verifyPathDoesNotExist := func(t *testing.T, runtimeDir string, actualErr error) {
		require.EqualError(t, actualErr, fmt.Sprintf("creating runtime directory: mkdir %s/gitaly-%d: no such file or directory", runtimeDir, os.Getpid()))
	}

	testCases := []struct {
		desc   string
		setup  func(t *testing.T) string
		verify func(t *testing.T, runtimeDir string, actualErr error)
	}{
		{
			desc: "non existing directory",
			setup: func(t *testing.T) string {
				return "/path/does/not/exist"
			},
			verify: verifyPathDoesNotExist,
		},
		{
			desc: "symlinked runtime directory",
			setup: func(t *testing.T) string {
				runtimeDir := testhelper.TempDir(t)
				require.NoError(t, os.Mkdir(filepath.Join(runtimeDir, "sock.d"), os.ModePerm))

				// Create a symlink which points to the real runtime directory.
				symlinkDir := testhelper.TempDir(t)
				symlink := filepath.Join(symlinkDir, "symlink-to-runtime-dir")
				require.NoError(t, os.Symlink(runtimeDir, symlink))

				return symlink
			},
		},
		{
			desc: "broken symlinked runtime directory",
			setup: func(t *testing.T) string {
				symlinkDir := testhelper.TempDir(t)
				symlink := filepath.Join(symlinkDir, "symlink-to-runtime-dir")
				require.NoError(t, os.Symlink("/path/does/not/exist", symlink))
				return symlink
			},
			verify: verifyPathDoesNotExist,
		},
		{
			desc: "socket can't be created",
			setup: func(t *testing.T) string {
				tempDir := testhelper.TempDir(t)

				runtimeDirTooLongForSockets := filepath.Join(tempDir, strings.Repeat("/nested_directory", 10))
				socketDir := filepath.Join(runtimeDirTooLongForSockets, "sock.d")
				require.NoError(t, os.MkdirAll(socketDir, os.ModePerm))

				return runtimeDirTooLongForSockets
			},
			verify: func(t *testing.T, runtimeDir string, actualErr error) {
				require.EqualError(t, actualErr, "failed creating internal test socket: invalid argument: your socket path is likely too long, please change Gitaly's runtime directory")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			runtimeDir := tc.setup(t)

			cfg := Cfg{
				RuntimeDir: runtimeDir,
			}

			_, actualErr := SetupRuntimeDirectory(cfg, os.Getpid())
			if tc.verify == nil {
				require.NoError(t, actualErr)
			} else {
				tc.verify(t, cfg.RuntimeDir, actualErr)
			}
		})
	}
}

func TestLoadDailyMaintenance(t *testing.T) {
	for _, tt := range []struct {
		name        string
		rawCfg      string
		expect      DailyJob
		loadErr     error
		validateErr error
	}{
		{
			name: "success",
			rawCfg: `[[storage]]
			name = "default"
			path = "/"

			[daily_maintenance]
			start_hour = 11
			start_minute = 23
			duration = "45m"
			storages = ["default"]
			`,
			expect: DailyJob{
				Hour:     11,
				Minute:   23,
				Duration: Duration(45 * time.Minute),
				Storages: []string{"default"},
			},
		},
		{
			rawCfg: `[daily_maintenance]
			start_hour = 24`,
			expect: DailyJob{
				Hour: 24,
			},
			validateErr: errors.New("daily maintenance specified hour '24' outside range (0-23)"),
		},
		{
			rawCfg: `[daily_maintenance]
			start_hour = 60`,
			expect: DailyJob{
				Hour: 60,
			},
			validateErr: errors.New("daily maintenance specified hour '60' outside range (0-23)"),
		},
		{
			rawCfg: `[daily_maintenance]
			start_hour = 0
			start_minute = 61`,
			expect: DailyJob{
				Hour:   0,
				Minute: 61,
			},
			validateErr: errors.New("daily maintenance specified minute '61' outside range (0-59)"),
		},
		{
			rawCfg: `[daily_maintenance]
			start_hour = 0
			start_minute = 59
			duration = "86401s"`,
			expect: DailyJob{
				Hour:     0,
				Minute:   59,
				Duration: Duration(24*time.Hour + time.Second),
			},
			validateErr: errors.New("daily maintenance specified duration 24h0m1s must be less than 24 hours"),
		},
		{
			rawCfg: `[daily_maintenance]
			duration = "meow"`,
			expect:  DailyJob{},
			loadErr: errors.New("load toml: (2, 4): unmarshal text: time: invalid duration"),
		},
		{
			rawCfg: `[daily_maintenance]
			storages = ["default"]`,
			expect: DailyJob{
				Storages: []string{"default"},
			},
			validateErr: errors.New(`daily maintenance specified storage "default" does not exist in configuration`),
		},
		{
			name: "default window",
			rawCfg: `[[storage]]
			name = "default"
			path = "/"
			`,
			expect: DailyJob{
				Hour:     12,
				Minute:   0,
				Duration: Duration(10 * time.Minute),
				Storages: []string{"default"},
			},
		},
		{
			name: "override default window",
			rawCfg: `[[storage]]
			name = "default"
			path = "/"
			[daily_maintenance]
			disabled = true
			`,
			expect: DailyJob{
				Disabled: true,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := strings.NewReader(tt.rawCfg)
			cfg, err := Load(tmpFile)
			if err != nil {
				require.Contains(t, err.Error(), tt.loadErr.Error())
			}
			require.Equal(t, tt.expect, cfg.DailyMaintenance)
			require.Equal(t, tt.validateErr, cfg.validateMaintenance())
		})
	}
}

func TestValidateCgroups(t *testing.T) {
	type testCase struct {
		name        string
		rawCfg      string
		expect      cgroups.Config
		validateErr error
	}

	t.Run("old format", func(t *testing.T) {
		testCases := []testCase{
			{
				name: "enabled success",
				rawCfg: `[cgroups]
				count = 10
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				[cgroups.memory]
				enabled = true
				limit = 1024
				[cgroups.cpu]
				enabled = true
				shares = 512`,
				expect: cgroups.Config{
					Count:         10,
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					Memory: cgroups.Memory{
						Enabled: true,
						Limit:   1024,
					},
					CPU: cgroups.CPU{
						Enabled: true,
						Shares:  512,
					},
					Repositories: cgroups.Repositories{
						Count:       10,
						MemoryBytes: 1024,
						CPUShares:   512,
					},
				},
			},
			{
				name: "empty mount point",
				rawCfg: `[cgroups]
				count = 10
				mountpoint = ""
				hierarchy_root = "baz"
				`,
				expect: cgroups.Config{
					Count:         10,
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "baz",
					Repositories: cgroups.Repositories{
						Count: 10,
					},
				},
			},
			{
				name: "empty hierarchy_root",
				rawCfg: `[cgroups]
				count = 10
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = ""`,
				expect: cgroups.Config{
					Count:         10,
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					Repositories: cgroups.Repositories{
						Count: 10,
					},
				},
			},
			{
				name: "cpu shares - zero",
				rawCfg: `[cgroups]
				count = 10
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				[cgroups.memory]
				enabled = true
				limit = 1024
				[cgroups.cpu]
				enabled = true
				shares = 0`,
				expect: cgroups.Config{
					Count:         10,
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					Memory: cgroups.Memory{
						Enabled: true,
						Limit:   1024,
					},
					CPU: cgroups.CPU{
						Enabled: true,
						Shares:  0,
					},
					Repositories: cgroups.Repositories{
						Count:       10,
						MemoryBytes: 1024,
					},
				},
			},
			{
				name: "memory limit - zero",
				rawCfg: `[cgroups]
				count = 10
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				[cgroups.memory]
				enabled = true
				limit = 0
				[cgroups.cpu]
				enabled = true
				shares = 512
				`,
				expect: cgroups.Config{
					Count:         10,
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					Memory: cgroups.Memory{
						Enabled: true,
						Limit:   0,
					},
					CPU: cgroups.CPU{
						Enabled: true,
						Shares:  512,
					},
					Repositories: cgroups.Repositories{
						Count:     10,
						CPUShares: 512,
					},
				},
			},
			{
				name: "repositories - zero count",
				rawCfg: `[cgroups]
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				[cgroups.repositories]
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
				},
			},
		}
		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				tmpFile := strings.NewReader(tt.rawCfg)
				cfg, err := Load(tmpFile)
				require.NoError(t, err)
				require.Equal(t, tt.expect, cfg.Cgroups)
				require.Equal(t, tt.validateErr, cfg.validateCgroups())
			})
		}
	})

	t.Run("new format", func(t *testing.T) {
		testCases := []testCase{
			{
				name: "enabled success",
				rawCfg: `[cgroups]
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				[cgroups.repositories]
				count = 10
				memory_bytes = 1024
				cpu_shares = 512
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					Repositories: cgroups.Repositories{
						Count:       10,
						MemoryBytes: 1024,
						CPUShares:   512,
					},
				},
			},
			{
				name: "repositories count is zero",
				rawCfg: `[cgroups]
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				[cgroups.repositories]
				count = 0
				memory_bytes = 1024
				cpu_shares = 512
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					Repositories: cgroups.Repositories{
						MemoryBytes: 1024,
						CPUShares:   512,
					},
				},
			},
			{
				name: "memory is zero",
				rawCfg: `[cgroups]
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				[cgroups.repositories]
				count = 10
				cpu_shares = 512`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					Repositories: cgroups.Repositories{
						Count:     10,
						CPUShares: 512,
					},
				},
			},
			{
				name: "repositories memory exceeds parent",
				rawCfg: `[cgroups]
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				memory_bytes = 1073741824
				cpu_shares = 1024
				[cgroups.repositories]
				count = 10
				memory_bytes = 2147483648
				cpu_shares = 128
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					MemoryBytes:   1073741824,
					CPUShares:     1024,
					Repositories: cgroups.Repositories{
						Count:       10,
						MemoryBytes: 2147483648,
						CPUShares:   128,
					},
				},
				validateErr: errors.New("cgroups.repositories: memory limit cannot exceed parent"),
			},
			{
				name: "repositories cpu exceeds parent",
				rawCfg: `[cgroups]
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				memory_bytes = 1073741824
				cpu_shares = 128
				[cgroups.repositories]
				count = 10
				memory_bytes = 1024
				cpu_shares = 512
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					MemoryBytes:   1073741824,
					CPUShares:     128,
					Repositories: cgroups.Repositories{
						Count:       10,
						MemoryBytes: 1024,
						CPUShares:   512,
					},
				},
				validateErr: errors.New("cgroups.repositories: cpu shares cannot exceed parent"),
			},
			{
				name: "metrics enabled",
				rawCfg: `[cgroups]
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				metrics_enabled = true
				[cgroups.repositories]
				count = 10
				memory_bytes = 1024
				cpu_shares = 512
				`,
				expect: cgroups.Config{
					Mountpoint:     "/sys/fs/cgroup",
					HierarchyRoot:  "gitaly",
					MetricsEnabled: true,
					Repositories: cgroups.Repositories{
						Count:       10,
						MemoryBytes: 1024,
						CPUShares:   512,
					},
				},
			},
		}
		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				tmpFile := strings.NewReader(tt.rawCfg)
				cfg, err := Load(tmpFile)
				require.NoError(t, err)
				require.Equal(t, tt.expect, cfg.Cgroups)
				require.Equal(t, tt.validateErr, cfg.validateCgroups())
			})
		}
	})
}

func TestConfigurePackObjectsCache(t *testing.T) {
	storageConfig := `[[storage]]
name="default"
path="/foobar"
`

	testCases := []struct {
		desc string
		in   string
		out  StreamCacheConfig
		err  error
	}{
		{desc: "empty"},
		{
			desc: "enabled",
			in: storageConfig + `[pack_objects_cache]
enabled = true
`,
			out: StreamCacheConfig{Enabled: true, MaxAge: Duration(5 * time.Minute), Dir: "/foobar/+gitaly/PackObjectsCache"},
		},
		{
			desc: "enabled with custom values",
			in: storageConfig + `[pack_objects_cache]
enabled = true
dir = "/bazqux"
max_age = "10m"
`,
			out: StreamCacheConfig{Enabled: true, MaxAge: Duration(10 * time.Minute), Dir: "/bazqux"},
		},
		{
			desc: "enabled with 0 storages",
			in: `[pack_objects_cache]
enabled = true
`,
			err: errPackObjectsCacheNoStorages,
		},
		{
			desc: "enabled with negative max age",
			in: `[pack_objects_cache]
enabled = true
max_age = "-5m"
`,
			err: errPackObjectsCacheNegativeMaxAge,
		},
		{
			desc: "enabled with relative path",
			in: `[pack_objects_cache]
enabled = true
dir = "foobar"
`,
			err: errPackObjectsCacheRelativePath,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, err := Load(strings.NewReader(tc.in))
			require.NoError(t, err)

			err = cfg.configurePackObjectsCache()
			if tc.err != nil {
				require.Equal(t, tc.err, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.out, cfg.PackObjectsCache)
		})
	}
}

func TestValidateToken(t *testing.T) {
	require.NoError(t, (&Cfg{Auth: auth.Config{}}).validateToken())
	require.NoError(t, (&Cfg{Auth: auth.Config{Token: ""}}).validateToken())
	require.NoError(t, (&Cfg{Auth: auth.Config{Token: "secret"}}).validateToken())
	require.NoError(t, (&Cfg{Auth: auth.Config{Transitioning: true, Token: "secret"}}).validateToken())
}

func TestValidateBinDir(t *testing.T) {
	tmpDir := testhelper.TempDir(t)
	tmpFile := filepath.Join(tmpDir, "file")
	fp, err := os.Create(tmpFile)
	require.NoError(t, err)
	require.NoError(t, fp.Close())

	for _, tc := range []struct {
		desc      string
		binDir    string
		expErrMsg string
	}{
		{
			desc:   "ok",
			binDir: tmpDir,
		},
		{
			desc:      "empty",
			binDir:    "",
			expErrMsg: "bin_dir: is not set",
		},
		{
			desc:      "path doesn't exist",
			binDir:    "/not/exists",
			expErrMsg: `bin_dir: path doesn't exist: "/not/exists"`,
		},
		{
			desc:      "is not a directory",
			binDir:    tmpFile,
			expErrMsg: fmt.Sprintf(`bin_dir: not a directory: %q`, tmpFile),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			err := (&Cfg{BinDir: tc.binDir}).validateBinDir()
			if tc.expErrMsg == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expErrMsg)
			}
		})
	}
}

func TestSetupRuntimeDirectory(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Run("empty runtime directory", func(t *testing.T) {
			cfg := Cfg{}
			runtimeDir, err := SetupRuntimeDirectory(cfg, os.Getpid())
			require.NoError(t, err)

			require.DirExists(t, runtimeDir)
			require.True(t, strings.HasPrefix(runtimeDir, filepath.Join(os.TempDir(), "gitaly-")))
		})

		t.Run("non-existent runtime directory", func(t *testing.T) {
			cfg := Cfg{
				RuntimeDir: "/does/not/exist",
			}

			_, err := SetupRuntimeDirectory(cfg, os.Getpid())
			require.EqualError(t, err, fmt.Sprintf("creating runtime directory: mkdir /does/not/exist/gitaly-%d: no such file or directory", os.Getpid()))
		})

		t.Run("existent runtime directory", func(t *testing.T) {
			dir := testhelper.TempDir(t)
			cfg := Cfg{
				RuntimeDir: dir,
			}

			runtimeDir, err := SetupRuntimeDirectory(cfg, os.Getpid())
			require.NoError(t, err)

			require.Equal(t, filepath.Join(dir, fmt.Sprintf("gitaly-%d", os.Getpid())), runtimeDir)
			require.DirExists(t, runtimeDir)
		})
	})

	t.Run("validation", func(t *testing.T) {
		dirPath := testhelper.TempDir(t)
		filePath := filepath.Join(dirPath, "file")
		require.NoError(t, os.WriteFile(filePath, nil, 0o644))

		for _, tc := range []struct {
			desc        string
			runtimeDir  string
			expectedErr error
		}{
			{
				desc:       "valid runtime directory",
				runtimeDir: dirPath,
			},
			{
				desc:       "unset",
				runtimeDir: "",
			},
			{
				desc:        "path doesn't exist",
				runtimeDir:  "/does/not/exist",
				expectedErr: fmt.Errorf("runtime_dir: path doesn't exist: %q", "/does/not/exist"),
			},
			{
				desc:        "path is not a directory",
				runtimeDir:  filePath,
				expectedErr: fmt.Errorf(`runtime_dir: not a directory: %q`, filePath),
			},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				err := (&Cfg{RuntimeDir: tc.runtimeDir}).validateRuntimeDir()
				require.Equal(t, tc.expectedErr, err)
			})
		}
	})
}

func TestPruneRuntimeDirectories(t *testing.T) {
	t.Run("no runtime directories", func(t *testing.T) {
		require.NoError(t, PruneRuntimeDirectories(testhelper.NewDiscardingLogEntry(t), testhelper.TempDir(t)))
	})

	t.Run("unset runtime directory", func(t *testing.T) {
		require.EqualError(t, PruneRuntimeDirectories(testhelper.NewDiscardingLogEntry(t), ""), "list runtime directory: open : no such file or directory")
	})

	t.Run("non-existent runtime directory", func(t *testing.T) {
		require.EqualError(t, PruneRuntimeDirectories(testhelper.NewDiscardingLogEntry(t), "/path/does/not/exist"), "list runtime directory: open /path/does/not/exist: no such file or directory")
	})

	t.Run("invalid, stale and active runtime directories", func(t *testing.T) {
		baseDir := testhelper.TempDir(t)
		cfg := Cfg{RuntimeDir: baseDir}

		// Setup a runtime directory for our process, it can't be stale as long as
		// we are running.
		ownRuntimeDir, err := SetupRuntimeDirectory(cfg, os.Getpid())
		require.NoError(t, err)

		expectedLogs := map[string]string{}

		// Setup runtime directories for processes that have finished.
		var prunableDirs []string
		for i := 0; i < 2; i++ {
			cmd := exec.Command("cat")
			require.NoError(t, cmd.Run())

			staleRuntimeDir, err := SetupRuntimeDirectory(cfg, cmd.Process.Pid)
			require.NoError(t, err)

			prunableDirs = append(prunableDirs, staleRuntimeDir)
			expectedLogs[staleRuntimeDir] = "removing leftover runtime directory"
		}

		// Create an unexpected file in the runtime directory
		unexpectedFilePath := filepath.Join(baseDir, "unexpected-file")
		require.NoError(t, os.WriteFile(unexpectedFilePath, []byte(""), os.ModePerm))
		expectedLogs[unexpectedFilePath] = "runtime directory contains an unexpected file"

		nonPrunableDirs := []string{ownRuntimeDir}

		// Setup some unexpected directories in the runtime directory
		for _, dirName := range []string{
			"nohyphen",
			"too-many-hyphens",
			"invalidprefix-3",
			"gitaly-invalidpid",
		} {
			dirPath := filepath.Join(baseDir, dirName)
			require.NoError(t, os.Mkdir(dirPath, os.ModePerm))
			expectedLogs[dirPath] = "runtime directory contains an unexpected directory"
			nonPrunableDirs = append(nonPrunableDirs, dirPath)
		}

		logger, hook := test.NewNullLogger()
		require.NoError(t, PruneRuntimeDirectories(logger, cfg.RuntimeDir))

		actualLogs := map[string]string{}
		for _, entry := range hook.Entries {
			actualLogs[entry.Data["path"].(string)] = entry.Message
		}

		require.Equal(t, expectedLogs, actualLogs)

		require.FileExists(t, unexpectedFilePath)

		for _, nonPrunableEntry := range nonPrunableDirs {
			require.DirExists(t, nonPrunableEntry, nonPrunableEntry)
		}

		for _, prunableEntry := range prunableDirs {
			require.NoDirExists(t, prunableEntry, prunableEntry)
		}
	})
}
