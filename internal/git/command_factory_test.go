package git_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/trace2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/trace2hooks"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tracing"
	"golang.org/x/time/rate"
)

func TestGitCommandProxy(t *testing.T) {
	cfg := testcfg.Build(t)

	requestReceived := false

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
	}))
	defer ts.Close()

	t.Setenv("http_proxy", ts.URL)

	ctx := testhelper.Context(t)

	dir := testhelper.TempDir(t)

	gitCmdFactory, cleanup, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(t))
	require.NoError(t, err)
	defer cleanup()

	cmd, err := gitCmdFactory.NewWithoutRepo(ctx, git.Command{
		Name: "clone",
		Args: []string{"http://gitlab.com/bogus-repo", dir},
	}, git.WithDisabledHooks())
	require.NoError(t, err)

	err = cmd.Wait()
	require.NoError(t, err)
	require.True(t, requestReceived)
}

// Global git configuration is only disabled in tests for now. Gitaly should stop using the global
// git configuration in 15.0. See https://gitlab.com/gitlab-org/gitaly/-/issues/3617.
func TestExecCommandFactory_globalGitConfigIgnored(t *testing.T) {
	cfg := testcfg.Build(t)

	gitCmdFactory, cleanup, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(t))
	require.NoError(t, err)
	defer cleanup()

	tmpHome := testhelper.TempDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpHome, ".gitconfig"), []byte(`[ignored]
	value = true
`,
	), os.ModePerm))
	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc   string
		filter string
	}{
		{desc: "global", filter: "--global"},
		// The test doesn't override the system config as that would be a global change or would
		// require chrooting. The assertion won't catch problems on systems that do not have system
		// level configuration set.
		{desc: "system", filter: "--system"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			var stdout strings.Builder
			cmd, err := gitCmdFactory.NewWithoutRepo(ctx, git.Command{
				Name:  "config",
				Flags: []git.Option{git.Flag{Name: "--list"}, git.Flag{Name: tc.filter}},
			}, git.WithEnv("HOME="+tmpHome), git.WithStdout(&stdout))
			require.NoError(t, err)
			require.NoError(t, cmd.Wait())
			require.Empty(t, stdout.String())
		})
	}
}

func TestExecCommandFactory_gitConfiguration(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	require.NoError(t, os.Remove(filepath.Join(repoPath, "config")))

	defaultConfig := func() []string {
		commandFactory, cleanup, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(t))
		require.NoError(t, err)
		defer cleanup()

		globalConfig, err := commandFactory.GlobalConfiguration(ctx)
		require.NoError(t, err)

		var configEntries []string
		for _, config := range globalConfig {
			configEntries = append(configEntries, fmt.Sprintf(
				"%s=%s", strings.ToLower(config.Key), config.Value,
			))
		}
		return configEntries
	}

	for _, tc := range []struct {
		desc           string
		config         []config.GitConfig
		options        []git.CmdOpt
		expectedConfig []string
	}{
		{
			desc:           "without config",
			expectedConfig: defaultConfig(),
		},
		{
			desc: "config with simple entry",
			config: []config.GitConfig{
				{Key: "core.foo", Value: "bar"},
			},
			expectedConfig: append(defaultConfig(), "core.foo=bar"),
		},
		{
			desc: "config with empty value",
			config: []config.GitConfig{
				{Key: "core.empty", Value: ""},
			},
			expectedConfig: append(defaultConfig(), "core.empty="),
		},
		{
			desc: "config with subsection",
			config: []config.GitConfig{
				{Key: "http.http://example.com.proxy", Value: "http://proxy.example.com"},
			},
			expectedConfig: append(defaultConfig(), "http.http://example.com.proxy=http://proxy.example.com"),
		},
		{
			desc: "config with multiple keys",
			config: []config.GitConfig{
				{Key: "core.foo", Value: "initial"},
				{Key: "core.foo", Value: "second"},
			},
			expectedConfig: append(defaultConfig(), "core.foo=initial", "core.foo=second"),
		},
		{
			desc: "option",
			options: []git.CmdOpt{
				git.WithConfig(git.ConfigPair{Key: "core.foo", Value: "bar"}),
			},
			expectedConfig: append(defaultConfig(), "core.foo=bar"),
		},
		{
			desc: "multiple options",
			options: []git.CmdOpt{
				git.WithConfig(
					git.ConfigPair{Key: "core.foo", Value: "initial"},
					git.ConfigPair{Key: "core.foo", Value: "second"},
				),
			},
			expectedConfig: append(defaultConfig(), "core.foo=initial", "core.foo=second"),
		},
		{
			desc: "config comes after options",
			config: []config.GitConfig{
				{Key: "from.config", Value: "value"},
			},
			options: []git.CmdOpt{
				git.WithConfig(
					git.ConfigPair{Key: "from.option", Value: "value"},
				),
			},
			expectedConfig: append(defaultConfig(), "from.option=value", "from.config=value"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			cfg.Git.Config = tc.config

			commandFactory, cleanup, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(t))
			require.NoError(t, err)
			defer cleanup()

			var stdout bytes.Buffer
			cmd, err := commandFactory.New(ctx, repo, git.Command{
				Name: "config",
				Flags: []git.Option{
					git.Flag{Name: "--list"},
				},
			}, append(tc.options, git.WithStdout(&stdout))...)
			require.NoError(t, err)
			require.NoError(t, cmd.Wait())
			require.Equal(t, tc.expectedConfig, strings.Split(text.ChompBytes(stdout.Bytes()), "\n"))
		})
	}
}

func TestCommandFactory_ExecutionEnvironment(t *testing.T) {
	testhelper.Unsetenv(t, "GITALY_TESTING_GIT_BINARY")
	testhelper.Unsetenv(t, "GITALY_TESTING_BUNDLED_GIT_PATH")

	ctx := testhelper.Context(t)

	assertExecEnv := func(t *testing.T, cfg config.Cfg, expectedExecEnv git.ExecutionEnvironment) {
		t.Helper()
		gitCmdFactory, cleanup, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(t), git.WithSkipHooks())
		require.NoError(t, err)
		defer cleanup()

		// We need to compare the execution environments manually because they also have
		// some private variables which we cannot easily check here.
		actualExecEnv := gitCmdFactory.GetExecutionEnvironment(ctx)
		require.Equal(t, expectedExecEnv.BinaryPath, actualExecEnv.BinaryPath)
		require.Equal(t, expectedExecEnv.EnvironmentVariables, actualExecEnv.EnvironmentVariables)
	}

	t.Run("set in config", func(t *testing.T) {
		assertExecEnv(t, config.Cfg{
			Git: config.Git{
				BinPath: "/path/to/myGit",
			},
		}, git.ExecutionEnvironment{
			BinaryPath: "/path/to/myGit",
			EnvironmentVariables: []string{
				"LANG=en_US.UTF-8",
				"GIT_TERMINAL_PROMPT=0",
				"GIT_CONFIG_GLOBAL=/dev/null",
				"GIT_CONFIG_SYSTEM=/dev/null",
				"XDG_CONFIG_HOME=/dev/null",
			},
		})
	})

	t.Run("set using GITALY_TESTING_GIT_BINARY", func(t *testing.T) {
		t.Setenv("GITALY_TESTING_GIT_BINARY", "/path/to/env_git")

		assertExecEnv(t, config.Cfg{}, git.ExecutionEnvironment{
			BinaryPath: "/path/to/env_git",
			EnvironmentVariables: []string{
				"NO_SET_GIT_TEMPLATE_DIR=YesPlease",
				"LANG=en_US.UTF-8",
				"GIT_TERMINAL_PROMPT=0",
				"GIT_CONFIG_GLOBAL=/dev/null",
				"GIT_CONFIG_SYSTEM=/dev/null",
				"XDG_CONFIG_HOME=/dev/null",
			},
		})
	})

	t.Run("set using GITALY_TESTING_BUNDLED_GIT_PATH", func(t *testing.T) {
		writeExecutables := func(t *testing.T, dir string, executables ...string) {
			for _, executable := range executables {
				testhelper.WriteExecutable(t, filepath.Join(dir, executable), nil)
			}
		}

		missingBinaryError := func(dir, binary string) error {
			return fmt.Errorf("setting up Git execution environment: %w",
				fmt.Errorf("constructing Git environment: %w",
					fmt.Errorf("statting %q: %w", binary,
						&fs.PathError{
							Op:   "stat",
							Path: filepath.Join(dir, binary),
							Err:  syscall.ENOENT,
						},
					),
				),
			)
		}

		type setupData struct {
			binaryDir     string
			bundledGitDir string
			expectedErr   error
		}

		for _, tc := range []struct {
			desc  string
			setup func(t *testing.T) setupData
		}{
			{
				desc: "unset binary directory",
				setup: func(t *testing.T) setupData {
					return setupData{
						binaryDir:     "",
						bundledGitDir: "/does/not/exist",
						expectedErr: fmt.Errorf("setting up Git execution environment: %w",
							fmt.Errorf("constructing Git environment: %w",
								errors.New("cannot use bundled binaries without bin path being set"),
							),
						),
					}
				},
			},
			{
				desc: "missing directory",
				setup: func(t *testing.T) setupData {
					return setupData{
						binaryDir:     testhelper.TempDir(t),
						bundledGitDir: "/does/not/exist",
						expectedErr:   missingBinaryError("/does/not/exist", "gitaly-git-v2.38"),
					}
				},
			},
			{
				desc: "missing gitaly-git",
				setup: func(t *testing.T) setupData {
					bundledGitDir := testhelper.TempDir(t)

					return setupData{
						binaryDir:     testhelper.TempDir(t),
						bundledGitDir: bundledGitDir,
						expectedErr:   missingBinaryError(bundledGitDir, "gitaly-git-v2.38"),
					}
				},
			},
			{
				desc: "missing gitaly-git-remote-http",
				setup: func(t *testing.T) setupData {
					bundledGitDir := testhelper.TempDir(t)

					writeExecutables(t, bundledGitDir, "gitaly-git-v2.38")

					return setupData{
						binaryDir:     testhelper.TempDir(t),
						bundledGitDir: bundledGitDir,
						expectedErr:   missingBinaryError(bundledGitDir, "gitaly-git-remote-http-v2.38"),
					}
				},
			},
			{
				desc: "missing gitaly-git-http-backend",
				setup: func(t *testing.T) setupData {
					bundledGitDir := testhelper.TempDir(t)

					writeExecutables(t, bundledGitDir,
						"gitaly-git-v2.38",
						"gitaly-git-remote-http-v2.38",
					)

					return setupData{
						binaryDir:     testhelper.TempDir(t),
						bundledGitDir: bundledGitDir,
						expectedErr:   missingBinaryError(bundledGitDir, "gitaly-git-http-backend-v2.38"),
					}
				},
			},
			{
				desc: "successful",
				setup: func(t *testing.T) setupData {
					bundledGitDir := testhelper.TempDir(t)
					writeExecutables(t, bundledGitDir, "gitaly-git-v2.38", "gitaly-git-remote-http-v2.38", "gitaly-git-http-backend-v2.38")

					return setupData{
						binaryDir:     testhelper.TempDir(t),
						bundledGitDir: bundledGitDir,
					}
				},
			},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				setupData := tc.setup(t)
				t.Setenv("GITALY_TESTING_BUNDLED_GIT_PATH", setupData.bundledGitDir)

				gitCmdFactory, cleanup, err := git.NewExecCommandFactory(
					config.Cfg{
						BinDir: setupData.binaryDir,
					},
					testhelper.SharedLogger(t),
					git.WithSkipHooks(),
					// Override the constructors so that we don't have to change
					// tests every time we're upgrading our bundled Git version.
					git.WithExecutionEnvironmentConstructors(
						git.BundledGitEnvironmentConstructor{
							Suffix: "-v2.38",
						},
					),
				)
				require.Equal(t, setupData.expectedErr, err)
				if err != nil {
					return
				}
				defer cleanup()

				// When the command factory has been successfully created then we
				// verify that the symlink that it has created match what we expect.
				gitBinPath := gitCmdFactory.GetExecutionEnvironment(ctx).BinaryPath
				for _, executable := range []string{"git", "git-remote-http", "git-http-backend"} {
					symlinkPath := filepath.Join(filepath.Dir(gitBinPath), executable)

					// The symlink in Git's temporary BinPath points to the Git
					// executable in Gitaly's BinDir.
					target, err := os.Readlink(symlinkPath)
					require.NoError(t, err)
					require.Equal(t, filepath.Join(setupData.binaryDir, "gitaly-"+executable+"-v2.38"), target)

					// And in a test setup, the symlink in Gitaly's BinDir must point to
					// the Git binary pointed to by the GITALY_TESTING_BUNDLED_GIT_PATH
					// environment variable.
					target, err = os.Readlink(target)
					require.NoError(t, err)
					require.Equal(t, filepath.Join(setupData.bundledGitDir, "gitaly-"+executable+"-v2.38"), target)
				}
			})
		}
	})

	t.Run("not set, get from system", func(t *testing.T) {
		resolvedPath, err := exec.LookPath("git")
		require.NoError(t, err)

		assertExecEnv(t, config.Cfg{}, git.ExecutionEnvironment{
			BinaryPath: resolvedPath,
			EnvironmentVariables: []string{
				"LANG=en_US.UTF-8",
				"GIT_TERMINAL_PROMPT=0",
				"GIT_CONFIG_GLOBAL=/dev/null",
				"GIT_CONFIG_SYSTEM=/dev/null",
				"XDG_CONFIG_HOME=/dev/null",
			},
		})
	})

	t.Run("doesn't exist in the system", func(t *testing.T) {
		testhelper.Unsetenv(t, "PATH")

		_, _, err := git.NewExecCommandFactory(config.Cfg{}, testhelper.SharedLogger(t), git.WithSkipHooks())
		require.EqualError(t, err, "setting up Git execution environment: could not set up any Git execution environments")
	})
}

func TestExecCommandFactoryHooksPath(t *testing.T) {
	ctx := testhelper.Context(t)

	t.Run("temporary hooks", func(t *testing.T) {
		cfg := config.Cfg{
			BinDir: testhelper.TempDir(t),
		}

		t.Run("no overrides", func(t *testing.T) {
			gitCmdFactory := gittest.NewCommandFactory(t, cfg)

			hooksPath := gitCmdFactory.HooksPath(ctx)

			// We cannot assert that the hooks path is equal to any specific
			// string, but instead we can assert that it exists and contains the
			// symlinks we expect.
			for _, hook := range []string{"update", "pre-receive", "post-receive", "reference-transaction"} {
				target, err := os.Readlink(filepath.Join(hooksPath, hook))
				require.NoError(t, err)
				require.Equal(t, cfg.BinaryPath("gitaly-hooks"), target)
			}
		})

		t.Run("with skip", func(t *testing.T) {
			gitCmdFactory := gittest.NewCommandFactory(t, cfg, git.WithSkipHooks())
			require.Equal(t, "/var/empty", gitCmdFactory.HooksPath(ctx))
		})
	})

	t.Run("hooks path", func(t *testing.T) {
		gitCmdFactory := gittest.NewCommandFactory(t, config.Cfg{
			BinDir: testhelper.TempDir(t),
		}, git.WithHooksPath("/hooks/path"))

		// The environment variable shouldn't override an explicitly set hooks path.
		require.Equal(t, "/hooks/path", gitCmdFactory.HooksPath(ctx))
	})
}

func TestExecCommandFactory_GitVersion(t *testing.T) {
	ctx := testhelper.Context(t)

	generateVersionScript := func(version string) func(git.ExecutionEnvironment) string {
		return func(git.ExecutionEnvironment) string {
			//nolint:gitaly-linters
			return fmt.Sprintf(
				`#!/usr/bin/env bash
				echo '%s'
			`, version)
		}
	}

	for _, tc := range []struct {
		desc            string
		versionString   string
		expectedErr     string
		expectedVersion string
	}{
		{
			desc:            "valid version",
			versionString:   "git version 2.33.1.gl1",
			expectedVersion: "2.33.1.gl1",
		},
		{
			desc:            "valid version with trailing newline",
			versionString:   "git version 2.33.1.gl1\n",
			expectedVersion: "2.33.1.gl1",
		},
		{
			desc:          "multi-line version",
			versionString: "git version 2.33.1.gl1\nfoobar\n",
			expectedErr:   "cannot parse git version: strconv.ParseUint: parsing \"1\\nfoobar\": invalid syntax",
		},
		{
			desc:          "unexpected format",
			versionString: "2.33.1\n",
			expectedErr:   "invalid version format: \"2.33.1\\n\\n\"",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			gitCmdFactory := gittest.NewInterceptingCommandFactory(
				t, ctx, testcfg.Build(t), generateVersionScript(tc.versionString),
				gittest.WithRealCommandFactoryOptions(git.WithSkipHooks()),
				gittest.WithInterceptedVersion(),
			)

			actualVersion, err := gitCmdFactory.GitVersion(ctx)
			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expectedErr)
			}
			require.Equal(t, tc.expectedVersion, actualVersion.String())
		})
	}

	t.Run("caching", func(t *testing.T) {
		gitCmdFactory := gittest.NewInterceptingCommandFactory(
			t, ctx, testcfg.Build(t), generateVersionScript("git version 1.2.3"),
			gittest.WithRealCommandFactoryOptions(git.WithSkipHooks()),
			gittest.WithInterceptedVersion(),
		)

		gitPath := gitCmdFactory.GetExecutionEnvironment(ctx).BinaryPath
		stat, err := os.Stat(gitPath)
		require.NoError(t, err)

		version, err := gitCmdFactory.GitVersion(ctx)
		require.NoError(t, err)
		require.Equal(t, "1.2.3", version.String())

		// We rewrite the file with the same content length and modification time such that
		// its file information doesn't change. As a result, information returned by
		// stat(3P) shouldn't differ and we should continue to see the cached version. This
		// is a known insufficiency, but it is extremely unlikely to ever happen in
		// production when the real Git binary changes.
		require.NoError(t, os.Remove(gitPath))
		testhelper.WriteExecutable(t, gitPath, []byte(generateVersionScript("git version 9.8.7")(git.ExecutionEnvironment{})))
		require.NoError(t, os.Chtimes(gitPath, stat.ModTime(), stat.ModTime()))

		// Given that we continue to use the cached version we shouldn't see any
		// change here.
		version, err = gitCmdFactory.GitVersion(ctx)
		require.NoError(t, err)
		require.Equal(t, "1.2.3", version.String())

		// If we really replace the Git binary with something else, then we should
		// see a changed version.
		require.NoError(t, os.Remove(gitPath))
		testhelper.WriteExecutable(t, gitPath, []byte(
			`#!/usr/bin/env bash
			echo 'git version 2.34.1'
		`))

		version, err = gitCmdFactory.GitVersion(ctx)
		require.NoError(t, err)
		require.Equal(t, "2.34.1", version.String())
	})
}

func TestExecCommandFactory_config(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	// Create a repository and remove its gitconfig to bring us into a known state where there
	// is no repo-level configuration that interferes with our test.
	repo, repoDir := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	require.NoError(t, os.Remove(filepath.Join(repoDir, "config")))

	expectedEnv := []string{
		"gc.auto=0",
		"maintenance.auto=0",
		"core.autocrlf=false",
		"core.usereplacerefs=false",
		"core.fsync=objects,derived-metadata,reference",
		"core.fsyncmethod=fsync",
		"core.packedrefstimeout=10000",
		"core.filesreflocktimeout=1000",
		"core.bigfilethreshold=50m",
	}

	gitCmdFactory := gittest.NewCommandFactory(t, cfg)

	var stdout bytes.Buffer
	cmd, err := gitCmdFactory.New(ctx, repo, git.Command{
		Name: "config",
		Flags: []git.Option{
			git.Flag{Name: "--list"},
		},
	}, git.WithStdout(&stdout))
	require.NoError(t, err)

	require.NoError(t, cmd.Wait())
	require.Equal(t, expectedEnv, strings.Split(strings.TrimSpace(stdout.String()), "\n"))
}

// TestFsckConfiguration tests the hardcoded configuration of the
// git fsck subcommand generated through the command factory.
func TestFsckConfiguration(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc string
		data string
	}{
		{
			desc: "with valid commit",
			data: strings.Join([]string{
				"tree " + gittest.DefaultObjectHash.EmptyTreeOID.String(),
				"author " + gittest.DefaultCommitterSignature,
				"committer " + gittest.DefaultCommitterSignature,
				"",
				"message",
			}, "\n"),
		},
		{
			desc: "with missing space",
			data: strings.Join([]string{
				"tree " + gittest.DefaultObjectHash.EmptyTreeOID.String(),
				"author Scrooge McDuck <scrooge@mcduck.com>1659043074 -0500",
				"committer Scrooge McDuck <scrooge@mcduck.com>1659975573 -0500",
				"",
				"message",
			}, "\n"),
		},
		{
			desc: "with bad timezone",
			data: strings.Join([]string{
				"tree " + gittest.DefaultObjectHash.EmptyTreeOID.String(),
				"author Scrooge McDuck <scrooge@mcduck.com> 1659043074 -0500BAD",
				"committer Scrooge McDuck <scrooge@mcduck.com> 1659975573 -0500BAD",
				"",
				"message",
			}, "\n"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)
			cfg := testcfg.Build(t)
			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg,
				gittest.CreateRepositoryConfig{SkipCreationViaService: true},
			)

			// Create commit object.
			commitOut := gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: bytes.NewBufferString(tc.data)},
				"-C", repoPath, "hash-object", "-w", "-t", "commit", "--stdin", "--literally",
			)
			_, err := gittest.DefaultObjectHash.FromHex(text.ChompBytes(commitOut))
			require.NoError(t, err)

			gitCmdFactory, cleanup, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(t))
			require.NoError(t, err)
			defer cleanup()

			// Create fsck command with configured ignore rules options.
			cmd, err := gitCmdFactory.New(ctx, repoProto,
				git.Command{Name: "fsck"},
			)
			require.NoError(t, err)

			// Execute git fsck command.
			err = cmd.Wait()
			require.NoError(t, err)
		})
	}
}

type dummyHook struct {
	name    string
	handler func(context.Context, *trace2.Trace) error
}

func (h *dummyHook) Name() string {
	return h.name
}

func (h *dummyHook) Handle(ctx context.Context, trace *trace2.Trace) error {
	return h.handler(ctx, trace)
}

func TestWithTrace2Hooks(t *testing.T) {
	t.Parallel()

	extractEventNames := func(trace *trace2.Trace) []string {
		var names []string
		trace.Walk(testhelper.Context(t), func(ctx context.Context, trace *trace2.Trace) context.Context {
			names = append(names, trace.Name)
			return nil
		})
		return names
	}

	// Trace2 outputs differently between platforms. It may include irrelevant events. In some
	// rare cases, it can also re-order the events, leading to flaky tests. So, we assert the
	// presences of essential events of the tested Git command.
	essentialEvents := []string{
		"pack-objects:enumerate-objects",
		"pack-objects:prepare-pack",
		"pack-objects:write-pack-file",
		"data:pack-objects:write_pack_file/wrote",
	}

	for _, tc := range []struct {
		desc           string
		setup          func(t *testing.T) []trace2.Hook
		expectedFields map[string]any
		withFields     bool
	}{
		{
			desc: "trace2 hook runs successfully",
			setup: func(t *testing.T) []trace2.Hook {
				return []trace2.Hook{
					&dummyHook{
						name: "dummy",
						handler: func(ctx context.Context, trace *trace2.Trace) error {
							require.Subset(t, extractEventNames(trace), essentialEvents)
							return nil
						},
					},
				}
			},
			withFields: true,
			expectedFields: map[string]any{
				"trace2.activated": "true",
				"trace2.hooks":     "dummy",
			},
		},
		{
			desc: "multiple trace2 hooks run successfully",
			setup: func(t *testing.T) []trace2.Hook {
				return []trace2.Hook{
					&dummyHook{
						name: "dummy",
						handler: func(ctx context.Context, trace *trace2.Trace) error {
							require.Subset(t, extractEventNames(trace), essentialEvents)
							return nil
						},
					},
					&dummyHook{
						name: "dummy2",
						handler: func(ctx context.Context, trace *trace2.Trace) error {
							require.Subset(t, extractEventNames(trace), essentialEvents)
							return nil
						},
					},
				}
			},
			withFields: true,
			expectedFields: map[string]any{
				"trace2.activated": "true",
				"trace2.hooks":     "dummy,dummy2",
			},
		},
		{
			desc: "no hooks provided",
			setup: func(t *testing.T) []trace2.Hook {
				return []trace2.Hook{}
			},
			withFields: true,
			expectedFields: map[string]any{
				"trace2.activated": nil,
				"trace2.hooks":     nil,
			},
		},
		{
			desc: "trace2 hook returns error",
			setup: func(t *testing.T) []trace2.Hook {
				return []trace2.Hook{
					&dummyHook{
						name: "dummy",
						handler: func(ctx context.Context, trace *trace2.Trace) error {
							return fmt.Errorf("something goes wrong")
						},
					},
					&dummyHook{
						name: "dummy2",
						handler: func(ctx context.Context, trace *trace2.Trace) error {
							require.Fail(t, "should not trigger hook after prior one fails")
							return nil
						},
					},
				}
			},
			withFields: true,
			expectedFields: map[string]any{
				"trace2.activated": "true",
				"trace2.hooks":     "dummy,dummy2",
				"trace2.error":     `trace2: executing "dummy" handler: something goes wrong`,
			},
		},
		{
			desc: "context does not initialize custom fields",
			setup: func(t *testing.T) []trace2.Hook {
				return []trace2.Hook{
					&dummyHook{
						name: "dummy",
						handler: func(ctx context.Context, trace *trace2.Trace) error {
							require.Subset(t, extractEventNames(trace), essentialEvents)
							return nil
						},
					},
				}
			},
			withFields: false,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			hooks := tc.setup(t)
			ctx := testhelper.Context(t)
			if tc.withFields {
				ctx = log.InitContextCustomFields(ctx)
			}

			performPackObjectGit(t, ctx, git.WithTrace2Hooks(hooks))

			if tc.withFields {
				customFields := log.CustomFieldsFromContext(ctx)
				require.NotNil(t, customFields)

				logrusFields := customFields.Fields()
				for key, value := range tc.expectedFields {
					require.Equal(t, value, logrusFields[key])
				}
			} else {
				require.Nil(t, log.CustomFieldsFromContext(ctx))
			}
		})
	}
}

func TestTrace2TracingExporter(t *testing.T) {
	for _, tc := range []struct {
		desc          string
		tracerOptions []testhelper.StubTracingReporterOption
		setup         func(*testing.T, context.Context) context.Context
		assert        func(*testing.T, []string, map[string]any)
	}{
		{
			desc: "there is no active span",
			setup: func(t *testing.T, ctx context.Context) context.Context {
				return ctx
			},
			assert: func(t *testing.T, spans []string, statFields map[string]any) {
				require.NotContains(t, statFields, "trace2.activated")
				require.NotContains(t, statFields, "trace2.hooks")
				for _, span := range spans {
					require.NotEqual(t, "trace2.parse", span)
				}
			},
		},
		{
			desc: "active span is sampled",
			setup: func(t *testing.T, ctx context.Context) context.Context {
				_, ctx = tracing.StartSpan(ctx, "root", nil)
				return ctx
			},
			assert: func(t *testing.T, spans []string, statFields map[string]any) {
				require.Equal(t, statFields["trace2.activated"], "true")
				require.Equal(t, statFields["trace2.hooks"], "tracing_exporter")
				require.Subset(t, spans, []string{
					"git-rev-list",
					"git",
					"git:version",
					"git:start",
					"trace2.parse",
				})
			},
		},
		{
			desc:          "active span is not sampled",
			tracerOptions: []testhelper.StubTracingReporterOption{testhelper.NeverSampled()},
			setup: func(t *testing.T, ctx context.Context) context.Context {
				_, ctx = tracing.StartSpan(ctx, "root", nil)
				return ctx
			},
			assert: func(t *testing.T, spans []string, statFields map[string]any) {
				require.NotContains(t, statFields, "trace2.activated")
				require.NotContains(t, statFields, "trace2.hooks")
				for _, span := range spans {
					require.NotEqual(t, "trace2.parse", span)
				}
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			reporter, cleanup := testhelper.StubTracingReporter(t, tc.tracerOptions...)
			defer cleanup()

			ctx := tc.setup(t, log.InitContextCustomFields(testhelper.Context(t)))
			performRevList(t, ctx)

			customFields := log.CustomFieldsFromContext(ctx)
			require.NotNil(t, customFields)
			statFields := customFields.Fields()

			var spans []string
			for _, span := range testhelper.ReportedSpans(t, reporter) {
				spans = append(spans, span.Operation)
			}

			tc.assert(t, spans, statFields)
		})
	}
}

func TestTrace2PackObjectsMetrics(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc              string
		performGitCommand func(t *testing.T, ctx context.Context, opts ...git.ExecCommandFactoryOption)
		assert            func(*testing.T, context.Context, log.Fields)
	}{
		{
			desc:              "git-pack-objects",
			performGitCommand: performPackObjectGit,
			assert: func(t *testing.T, ctx context.Context, statFields log.Fields) {
				require.Equal(t, "true", statFields["trace2.activated"])
				require.Equal(t, "pack_objects_metrics", statFields["trace2.hooks"])
				require.Contains(t, statFields, "pack_objects.enumerate_objects_ms")
				require.Contains(t, statFields, "pack_objects.prepare_pack_ms")
				require.Contains(t, statFields, "pack_objects.write_pack_file_ms")
				require.Equal(t, 1, statFields["pack_objects.written_object_count"])
			},
		},
		{
			desc: "other git command",
			performGitCommand: func(t *testing.T, ctx context.Context, opts ...git.ExecCommandFactoryOption) {
				cfg := testcfg.Build(t)
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg,
					gittest.CreateRepositoryConfig{SkipCreationViaService: true},
				)
				gitCmdFactory, cleanup, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(t), opts...)
				require.NoError(t, err)
				defer cleanup()

				cmd, err := gitCmdFactory.New(ctx, repoProto, git.Command{
					Name: "rev-list",
					Flags: []git.Option{
						git.Flag{Name: "--all"},
						git.Flag{Name: "--max-count=1"},
					},
				})
				require.NoError(t, err)

				err = cmd.Wait()
				require.NoError(t, err)
			},
			assert: func(t *testing.T, ctx context.Context, statFields log.Fields) {
				require.NotContains(t, statFields, "trace2.activated")
				require.NotContains(t, statFields, "trace2.hooks")
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := log.InitContextCustomFields(testhelper.Context(t))
			tc.performGitCommand(t, ctx)

			customFields := log.CustomFieldsFromContext(ctx)
			require.NotNil(t, customFields)

			logrusFields := customFields.Fields()
			tc.assert(t, ctx, logrusFields)
		})
	}
}

// This test modifies global tracing tracer. Thus, it cannot run in parallel.
func TestDefaultTrace2HooksFor(t *testing.T) {
	for _, tc := range []struct {
		desc          string
		subCmd        string
		setup         func(t *testing.T) (context.Context, []trace2.Hook)
		tracerOptions []testhelper.StubTracingReporterOption
	}{
		{
			desc:   "there is no active span",
			subCmd: "status",
			setup: func(t *testing.T) (context.Context, []trace2.Hook) {
				ctx := testhelper.Context(t)
				return ctx, []trace2.Hook{}
			},
		},
		{
			desc:   "active span is sampled",
			subCmd: "status",
			setup: func(t *testing.T) (context.Context, []trace2.Hook) {
				_, ctx := tracing.StartSpan(testhelper.Context(t), "root", nil)
				return ctx, []trace2.Hook{
					trace2hooks.NewTracingExporter(),
				}
			},
		},
		{
			desc:   "active span is not sampled",
			subCmd: "status",
			setup: func(t *testing.T) (context.Context, []trace2.Hook) {
				_, ctx := tracing.StartSpan(testhelper.Context(t), "root", nil)
				return ctx, []trace2.Hook{}
			},
			tracerOptions: []testhelper.StubTracingReporterOption{testhelper.NeverSampled()},
		},
		{
			desc:   "subcmd is pack-objects but span is not sampled",
			subCmd: "pack-objects",
			setup: func(t *testing.T) (context.Context, []trace2.Hook) {
				return testhelper.Context(t), []trace2.Hook{
					trace2hooks.NewPackObjectsMetrics(),
				}
			},
		},
		{
			desc:   "subcmd is pack-objects and active span is sampled",
			subCmd: "pack-objects",
			setup: func(t *testing.T) (context.Context, []trace2.Hook) {
				_, ctx := tracing.StartSpan(testhelper.Context(t), "root", nil)
				hooks := []trace2.Hook{
					trace2hooks.NewTracingExporter(),
					trace2hooks.NewPackObjectsMetrics(),
				}

				return ctx, hooks
			},
		},
		{
			desc:   "subcmd is pack-objects, active span is sampled and feature flag LogGitTraces enabled",
			subCmd: "pack-objects",
			setup: func(t *testing.T) (context.Context, []trace2.Hook) {
				ctx := testhelper.Context(t)
				ctx = featureflag.ContextWithFeatureFlag(ctx, featureflag.LogGitTraces, true)
				_, ctx = tracing.StartSpan(ctx, "root", nil)

				hooks := []trace2.Hook{
					trace2hooks.NewTracingExporter(),
					trace2hooks.NewPackObjectsMetrics(),
					trace2hooks.NewLogExporter(rate.NewLimiter(1, 1), testhelper.SharedLogger(t)),
				}

				return ctx, hooks
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, cleanup := testhelper.StubTracingReporter(t, tc.tracerOptions...)
			defer cleanup()

			ctx, expectedHooks := tc.setup(t)
			hooks := git.DefaultTrace2HooksFor(ctx, tc.subCmd, testhelper.SharedLogger(t), rate.NewLimiter(1, 1))

			require.Equal(t, hookNames(expectedHooks), hookNames(hooks))
		})
	}
}

func performPackObjectGit(t *testing.T, ctx context.Context, opts ...git.ExecCommandFactoryOption) {
	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg,
		gittest.CreateRepositoryConfig{SkipCreationViaService: true},
	)

	var input bytes.Buffer
	for i := 0; i <= 10; i++ {
		input.WriteString(gittest.WriteCommit(t, cfg, repoPath).String())
		input.WriteString("\n")
	}

	gitCmdFactory, cleanup, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(t), opts...)
	require.NoError(t, err)
	defer cleanup()

	cmd, err := gitCmdFactory.New(ctx, repoProto, git.Command{
		Name: "pack-objects",
		Flags: []git.Option{
			git.Flag{Name: "--compression=0"},
			git.Flag{Name: "--stdout"},
			git.Flag{Name: "-q"},
		},
	}, git.WithStdin(&input))
	require.NoError(t, err)

	err = cmd.Wait()
	require.NoError(t, err)
}

func performRevList(t *testing.T, ctx context.Context, opts ...git.ExecCommandFactoryOption) {
	cfg := testcfg.Build(t)
	repoProto, _ := gittest.CreateRepository(t, ctx, cfg,
		gittest.CreateRepositoryConfig{SkipCreationViaService: true},
	)
	gitCmdFactory, cleanup, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(t), opts...)
	require.NoError(t, err)
	defer cleanup()

	cmd, err := gitCmdFactory.New(ctx, repoProto, git.Command{
		Name: "rev-list",
		Flags: []git.Option{
			git.Flag{Name: "--max-count=10"},
			git.Flag{Name: "--all"},
		},
	})
	require.NoError(t, err)

	err = cmd.Wait()
	require.NoError(t, err)
}

func hookNames(hooks []trace2.Hook) []string {
	var names []string
	for _, hook := range hooks {
		names = append(names, hook.Name())
	}
	return names
}
