//go:build !gitaly_test_sha256

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/smudge"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

const logName = "gitaly_lfs_smudge.log"

func TestGitalyLFSSmudge(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	binary := testcfg.BuildGitalyLFSSmudge(t, cfg)

	gitlabCfg, cleanup := runTestServer(t, defaultOptions)
	defer cleanup()

	tlsCfg := config.TLS{
		CertPath: certPath,
		KeyPath:  keyPath,
	}

	marshalledGitlabCfg, err := json.Marshal(gitlabCfg)
	require.NoError(t, err)

	marshalledTLSCfg, err := json.Marshal(tlsCfg)
	require.NoError(t, err)

	standardEnv := func(logDir string) []string {
		return []string{
			"GL_REPOSITORY=project-1",
			"GL_INTERNAL_CONFIG=" + string(marshalledGitlabCfg),
			"GITALY_LOG_DIR=" + logDir,
			"GITALY_TLS=" + string(marshalledTLSCfg),
		}
	}

	for _, tc := range []struct {
		desc              string
		setup             func(t *testing.T) ([]string, string)
		stdin             io.Reader
		expectedErr       string
		expectedStdout    string
		expectedStderr    string
		expectedLogRegexp string
	}{
		{
			desc: "success",
			setup: func(t *testing.T) ([]string, string) {
				logDir := testhelper.TempDir(t)
				return standardEnv(logDir), filepath.Join(logDir, logName)
			},
			stdin:             strings.NewReader(lfsPointer),
			expectedStdout:    "hello world",
			expectedLogRegexp: "Finished HTTP request",
		},
		{
			desc: "success with single envvar",
			setup: func(t *testing.T) ([]string, string) {
				logDir := testhelper.TempDir(t)

				cfg := smudge.Config{
					GlRepository: "project-1",
					Gitlab:       gitlabCfg,
					TLS:          tlsCfg,
				}

				env, err := cfg.Environment()
				require.NoError(t, err)

				return []string{
					env,
					"GITALY_LOG_DIR=" + logDir,
				}, filepath.Join(logDir, "gitaly_lfs_smudge.log")
			},
			stdin:             strings.NewReader(lfsPointer),
			expectedStdout:    "hello world",
			expectedLogRegexp: "Finished HTTP request",
		},
		{
			desc: "missing Gitlab repository",
			setup: func(t *testing.T) ([]string, string) {
				logDir := testhelper.TempDir(t)

				return []string{
					"GL_INTERNAL_CONFIG=" + string(marshalledGitlabCfg),
					"GITALY_LOG_DIR=" + logDir,
					"GITALY_TLS=" + string(marshalledTLSCfg),
				}, filepath.Join(logDir, logName)
			},
			stdin:             strings.NewReader(lfsPointer),
			expectedErr:       "exit status 1",
			expectedLogRegexp: "error loading project: GL_REPOSITORY is not defined",
		},
		{
			desc: "missing Gitlab configuration",
			setup: func(t *testing.T) ([]string, string) {
				logDir := testhelper.TempDir(t)

				return []string{
					"GL_REPOSITORY=project-1",
					"GITALY_LOG_DIR=" + logDir,
					"GITALY_TLS=" + string(marshalledTLSCfg),
				}, filepath.Join(logDir, logName)
			},
			stdin:             strings.NewReader(lfsPointer),
			expectedErr:       "exit status 1",
			expectedLogRegexp: "unable to retrieve GL_INTERNAL_CONFIG",
		},
		{
			desc: "missing TLS configuration",
			setup: func(t *testing.T) ([]string, string) {
				logDir := testhelper.TempDir(t)

				return []string{
					"GL_REPOSITORY=project-1",
					"GL_INTERNAL_CONFIG=" + string(marshalledGitlabCfg),
					"GITALY_LOG_DIR=" + logDir,
				}, filepath.Join(logDir, logName)
			},
			stdin:             strings.NewReader(lfsPointer),
			expectedErr:       "exit status 1",
			expectedLogRegexp: "unable to retrieve GITALY_TLS",
		},
		{
			desc: "missing log configuration",
			setup: func(t *testing.T) ([]string, string) {
				logDir := testhelper.TempDir(t)

				return []string{
					"GL_REPOSITORY=project-1",
					"GL_INTERNAL_CONFIG=" + string(marshalledGitlabCfg),
					"GITALY_TLS=" + string(marshalledTLSCfg),
				}, filepath.Join(logDir, "gitaly_lfs_smudge.log")
			},
			stdin:          strings.NewReader(lfsPointer),
			expectedStdout: "hello world",
		},
		{
			desc: "missing stdin",
			setup: func(t *testing.T) ([]string, string) {
				logDir := testhelper.TempDir(t)
				return standardEnv(logDir), filepath.Join(logDir, logName)
			},
			expectedErr:    "exit status 1",
			expectedStdout: "Cannot read from STDIN. This command should be run by the Git 'smudge' filter\n",
		},
		{
			desc: "non-LFS-pointer input",
			setup: func(t *testing.T) ([]string, string) {
				logDir := testhelper.TempDir(t)
				return standardEnv(logDir), filepath.Join(logDir, logName)
			},
			stdin:             strings.NewReader("somethingsomething"),
			expectedStdout:    "somethingsomething",
			expectedLogRegexp: "^$",
		},
		{
			desc: "mixed input",
			setup: func(t *testing.T) ([]string, string) {
				logDir := testhelper.TempDir(t)
				return standardEnv(logDir), filepath.Join(logDir, logName)
			},
			stdin:             strings.NewReader(lfsPointer + "\nsomethingsomething\n"),
			expectedStdout:    lfsPointer + "\nsomethingsomething\n",
			expectedLogRegexp: "^$",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			env, logFile := tc.setup(t)

			var stdout, stderr bytes.Buffer
			cmd, err := command.New(ctx, []string{binary},
				command.WithStdin(tc.stdin),
				command.WithStdout(&stdout),
				command.WithStderr(&stderr),
				command.WithEnvironment(env),
			)
			require.NoError(t, err)

			err = cmd.Wait()
			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expectedErr)
			}
			require.Equal(t, tc.expectedStdout, stdout.String())
			require.Equal(t, tc.expectedStderr, stderr.String())

			if tc.expectedLogRegexp == "" {
				require.NoFileExists(t, logFile)
			} else {
				logData := testhelper.MustReadFile(t, logFile)
				require.Regexp(t, tc.expectedLogRegexp, string(logData))
			}
		})
	}
}
