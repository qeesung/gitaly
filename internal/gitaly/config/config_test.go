package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/errors/cfgerror"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/cgroups"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/sentry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/duration"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
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

func TestLoadTransactions(t *testing.T) {
	cfg, err := Load(strings.NewReader(`
[transactions]
enabled = true
	`))
	require.NoError(t, err)

	require.True(t, cfg.Transactions.Enabled)
}

func TestLoadEmptyConfig(t *testing.T) {
	cfg, err := Load(strings.NewReader(``))
	require.NoError(t, err)

	expectedCfg := Cfg{
		Logging:             defaultLoggingConfig(),
		Prometheus:          prometheus.DefaultConfig(),
		PackObjectsCache:    defaultPackObjectsCacheConfig(),
		PackObjectsLimiting: defaultPackObjectsLimiting(),
	}
	require.NoError(t, expectedCfg.SetDefaults())

	// The runtime directory is a temporary path, so we need to take the value from the loaded
	// config. Furthermore, because `SetDefaults()` would append the PID, we can't do so before
	// calling that function.
	expectedCfg.RuntimeDir = cfg.RuntimeDir

	require.Equal(t, expectedCfg, cfg)
}

func TestTimeout(t *testing.T) {
	t.Parallel()

	tcs := []struct {
		Name            string
		InputTOML       string
		ExpectedConfig  Cfg
		ExpectedLoadErr error
	}{
		{
			Name:      "when no custom timeouts are provided",
			InputTOML: "",
			ExpectedConfig: Cfg{Timeout: TimeoutConfig{
				UploadPackNegotiation:    duration.Duration(10 * time.Minute),
				UploadArchiveNegotiation: duration.Duration(time.Minute),
			}},
		},
		{
			Name: "when custom timeouts are provided",
			InputTOML: `
[timeout]
upload_pack_negotiation = "20m"
upload_archive_negotiation = "5m"`,
			ExpectedConfig: Cfg{Timeout: TimeoutConfig{
				UploadPackNegotiation:    duration.Duration(20 * time.Minute),
				UploadArchiveNegotiation: duration.Duration(5 * time.Minute),
			}},
		},
		{
			Name: "when timeouts are set to 0, the defaults are applied",
			InputTOML: `
[timeout]
upload_pack_negotiation = 0
upload_archive_negotiation = "0s"`,
			ExpectedConfig: Cfg{Timeout: TimeoutConfig{
				UploadPackNegotiation:    duration.Duration(10 * time.Minute),
				UploadArchiveNegotiation: duration.Duration(time.Minute),
			}},
		},
		{
			Name: "when invalid timeouts are provided, Load returns an error",
			InputTOML: `
[timeout]
upload_pack_negotiation = abc
upload_archive_negotiation = -10`,
			ExpectedLoadErr: errors.New("load toml: toml: incomplete number"),
		},
	}

	for _, tc := range tcs {
		t.Run(tc.Name, func(t *testing.T) {
			cfg, err := Load(strings.NewReader(tc.InputTOML))
			if tc.ExpectedLoadErr == nil {
				require.NoError(t, err)
				require.NoError(t, tc.ExpectedConfig.SetDefaults())
				require.Equal(t, tc.ExpectedConfig.Timeout, cfg.Timeout)
			} else {
				require.EqualError(t, err, tc.ExpectedLoadErr.Error())
			}
		})
	}
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
	require.NoError(t, expectedCfg.SetDefaults())
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
	require.NoError(t, expectedCfg.SetDefaults())
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
sentry_dsn = "abc123"`)

	cfg, err := Load(tmpFile)
	require.NoError(t, err)

	require.Equal(t, Logging{
		Config: log.Config{
			Level: "info",
		},
		Sentry: Sentry(sentry.Config{
			Environment: "production",
			DSN:         "abc123",
		}),
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
		ScrapeTimeout:      duration.Duration(time.Second),
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

func TestLoadConfigCommand(t *testing.T) {
	t.Parallel()

	modifyDefaultConfig := func(modify func(cfg *Cfg)) Cfg {
		cfg := &Cfg{
			Logging:             defaultLoggingConfig(),
			Prometheus:          prometheus.DefaultConfig(),
			PackObjectsCache:    defaultPackObjectsCacheConfig(),
			PackObjectsLimiting: defaultPackObjectsLimiting(),
		}
		require.NoError(t, cfg.SetDefaults())
		modify(cfg)
		return *cfg
	}

	writeScript := func(t *testing.T, script string) string {
		return testhelper.WriteExecutable(t,
			filepath.Join(testhelper.TempDir(t), "script"),
			[]byte("#!/bin/sh\n"+script),
		)
	}

	type setupData struct {
		cfg         Cfg
		expectedErr string
		expectedCfg Cfg
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "nonexistent executable",
			setup: func(t *testing.T) setupData {
				return setupData{
					cfg: Cfg{
						ConfigCommand: "/does/not/exist",
					},
					expectedErr: "running config command: fork/exec /does/not/exist: no such file or directory",
				}
			},
		},
		{
			desc: "command points to non-executable file",
			setup: func(t *testing.T) setupData {
				cmd := filepath.Join(testhelper.TempDir(t), "script")
				require.NoError(t, os.WriteFile(cmd, nil, perm.PrivateFile))

				return setupData{
					cfg: Cfg{
						ConfigCommand: cmd,
					},
					expectedErr: fmt.Sprintf(
						"running config command: fork/exec %s: permission denied", cmd,
					),
				}
			},
		},
		{
			desc: "executable returns error",
			setup: func(t *testing.T) setupData {
				return setupData{
					cfg: Cfg{
						ConfigCommand: writeScript(t, "echo error >&2 && exit 1"),
					},
					expectedErr: "running config command: exit status 1, stderr: \"error\\n\"",
				}
			},
		},
		{
			desc: "invalid JSON",
			setup: func(t *testing.T) setupData {
				return setupData{
					cfg: Cfg{
						ConfigCommand: writeScript(t, "echo 'this is not json'"),
					},
					expectedErr: "unmarshalling generated config: invalid character 'h' in literal true (expecting 'r')",
				}
			},
		},
		{
			desc: "mixed stdout and stderr",
			setup: func(t *testing.T) setupData {
				// We want to verify that we're able to correctly parse the output
				// even if the process writes to both its stdout and stderr.
				cmd := writeScript(t, "echo error >&2 && echo '{}'")

				return setupData{
					cfg: Cfg{
						ConfigCommand: cmd,
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Cfg) {
						cfg.ConfigCommand = cmd
					}),
				}
			},
		},
		{
			desc: "empty script",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, "echo '{}'")

				return setupData{
					cfg: Cfg{
						ConfigCommand: cmd,
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Cfg) {
						cfg.ConfigCommand = cmd
					}),
				}
			},
		},
		{
			desc: "unknown value",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `echo '{"key_does_not_exist":"value"}'`)

				return setupData{
					cfg: Cfg{
						ConfigCommand: cmd,
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Cfg) {
						cfg.ConfigCommand = cmd
					}),
				}
			},
		},
		{
			desc: "generated value",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `echo '{"socket_path": "value"}'`)

				return setupData{
					cfg: Cfg{
						ConfigCommand: cmd,
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Cfg) {
						cfg.ConfigCommand = cmd
						cfg.SocketPath = filepath.Join(testhelper.WorkingDirectory(t), "value")
					}),
				}
			},
		},
		{
			desc: "overridden value",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `echo '{"socket_path": "overridden_value"}'`)

				return setupData{
					cfg: Cfg{
						ConfigCommand: cmd,
						SocketPath:    "initial_value",
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Cfg) {
						cfg.ConfigCommand = cmd
						cfg.SocketPath = filepath.Join(testhelper.WorkingDirectory(t), "overridden_value")
					}),
				}
			},
		},
		{
			desc: "mixed configuration",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `echo '{"listen_addr": "listen_addr"}'`)

				return setupData{
					cfg: Cfg{
						ConfigCommand: cmd,
						SocketPath:    "socket_path",
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Cfg) {
						cfg.ConfigCommand = cmd
						cfg.SocketPath = filepath.Join(testhelper.WorkingDirectory(t), "socket_path")
						cfg.ListenAddr = "listen_addr"
					}),
				}
			},
		},
		{
			desc: "unset storages",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `echo '{"storage": []}'`)

				return setupData{
					cfg: Cfg{
						ConfigCommand: cmd,
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Cfg) {
						cfg.ConfigCommand = cmd
						cfg.Storages = []Storage{}
					}),
				}
			},
		},
		{
			desc: "overridden storages",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `echo '{"storage": [
					{"name": "overridden storage", "path": "/path/to/storage"}
				]}'`)

				return setupData{
					cfg: Cfg{
						ConfigCommand: cmd,
						Storages: []Storage{
							{Name: "initial storage"},
						},
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Cfg) {
						cfg.ConfigCommand = cmd
						cfg.Storages = []Storage{
							{Name: "overridden storage", Path: "/path/to/storage"},
						}
						cfg.DailyMaintenance.Storages = []string{
							"overridden storage",
						}
					}),
				}
			},
		},
		{
			desc: "subsections are being merged",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `cat <<-EOF
					{
						"git": {
							"signing_key": "signing_key",
							"rotated_signing_keys": ["rotated_key_1", "rotated_key_2"],
							"committer_email": "noreply@gitlab.com",
							"committer_name": "GitLab"
						}
					}
					EOF
				`)

				return setupData{
					cfg: Cfg{
						ConfigCommand: cmd,
						Git: Git{
							BinPath: "foo/bar",
						},
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Cfg) {
						cfg.ConfigCommand = cmd
						cfg.Git.BinPath = "foo/bar"
						cfg.Git.SigningKey = "signing_key"
						cfg.Git.RotatedSigningKeys = []string{"rotated_key_1", "rotated_key_2"}
						cfg.Git.CommitterEmail = "noreply@gitlab.com"
						cfg.Git.CommitterName = "GitLab"
					}),
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			var cfgBuffer bytes.Buffer
			require.NoError(t, toml.NewEncoder(&cfgBuffer).Encode(setup.cfg))

			cfg, err := Load(&cfgBuffer)
			// We can't use `require.Equal()` for the error as it's basically impossible
			// to reproduce the exact `exec.ExitError`.
			if setup.expectedErr != "" {
				require.EqualError(t, err, setup.expectedErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, setup.expectedCfg, cfg)
		})
	}
}

func TestConfig_marshallingTOMLRoundtrips(t *testing.T) {
	// Without `omitempty` tags marshalling and de-marshalling as TOML would result in different
	// configurations.
	serialized, err := toml.Marshal(Cfg{})
	require.NoError(t, err)
	require.Empty(t, string(serialized))

	var deserialized Cfg
	require.NoError(t, toml.Unmarshal(serialized, &deserialized))
	require.Equal(t, Cfg{}, deserialized)
}

func TestValidateStorages(t *testing.T) {
	repositories := testhelper.TempDir(t)
	repositories2 := testhelper.TempDir(t)
	nestedRepositories := filepath.Join(repositories, "nested")
	require.NoError(t, os.MkdirAll(nestedRepositories, perm.PublicDir))

	filePath := filepath.Join(testhelper.TempDir(t), "temporary-file")
	require.NoError(t, os.WriteFile(filePath, []byte{}, perm.PublicFile))

	invalidDir := filepath.Join(filepath.Dir(repositories), t.Name())

	testCases := []struct {
		desc        string
		storages    []Storage
		expectedErr error
	}{
		{
			desc:        "no storages",
			expectedErr: cfgerror.NewValidationError(cfgerror.ErrNotSet),
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
			expectedErr: cfgerror.ValidationErrors{
				{
					Key:   []string{"[1]", "path"},
					Cause: fmt.Errorf(`%w: %q`, cfgerror.ErrNotUnique, repositories),
				}, {
					Key:   []string{"[2]", "path"},
					Cause: fmt.Errorf(`%w: %q`, cfgerror.ErrNotUnique, repositories),
				}, {
					Key:   []string{"[2]", "path"},
					Cause: fmt.Errorf(`%w: %q`, cfgerror.ErrNotUnique, repositories),
				},
			},
		},
		{
			desc: "nested paths 1",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "other", Path: repositories},
				{Name: "third", Path: nestedRepositories},
			},
			expectedErr: cfgerror.ValidationErrors{{
				Key:   []string{"[1]", "path"},
				Cause: fmt.Errorf(`%w: %q`, cfgerror.ErrNotUnique, repositories),
			}, {
				Key:   []string{"[2]", "path"},
				Cause: fmt.Errorf(`can't nest: %q and %q`, nestedRepositories, repositories),
			}, {
				Key:   []string{"[2]", "path"},
				Cause: fmt.Errorf(`can't nest: %q and %q`, nestedRepositories, repositories),
			}},
		},
		{
			desc: "nested paths 2",
			storages: []Storage{
				{Name: "default", Path: nestedRepositories},
				{Name: "other", Path: repositories},
				{Name: "third", Path: repositories},
			},
			expectedErr: cfgerror.ValidationErrors{{
				Key:   []string{"[1]", "path"},
				Cause: fmt.Errorf(`can't nest: %q and %q`, repositories, nestedRepositories),
			}, {
				Key:   []string{"[2]", "path"},
				Cause: fmt.Errorf(`can't nest: %q and %q`, repositories, nestedRepositories),
			}, {
				Key:   []string{"[2]", "path"},
				Cause: fmt.Errorf(`%w: %q`, cfgerror.ErrNotUnique, repositories),
			}},
		},
		{
			desc: "duplicate definition",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "default", Path: repositories},
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf(`%w: "default"`, cfgerror.ErrNotUnique),
					"[1]", "name"),
				cfgerror.NewValidationError(
					fmt.Errorf(`%w: %q`, cfgerror.ErrNotUnique, repositories),
					"[1]", "path",
				),
			},
		},
		{
			desc: "re-definition",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "default", Path: repositories2},
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf(`%w: "default"`, cfgerror.ErrNotUnique),
					"[1]", "name",
				),
			},
		},
		{
			desc: "empty name",
			storages: []Storage{
				{Name: "some", Path: repositories},
				{Name: "", Path: repositories2},
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					cfgerror.ErrNotSet,
					"[1]", "name",
				),
			},
		},
		{
			desc: "non existing directory",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "nope", Path: invalidDir},
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrDoesntExist, invalidDir),
					"[1]", "path",
				),
			},
		},
		{
			desc: "path points to the regular file",
			storages: []Storage{
				{Name: "default", Path: repositories},
				{Name: "is_file", Path: filePath},
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrNotDir, filePath),
					"[1]", "path",
				),
			},
		},
		{
			desc: "multiple errors",
			storages: []Storage{
				{Name: "", Path: repositories},
				{Name: "default", Path: "somewhat"},
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					cfgerror.ErrNotSet,
					"[0]", "name",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrDoesntExist, "somewhat"),
					"[1]", "path",
				),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := Cfg{Storages: tc.storages}
			err := cfg.validateStorages()

			if tc.expectedErr != nil {
				assert.Equalf(t, tc.expectedErr, err, "%+v", tc.storages)
				return
			}

			assert.NoErrorf(t, err, "%+v", tc.storages)
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

func TestGit_Validate(t *testing.T) {
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
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					errors.New("not set"),
					"config", "key",
				),
			},
		},
		{
			desc: "key has no section",
			configPairs: []GitConfig{
				{Key: "foo", Value: "value"},
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					errors.New(`key "foo" must contain at least one section`),
					"config", "key",
				),
			},
		},
		{
			desc: "key with leading dot",
			configPairs: []GitConfig{
				{Key: ".foo.bar", Value: "value"},
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					errors.New(`key ".foo.bar" must not start or end with a dot`),
					"config", "key",
				),
			},
		},
		{
			desc: "key with trailing dot",
			configPairs: []GitConfig{
				{Key: "foo.bar.", Value: "value"},
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					errors.New(`key "foo.bar." must not start or end with a dot`),
					"config", "key",
				),
			},
		},
		{
			desc: "key has assignment",
			configPairs: []GitConfig{
				{Key: "foo.bar=value", Value: "value"},
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					errors.New(`key "foo.bar=value" cannot contain "="`),
					"config", "key",
				),
			},
		},
		{
			desc: "missing value",
			configPairs: []GitConfig{
				{Key: "foo.bar", Value: ""},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := Cfg{BinDir: testhelper.TempDir(t), Git: Git{Config: tc.configPairs}}
			require.Equal(t, tc.expectedErr, cfg.Git.Validate())
		})
	}
}

func TestCfg_ValidateGitlabSecret(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) (Cfg, error)
	}{
		{
			desc: "empty configuration",
			setup: func(t *testing.T) (Cfg, error) {
				return Cfg{}, fmt.Errorf("GitLab secret not configured")
			},
		},
		{
			desc: "with GitLab secret",
			setup: func(t *testing.T) (Cfg, error) {
				return Cfg{
					Gitlab: Gitlab{
						Secret: "secret token",
					},
				}, nil
			},
		},
		{
			desc: "with non-existent GitLab secret file",
			setup: func(t *testing.T) (Cfg, error) {
				// We do not create an error in this case even though we eventually should.
				return Cfg{
					Gitlab: Gitlab{
						SecretFile: "/does/not/exist",
					},
				}, nil
			},
		},
		{
			desc: "with existing GitLab secret file",
			setup: func(t *testing.T) (Cfg, error) {
				path := filepath.Join(testhelper.TempDir(t), "secret")
				require.NoError(t, os.WriteFile(path, []byte("something"), 0o644))

				return Cfg{
					Gitlab: Gitlab{
						SecretFile: path,
					},
				}, nil
			},
		},
		{
			desc: "with non-existent gitlab-shell directory",
			setup: func(t *testing.T) (Cfg, error) {
				return Cfg{
					GitlabShell: GitlabShell{
						Dir: "/does/not/exist",
					},
				}, fmt.Errorf("%s: path doesn't exist: %q", "gitlab-shell.dir", "/does/not/exist")
			},
		},
		{
			desc: "with non-existent gitlab-shell secret file",
			setup: func(t *testing.T) (Cfg, error) {
				// We do not expect an error in case the secret file doesn't exist. This is done in
				// order to retain our legacy behaviour with a GitLab secret file whose location is
				// derived from the gitlab-shell directory.
				return Cfg{
					GitlabShell: GitlabShell{
						Dir: testhelper.TempDir(t),
					},
				}, nil
			},
		},
		{
			desc: "with existing gitlab-shell secret file",
			setup: func(t *testing.T) (Cfg, error) {
				shellDir := testhelper.TempDir(t)
				secretPath := filepath.Join(shellDir, ".gitlab_secret_file")
				require.NoError(t, os.WriteFile(secretPath, []byte("something"), 0o644))

				return Cfg{
					GitlabShell: GitlabShell{
						Dir: shellDir,
					},
				}, nil
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, expectedErr := tc.setup(t)
			require.Equal(t, expectedErr, cfg.validateGitlabSecret())
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
		expected duration.Duration
	}{
		{
			name:     "default value",
			expected: duration.Duration(1 * time.Minute),
		},
		{
			name:     "8m03s",
			config:   `graceful_restart_timeout = "8m03s"`,
			expected: duration.Duration(8*time.Minute + 3*time.Second),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpFile := strings.NewReader(test.config)

			cfg, err := Load(tmpFile)
			assert.NoError(t, err)

			assert.Equal(t, test.expected, cfg.GracefulRestartTimeout)
		})
	}
}

func TestGitlabShellDefaults(t *testing.T) {
	gitlabShellDir := "/dir"

	//nolint:gitaly-linters
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
				require.NoError(t, os.Mkdir(filepath.Join(runtimeDir, "sock.d"), perm.PublicDir))

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
				require.NoError(t, os.MkdirAll(socketDir, perm.PublicDir))

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
				Duration: duration.Duration(45 * time.Minute),
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
				Duration: duration.Duration(24*time.Hour + time.Second),
			},
			validateErr: errors.New("daily maintenance specified duration 24h0m1s must be less than 24 hours"),
		},
		{
			rawCfg: `[daily_maintenance]
			duration = "meow"`,
			expect:  DailyJob{},
			loadErr: errors.New("load toml: toml: time: invalid duration \"meow\""),
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
				Duration: duration.Duration(10 * time.Minute),
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
						Count:             10,
						MaxCgroupsPerRepo: 1,
						MemoryBytes:       1024,
						CPUShares:         512,
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
						Count:             10,
						MaxCgroupsPerRepo: 1,
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
						Count:             10,
						MaxCgroupsPerRepo: 1,
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
						Count:             10,
						MaxCgroupsPerRepo: 1,
						MemoryBytes:       1024,
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
						Count:             10,
						MaxCgroupsPerRepo: 1,
						CPUShares:         512,
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
				cpu_quota_us = 500
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					Repositories: cgroups.Repositories{
						Count:             10,
						MaxCgroupsPerRepo: 1,
						MemoryBytes:       1024,
						CPUShares:         512,
						CPUQuotaUs:        500,
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
				cpu_quota_us = 500
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					Repositories: cgroups.Repositories{
						MemoryBytes: 1024,
						CPUShares:   512,
						CPUQuotaUs:  500,
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
				cpu_shares = 512
				cpu_quota_us = 500
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					Repositories: cgroups.Repositories{
						Count:             10,
						MaxCgroupsPerRepo: 1,
						CPUShares:         512,
						CPUQuotaUs:        500,
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
				cpu_quota_us = 800
				[cgroups.repositories]
				count = 10
				memory_bytes = 2147483648
				cpu_shares = 128
				cpu_quota_us = 500
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					MemoryBytes:   1073741824,
					CPUShares:     1024,
					CPUQuotaUs:    800,
					Repositories: cgroups.Repositories{
						Count:             10,
						MaxCgroupsPerRepo: 1,
						MemoryBytes:       2147483648,
						CPUShares:         128,
						CPUQuotaUs:        500,
					},
				},
				validateErr: errors.New("cgroups.repositories: memory limit cannot exceed parent"),
			},
			{
				name: "repositories cpu shares exceeds parent",
				rawCfg: `[cgroups]
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				cpu_shares = 128
				[cgroups.repositories]
				count = 10
				cpu_shares = 512
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					CPUShares:     128,
					Repositories: cgroups.Repositories{
						Count:             10,
						MaxCgroupsPerRepo: 1,
						CPUShares:         512,
					},
				},
				validateErr: errors.New("cgroups.repositories: cpu shares cannot exceed parent"),
			},
			{
				name: "repositories cpu quota exceeds parent",
				rawCfg: `[cgroups]
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				cpu_quota_us = 225
				[cgroups.repositories]
				count = 10
				cpu_quota_us = 500
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					CPUQuotaUs:    225,
					Repositories: cgroups.Repositories{
						Count:             10,
						MaxCgroupsPerRepo: 1,
						CPUQuotaUs:        500,
					},
				},
				validateErr: errors.New("cgroups.repositories: cpu quota cannot exceed parent"),
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
						Count:             10,
						MaxCgroupsPerRepo: 1,
						MemoryBytes:       1024,
						CPUShares:         512,
					},
				},
			},
			{
				name: "max_cgroups_per_repo enabled",
				rawCfg: `[cgroups]
				mountpoint = "/sys/fs/cgroup"
				hierarchy_root = "gitaly"
				[cgroups.repositories]
				count = 10
				max_cgroups_per_repo = 5
				memory_bytes = 1024
				cpu_shares = 512
				`,
				expect: cgroups.Config{
					Mountpoint:    "/sys/fs/cgroup",
					HierarchyRoot: "gitaly",
					Repositories: cgroups.Repositories{
						Count:             10,
						MaxCgroupsPerRepo: 5,
						MemoryBytes:       1024,
						CPUShares:         512,
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
		{desc: "empty", out: StreamCacheConfig{MinOccurrences: 1}},
		{
			desc: "enabled",
			in: storageConfig + `[pack_objects_cache]
enabled = true
`,
			out: StreamCacheConfig{Enabled: true, MaxAge: duration.Duration(5 * time.Minute), Dir: "/foobar/+gitaly/PackObjectsCache", MinOccurrences: 1},
		},
		{
			desc: "enabled with custom values",
			in: storageConfig + `[pack_objects_cache]
enabled = true
dir = "/bazqux"
max_age = "10m"
min_occurrences = 0
`,
			out: StreamCacheConfig{Enabled: true, MaxAge: duration.Duration(10 * time.Minute), Dir: "/bazqux", MinOccurrences: 0},
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
		require.NoError(t, os.WriteFile(filePath, nil, perm.SharedFile))

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

func TestPackObjectsLimiting(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		desc              string
		rawCfg            string
		expectedErrString string
		expectedCfg       PackObjectsLimiting
	}{
		{
			desc: "max_queue_wait is set to 10s",
			rawCfg: `[pack_objects_limiting]
			max_concurrency = 20
 			max_queue_length = 100
			max_queue_wait = "10s"
			`,
			expectedCfg: PackObjectsLimiting{
				MaxConcurrency: 20,
				MaxQueueLength: 100,
				MaxQueueWait:   duration.Duration(10 * time.Second),
			},
		},
		{
			desc: "max_queue_wait is set to 1m",
			rawCfg: `[pack_objects_limiting]
			max_concurrency = 10
			max_queue_length = 100
			max_queue_wait = "1m"
			`,
			expectedCfg: PackObjectsLimiting{
				MaxConcurrency: 10,
				MaxQueueLength: 100,
				MaxQueueWait:   duration.Duration(1 * time.Minute),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			tmpFile := strings.NewReader(tc.rawCfg)
			cfg, err := Load(tmpFile)
			if tc.expectedErrString != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErrString)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.expectedCfg, cfg.PackObjectsLimiting)
		})
	}
}

func TestPackObjectsLimiting_defaultPackObjectsLimiting(t *testing.T) {
	t.Parallel()

	cfg := defaultPackObjectsLimiting()
	require.Equal(t, PackObjectsLimiting{
		MaxConcurrency: 200,
		MaxQueueWait:   0,
		MaxQueueLength: 0,
	}, cfg)
}

func TestPackObjectsLimiting_Validate(t *testing.T) {
	t.Parallel()

	require.NoError(t, PackObjectsLimiting{MaxConcurrency: 0}.Validate())
	require.NoError(t, PackObjectsLimiting{MaxConcurrency: 1}.Validate())
	require.NoError(t, PackObjectsLimiting{MaxConcurrency: 100}.Validate())
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1 is not greater than or equal to 0", cfgerror.ErrNotInRange),
				"max_concurrency",
			),
		},
		PackObjectsLimiting{MaxConcurrency: -1}.Validate(),
	)

	require.NoError(t, PackObjectsLimiting{Adaptive: true, InitialLimit: 0, MinLimit: 0, MaxLimit: 100}.Validate())
	require.NoError(t, PackObjectsLimiting{Adaptive: true, InitialLimit: 10, MinLimit: 0, MaxLimit: 100}.Validate())
	require.NoError(t, PackObjectsLimiting{Adaptive: true, InitialLimit: 100, MinLimit: 0, MaxLimit: 100}.Validate())
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1 is not greater than or equal to 0", cfgerror.ErrNotInRange),
				"initial_limit",
			),
		},
		PackObjectsLimiting{Adaptive: true, InitialLimit: -1, MinLimit: 0, MaxLimit: 100}.Validate(),
	)
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: 10 is not greater than or equal to 11", cfgerror.ErrNotInRange),
				"initial_limit",
			),
		},
		PackObjectsLimiting{Adaptive: true, InitialLimit: 10, MinLimit: 11, MaxLimit: 100}.Validate(),
	)
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: 3 is not greater than or equal to 10", cfgerror.ErrNotInRange),
				"max_limit",
			),
		},
		PackObjectsLimiting{Adaptive: true, InitialLimit: 10, MinLimit: 5, MaxLimit: 3}.Validate(),
	)
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1 is not greater than or equal to 0", cfgerror.ErrNotInRange),
				"min_limit",
			),
		},
		PackObjectsLimiting{Adaptive: true, InitialLimit: 5, MinLimit: -1, MaxLimit: 99}.Validate(),
	)
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1 is not greater than or equal to 10", cfgerror.ErrNotInRange),
				"max_limit",
			),
		},
		PackObjectsLimiting{Adaptive: true, InitialLimit: 10, MinLimit: 5, MaxLimit: -1}.Validate(),
	)

	require.NoError(t, PackObjectsLimiting{MaxQueueLength: 0}.Validate())
	require.NoError(t, PackObjectsLimiting{MaxQueueLength: 1}.Validate())
	require.NoError(t, PackObjectsLimiting{MaxQueueLength: 100}.Validate())
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1 is not greater than or equal to 0", cfgerror.ErrNotInRange),
				"max_queue_length",
			),
		},
		PackObjectsLimiting{MaxQueueLength: -1}.Validate(),
	)

	require.NoError(t, PackObjectsLimiting{MaxQueueWait: duration.Duration(1)}.Validate())
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1m0s is not greater than or equal to 0s", cfgerror.ErrNotInRange),
				"max_queue_wait",
			),
		},
		PackObjectsLimiting{MaxQueueWait: duration.Duration(-time.Minute)}.Validate(),
	)
}

func TestConcurrency_Validate(t *testing.T) {
	t.Parallel()

	require.NoError(t, Concurrency{MaxPerRepo: 0}.Validate())
	require.NoError(t, Concurrency{MaxPerRepo: 1}.Validate())
	require.NoError(t, Concurrency{MaxPerRepo: 100}.Validate())
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1 is not greater than or equal to 0", cfgerror.ErrNotInRange),
				"max_per_repo",
			),
		},
		Concurrency{MaxPerRepo: -1}.Validate(),
	)

	require.NoError(t, Concurrency{Adaptive: true, InitialLimit: 1, MinLimit: 1, MaxLimit: 100}.Validate())
	require.NoError(t, Concurrency{Adaptive: true, InitialLimit: 10, MinLimit: 1, MaxLimit: 100}.Validate())
	require.NoError(t, Concurrency{Adaptive: true, InitialLimit: 100, MinLimit: 1, MaxLimit: 100}.Validate())
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: 0 is not greater than 0", cfgerror.ErrNotInRange),
				"min_limit",
			),
		},
		Concurrency{Adaptive: true, InitialLimit: 0, MinLimit: 0, MaxLimit: 100}.Validate(),
	)
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1 is not greater than or equal to 1", cfgerror.ErrNotInRange),
				"initial_limit",
			),
		},
		Concurrency{Adaptive: true, InitialLimit: -1, MinLimit: 1, MaxLimit: 100}.Validate(),
	)
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: 10 is not greater than or equal to 11", cfgerror.ErrNotInRange),
				"initial_limit",
			),
		},
		Concurrency{Adaptive: true, InitialLimit: 10, MinLimit: 11, MaxLimit: 100}.Validate(),
	)
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: 3 is not greater than or equal to 10", cfgerror.ErrNotInRange),
				"max_limit",
			),
		},
		Concurrency{Adaptive: true, InitialLimit: 10, MinLimit: 5, MaxLimit: 3}.Validate(),
	)
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1 is not greater than 0", cfgerror.ErrNotInRange),
				"min_limit",
			),
		},
		Concurrency{Adaptive: true, InitialLimit: 5, MinLimit: -1, MaxLimit: 99}.Validate(),
	)
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1 is not greater than or equal to 10", cfgerror.ErrNotInRange),
				"max_limit",
			),
		},
		Concurrency{Adaptive: true, InitialLimit: 10, MinLimit: 5, MaxLimit: -1}.Validate(),
	)

	require.NoError(t, Concurrency{MaxQueueSize: 0}.Validate())
	require.NoError(t, Concurrency{MaxQueueSize: 1}.Validate())
	require.NoError(t, Concurrency{MaxQueueSize: 100}.Validate())
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1 is not greater than or equal to 0", cfgerror.ErrNotInRange),
				"max_queue_size",
			),
		},
		Concurrency{MaxQueueSize: -1}.Validate(),
	)

	require.NoError(t, Concurrency{MaxQueueWait: duration.Duration(1)}.Validate())
	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -1m0s is not greater than or equal to 0s", cfgerror.ErrNotInRange),
				"max_queue_wait",
			),
		},
		Concurrency{MaxQueueWait: duration.Duration(-time.Minute)}.Validate(),
	)
}

func TestAdaptiveLimiting_Validate(t *testing.T) {
	t.Parallel()

	require.NoError(t, AdaptiveLimiting{CPUThrottledThreshold: 0}.Validate())
	require.NoError(t, AdaptiveLimiting{CPUThrottledThreshold: 0.1}.Validate())
	require.NoError(t, AdaptiveLimiting{CPUThrottledThreshold: 0.9}.Validate())
	require.NoError(t, AdaptiveLimiting{CPUThrottledThreshold: 2.0}.Validate())

	require.NoError(t, AdaptiveLimiting{MemoryThreshold: 0}.Validate())
	require.NoError(t, AdaptiveLimiting{MemoryThreshold: 0.1}.Validate())
	require.NoError(t, AdaptiveLimiting{MemoryThreshold: 0.9}.Validate())
	require.NoError(t, AdaptiveLimiting{MemoryThreshold: 2.0}.Validate())

	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -0.1 is not greater than or equal to 0", cfgerror.ErrNotInRange),
				"cpu_throttled_threshold",
			),
		},
		AdaptiveLimiting{CPUThrottledThreshold: -0.1}.Validate(),
	)

	require.Equal(
		t,
		cfgerror.ValidationErrors{
			cfgerror.NewValidationError(
				fmt.Errorf("%w: -0.1 is not greater than or equal to 0", cfgerror.ErrNotInRange),
				"memory_threshold",
			),
		},
		AdaptiveLimiting{MemoryThreshold: -0.1}.Validate(),
	)
}

func TestStorage_Validate(t *testing.T) {
	t.Parallel()

	dirPath := testhelper.TempDir(t)
	filePath := filepath.Join(dirPath, "file")
	require.NoError(t, os.WriteFile(filePath, nil, perm.SharedFile))
	for _, tc := range []struct {
		name        string
		storage     Storage
		expectedErr error
	}{
		{
			name:    "valid",
			storage: Storage{Name: "name", Path: dirPath},
		},
		{
			name:    "invalid",
			storage: Storage{Name: "", Path: filePath},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					cfgerror.ErrNotSet,
					"name",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrNotDir, filePath),
					"path",
				),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.storage.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestTLS_Validate(t *testing.T) {
	t.Parallel()
	const certPath = "../server/testdata/gitalycert.pem"
	const keyPath = "../server/testdata/gitalykey.pem"

	key, err := os.ReadFile(keyPath)
	require.NoError(t, err)

	tmpDir := testhelper.TempDir(t)
	tmpFile := filepath.Join(tmpDir, "file")
	require.NoError(t, os.WriteFile(tmpFile, []byte("I am not a certificate"), perm.SharedFile))

	for _, tc := range []struct {
		name        string
		setup       func(t *testing.T) TLS
		expectedErr error
	}{
		{
			name: "empty",
			setup: func(t *testing.T) TLS {
				return TLS{}
			},
		},
		{
			name: "valid",
			setup: func(t *testing.T) TLS {
				return TLS{CertPath: certPath, KeyPath: keyPath}
			},
		},
		{
			name: "valid, pass in key directly",
			setup: func(t *testing.T) TLS {
				return TLS{CertPath: certPath, Key: string(key)}
			},
		},
		{
			name: "no cert path, bad key path",
			setup: func(t *testing.T) TLS {
				return TLS{CertPath: "", KeyPath: "somewhere"}
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrDoesntExist, ""),
					"certificate_path",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrDoesntExist, "somewhere"),
					"key_path",
				),
			},
		},
		{
			name: "bad cert",
			setup: func(t *testing.T) TLS {
				return TLS{CertPath: tmpFile, KeyPath: keyPath}
			},
			expectedErr: cfgerror.NewValidationError(
				errors.New("loading x509 keypair: tls: failed to find any PEM data in certificate input"),
				"certificate_path",
			),
		},
		{
			name: "bad keypath",
			setup: func(t *testing.T) TLS {
				return TLS{CertPath: certPath, KeyPath: "config_test.go"}
			},
			expectedErr: cfgerror.NewValidationError(
				errors.New("loading x509 keypair: tls: failed to find any PEM data in key input"),
				"key_path",
			),
		},
		{
			name: "bad key",
			setup: func(t *testing.T) TLS {
				return TLS{CertPath: certPath, Key: "this is not a valid key"}
			},
			expectedErr: cfgerror.NewValidationError(
				errors.New("loading x509 keypair: tls: failed to find any PEM data in key input"),
				"key",
			),
		},
		{
			name: "key and key_path both set",
			setup: func(t *testing.T) TLS {
				return TLS{CertPath: certPath, Key: "key data", KeyPath: "path/to/key"}
			},
			expectedErr: cfgerror.NewValidationError(
				errors.New("key_path and key cannot both be set"),
				"key_path",
				"key",
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tlsCfg := tc.setup(t)
			err := tlsCfg.Validate()
			if err == nil {
				require.NoError(t, err)
				return
			}

			require.Equal(t, tc.expectedErr.Error(), err.Error())
		})
	}
}

func TestGitlabShell_Validate(t *testing.T) {
	t.Parallel()

	tmpDir := testhelper.TempDir(t)
	tmpFile := filepath.Join(tmpDir, "file")
	require.NoError(t, os.WriteFile(tmpFile, nil, perm.SharedFile))

	for _, tc := range []struct {
		name        string
		gitlabShell GitlabShell
		expectedErr error
	}{
		{
			name:        "valid",
			gitlabShell: GitlabShell{Dir: tmpDir},
		},
		{
			name:        "dir does not exist",
			gitlabShell: GitlabShell{Dir: "/does/not/exist"},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrDoesntExist, "/does/not/exist"),
					"dir",
				),
			},
		},
		{
			name:        "dir is not a directory",
			gitlabShell: GitlabShell{Dir: tmpFile},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrNotDir, tmpFile),
					"dir",
				),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.gitlabShell.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestDailyJob_Validate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		dailyJob    DailyJob
		storage     []string
		expectedErr error
	}{
		{
			name:     "empty",
			dailyJob: DailyJob{},
		},
		{
			name: "all valid",
			dailyJob: DailyJob{
				Hour:     5,
				Minute:   30,
				Duration: duration.Duration(100),
				Disabled: false,
				Storages: []string{"1", "2"},
			},
			storage: []string{"1", "2"},
		},
		{
			name: "all invalid",
			dailyJob: DailyJob{
				Hour:     100,
				Minute:   90,
				Duration: duration.Duration(80 * time.Hour),
				Disabled: false,
				Storages: []string{"1", "2"},
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf("%w: 100 out of [0, 23]", cfgerror.ErrNotInRange),
					"start_hour",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: 90 out of [0, 59]", cfgerror.ErrNotInRange),
					"start_minute",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: 80h0m0s out of [0s, 24h0m0s]", cfgerror.ErrNotInRange),
					"duration",
				),
				cfgerror.NewValidationError(
					fmt.Errorf(`%w: "1"`, cfgerror.ErrDoesntExist),
					"storages", "[0]",
				),
				cfgerror.NewValidationError(
					fmt.Errorf(`%w: "2"`, cfgerror.ErrDoesntExist),
					"storages", "[1]",
				),
			},
		},
		{
			name: "invalid, but disabled",
			dailyJob: DailyJob{
				Hour:     100,
				Disabled: true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.dailyJob.Validate(tc.storage)
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestHTTPSettings_Validate(t *testing.T) {
	t.Parallel()

	tmpDir := testhelper.TempDir(t)
	tmpFile := filepath.Join(tmpDir, "tmpfile")
	require.NoError(t, os.WriteFile(tmpFile, []byte{}, perm.PublicFile))

	for _, tc := range []struct {
		name         string
		httpSettings HTTPSettings
		expectedErr  error
	}{
		{
			name: "empty",
		},
		{
			name: "all valid",
			httpSettings: HTTPSettings{
				User:     "user",
				Password: "psswrd",
				CAFile:   tmpFile,
				CAPath:   tmpDir,
			},
		},
		{
			name: "all invalid",
			httpSettings: HTTPSettings{
				User:     "user",
				Password: "",
				CAFile:   tmpDir,
				CAPath:   tmpFile,
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					cfgerror.ErrBlankOrEmpty,
					"password",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrNotFile, tmpDir),
					"ca_file",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrNotDir, tmpFile),
					"ca_path",
				),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.httpSettings.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestGitlab_Validate(t *testing.T) {
	t.Parallel()

	tmpDir := testhelper.TempDir(t)
	tmpFile := filepath.Join(tmpDir, "tmpfile")
	require.NoError(t, os.WriteFile(tmpFile, []byte{}, perm.PublicFile))

	for _, tc := range []struct {
		name        string
		gitLab      Gitlab
		expectedErr error
	}{
		{
			name: "all valid",
			gitLab: Gitlab{
				URL:             "https://gitlab.com",
				RelativeURLRoot: "api/v1",
				HTTPSettings: HTTPSettings{
					User:     "user",
					Password: "psswrd",
					CAFile:   tmpFile,
					CAPath:   tmpDir,
				},
				SecretFile: tmpFile,
			},
		},
		{
			name: "all invalid",
			gitLab: Gitlab{
				URL:             string(rune(0x7f)),
				RelativeURLRoot: "",
				HTTPSettings: HTTPSettings{
					CAFile: "/doesnt/exist",
				},
				SecretFile: tmpDir,
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					&url.Error{
						Op:  "parse",
						URL: string(rune(0x7f)),
						Err: errors.New("net/url: invalid control character in URL"),
					},
					"url",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrNotFile, tmpDir),
					"secret_file",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrDoesntExist, "/doesnt/exist"),
					"http-settings", "ca_file",
				),
			},
		},
		{
			name: "secret directly configured",
			gitLab: Gitlab{
				URL:             "https://gitlab.com",
				RelativeURLRoot: "api/v1",
				HTTPSettings: HTTPSettings{
					User:     "user",
					Password: "psswrd",
					CAFile:   tmpFile,
					CAPath:   tmpDir,
				},
				Secret:     "secret_token",
				SecretFile: "",
			},
		},
		{
			name: "secret and secret file configured",
			gitLab: Gitlab{
				URL:             "https://gitlab.com",
				RelativeURLRoot: "api/v1",
				HTTPSettings: HTTPSettings{
					User:     "user",
					Password: "psswrd",
					CAFile:   tmpFile,
					CAPath:   tmpDir,
				},
				Secret:     "secret_token",
				SecretFile: tmpFile,
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					errors.New("ambiguous secret configuration"),
					"secret", "secret_file",
				),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.gitLab.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestBackupConfig_Validate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		backupConfig BackupConfig
		expectedErr  error
	}{
		{
			name: "empty",
		},
		{
			name: "valid",
			backupConfig: BackupConfig{
				GoCloudURL: "s3://my-bucket",
				Layout:     "pointer",
			},
		},
		{
			name: "go_cloud_url invalid",
			backupConfig: BackupConfig{
				GoCloudURL: "%invalid%",
				Layout:     "pointer",
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					&url.Error{
						Op:  "parse",
						URL: "%invalid%",
						Err: url.EscapeError("%in"),
					},
					"go_cloud_url",
				),
			},
		},
		{
			name: "layout missing",
			backupConfig: BackupConfig{
				GoCloudURL: "s3://my-bucket",
				Layout:     "",
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					cfgerror.ErrBlankOrEmpty,
					"layout",
				),
			},
		},
	} {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.backupConfig.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestStreamCacheConfig_Validate(t *testing.T) {
	t.Parallel()

	absPath, err := filepath.Abs(".")
	require.NoError(t, err)

	for _, tc := range []struct {
		name        string
		streamCache StreamCacheConfig
		expectedErr error
	}{
		{
			name:        "disable",
			streamCache: StreamCacheConfig{MaxAge: duration.Duration(-1)},
		},
		{
			name: "valid",
			streamCache: StreamCacheConfig{
				Enabled: true,
				Dir:     absPath,
				MaxAge:  duration.Duration(1),
			},
		},
		{
			name: "invalid",
			streamCache: StreamCacheConfig{
				Enabled: true,
				Dir:     "relative/path",
				MaxAge:  duration.Duration(-1),
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf("%w: %q", cfgerror.ErrNotAbsolutePath, "relative/path"),
					"dir",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: -1ns is not greater than or equal to 0s", cfgerror.ErrNotInRange),
					"max_age",
				),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.streamCache.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}
