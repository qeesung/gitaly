//go:build !gitaly_test_sha256

package gittest

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestInterceptingCommandFactory(t *testing.T) {
	cfg, repoProto, repoPath := setup(t)
	ctx := testhelper.Context(t)

	factory := NewInterceptingCommandFactory(ctx, t, cfg, func(execEnv git.ExecutionEnvironment) string {
		return fmt.Sprintf(
			`#!/usr/bin/env bash
			%q rev-parse --sq-quote 'Hello, world!'
		`, execEnv.BinaryPath)
	})
	expectedString := " 'Hello, world'\\!''\n"

	t.Run("New", func(t *testing.T) {
		var stdout bytes.Buffer
		cmd, err := factory.New(ctx, repoProto, git.SubCmd{
			Name: "rev-parse",
			Args: []string{"something"},
		}, git.WithStdout(&stdout))
		require.NoError(t, err)
		require.NoError(t, cmd.Wait())
		require.Equal(t, expectedString, stdout.String())
	})

	t.Run("NewWithDir", func(t *testing.T) {
		var stdout bytes.Buffer
		cmd, err := factory.NewWithDir(ctx, repoPath, git.SubCmd{
			Name: "rev-parse",
			Args: []string{"something"},
		}, git.WithStdout(&stdout))
		require.NoError(t, err)
		require.NoError(t, cmd.Wait())
		require.Equal(t, expectedString, stdout.String())
	})

	t.Run("NewWithoutRepo", func(t *testing.T) {
		var stdout bytes.Buffer
		cmd, err := factory.NewWithoutRepo(ctx, git.SubCmd{
			Name: "rev-parse",
			Args: []string{"something"},
			Flags: []git.Option{
				git.ValueFlag{Name: "-C", Value: repoPath},
			},
		}, git.WithStdout(&stdout))
		require.NoError(t, err)
		require.NoError(t, cmd.Wait())
		require.Equal(t, expectedString, stdout.String())
	})
}

func TestInterceptingCommandFactory_GitVersion(t *testing.T) {
	cfg, _, _ := setup(t)
	ctx := testhelper.Context(t)

	generateVersionScript := func(execEnv git.ExecutionEnvironment) string {
		return `#!/usr/bin/env bash
			echo "git version 1.2.3"
		`
	}

	// Obtain the real Git version so that we can compare that it matches what we expect.
	realFactory, cleanup, err := git.NewExecCommandFactory(cfg)
	require.NoError(t, err)
	defer cleanup()

	realVersion, err := realFactory.GitVersion(ctx)
	require.NoError(t, err)

	// Furthermore, we need to obtain the intercepted version here because we cannot construct
	// `git.Version` structs ourselves.
	fakeVersion, err := NewInterceptingCommandFactory(ctx, t, cfg, generateVersionScript, WithInterceptedVersion()).GitVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, "1.2.3", fakeVersion.String())

	for _, tc := range []struct {
		desc            string
		opts            []InterceptingCommandFactoryOption
		expectedVersion git.Version
	}{
		{
			desc:            "without version interception",
			expectedVersion: realVersion,
		},
		{
			desc: "with version interception",
			opts: []InterceptingCommandFactoryOption{
				WithInterceptedVersion(),
			},
			expectedVersion: fakeVersion,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			factory := NewInterceptingCommandFactory(ctx, t, cfg, generateVersionScript, tc.opts...)

			version, err := factory.GitVersion(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.expectedVersion, version)

			// The real command factory should always return the real Git version.
			version, err = factory.realCommandFactory.GitVersion(ctx)
			require.NoError(t, err)
			require.Equal(t, realVersion, version)

			// On the other hand, the intercepting command factory should return
			// different versions depending on whether the version is intercepted or
			// not. This is required such that it correctly handles version checks in
			// case it calls `GitVersion()` on itself.
			version, err = factory.interceptingCommandFactory.GitVersion(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.expectedVersion, version)
		})
	}
}
