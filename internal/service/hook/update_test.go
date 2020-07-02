package hook

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

func TestUpdateInvalidArgument(t *testing.T) {
	serverSocketPath, stop := runHooksServer(t, config.Config.Hooks)
	defer stop()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	stream, err := client.UpdateHook(ctx, &gitalypb.UpdateHookRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()

	testhelper.RequireGrpcError(t, err, codes.InvalidArgument)
}

func TestUpdate(t *testing.T) {
	rubyDir := config.Config.Ruby.Dir
	defer func() {
		config.Config.Ruby.Dir = rubyDir
	}()

	cwd, err := os.Getwd()
	require.NoError(t, err)
	config.Config.Ruby.Dir = filepath.Join(cwd, "testdata")

	serverSocketPath, stop := runHooksServer(t, config.Config.Hooks)
	defer stop()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	testCases := []struct {
		desc           string
		req            gitalypb.UpdateHookRequest
		status         int32
		stdout, stderr string
	}{
		{
			desc: "valid inputs",
			req: gitalypb.UpdateHookRequest{
				Repository:           testRepo,
				Ref:                  []byte("master"),
				OldValue:             "a",
				NewValue:             "b",
				EnvironmentVariables: []string{"GL_ID=key-123", "GL_USERNAME=username", "GL_PROTOCOL=protocol", "GL_REPOSITORY=repository"},
			},
			status: 0,
			stdout: "OK",
			stderr: "",
		},
		{
			desc: "missing ref",
			req: gitalypb.UpdateHookRequest{
				Repository:           testRepo,
				Ref:                  nil,
				OldValue:             "a",
				NewValue:             "b",
				EnvironmentVariables: []string{"GL_ID=key-123", "GL_USERNAME=username", "GL_PROTOCOL=protocol", "GL_REPOSITORY=repository"},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
		{
			desc: "missing old value",
			req: gitalypb.UpdateHookRequest{
				Repository:           testRepo,
				Ref:                  []byte("master"),
				OldValue:             "",
				NewValue:             "b",
				EnvironmentVariables: []string{"GL_ID=key-123", "GL_USERNAME=username", "GL_PROTOCOL=protocol", "GL_REPOSITORY=repository"},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
		{
			desc: "missing new value",
			req: gitalypb.UpdateHookRequest{
				Repository:           testRepo,
				Ref:                  []byte("master"),
				OldValue:             "a",
				NewValue:             "",
				EnvironmentVariables: []string{"GL_ID=key-123", "GL_USERNAME=username", "GL_PROTOCOL=protocol", "GL_REPOSITORY=repository"},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
		{
			desc: "missing gl_id value",
			req: gitalypb.UpdateHookRequest{
				Repository:           testRepo,
				Ref:                  []byte("master"),
				OldValue:             "a",
				NewValue:             "b",
				EnvironmentVariables: []string{"GL_ID=", "GL_USERNAME=username", "GL_PROTOCOL=protocol", "GL_REPOSITORY=repository"},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
		{
			desc: "missing gl_username value",
			req: gitalypb.UpdateHookRequest{
				Repository:           testRepo,
				Ref:                  []byte("master"),
				OldValue:             "a",
				NewValue:             "b",
				EnvironmentVariables: []string{"GL_ID=key-123", "GL_USERNAME=", "GL_PROTOCOL=protocol", "GL_REPOSITORY=repository"},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
		{
			desc: "missing gl_protocol value",
			req: gitalypb.UpdateHookRequest{
				Repository:           testRepo,
				Ref:                  []byte("master"),
				OldValue:             "a",
				NewValue:             "b",
				EnvironmentVariables: []string{"GL_ID=key-123", "GL_USERNAME=username", "GL_PROTOCOL=", "GL_REPOSITORY=repository"},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
		{
			desc: "missing gl_repository value",
			req: gitalypb.UpdateHookRequest{
				Repository: &gitalypb.Repository{
					StorageName:  testRepo.GetStorageName(),
					RelativePath: testRepo.GetRelativePath(),
					GlRepository: "",
				},
				Ref:                  []byte("master"),
				OldValue:             "a",
				NewValue:             "b",
				EnvironmentVariables: []string{"GL_ID=key-123", "GL_USERNAME=username", "GL_PROTOCOL=protocol", "GL_REPOSITORY="},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			stream, err := client.UpdateHook(ctx, &tc.req)
			require.NoError(t, err)

			var status int32
			var stderr, stdout bytes.Buffer
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}

				stderr.Write(resp.GetStderr())
				stdout.Write(resp.GetStdout())

				if err != nil {
					t.Errorf("error when receiving stream: %v", err)
				}

				status = resp.GetExitStatus().GetValue()
				require.NoError(t, err)
			}

			assert.Equal(t, tc.status, status)
			assert.Equal(t, tc.stderr, text.ChompBytes(stderr.Bytes()), "hook stderr")
			assert.Equal(t, tc.stdout, text.ChompBytes(stdout.Bytes()), "hook stdout")
		})
	}
}

func TestUpdate_CustomHooks(t *testing.T) {
	rubyDir := config.Config.Ruby.Dir
	defer func() {
		config.Config.Ruby.Dir = rubyDir
	}()

	cwd, err := os.Getwd()
	require.NoError(t, err)
	config.Config.Ruby.Dir = filepath.Join(cwd, "testdata")

	serverSocketPath, stop := runHooksServer(t, config.Config.Hooks)
	defer stop()

	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()
	req := gitalypb.UpdateHookRequest{
		Repository:           testRepo,
		Ref:                  []byte("master"),
		OldValue:             "a",
		NewValue:             "b",
		EnvironmentVariables: []string{"GL_ID=key-123", "GL_USERNAME=username", "GL_PROTOCOL=protocol", "GL_REPOSITORY=repository"},
	}

	errorMsg := "error123"
	cleanup, err := testhelper.WriteCustomHook(testRepoPath, "update", []byte(fmt.Sprintf(`#!/bin/bash
echo %s 1>&2
exit 1
`, errorMsg)))
	require.NoError(t, err)
	defer cleanup()

	req.EnvironmentVariables = append(req.EnvironmentVariables, featureflag.GoUpdateHookEnvVar+"=true")
	stream, err := client.UpdateHook(ctx, &req)
	require.NoError(t, err)

	var status int32
	var stderr, stdout bytes.Buffer
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err, "when receiving stream")

		stderr.Write(resp.GetStderr())
		stdout.Write(resp.GetStdout())

		status = resp.GetExitStatus().GetValue()
	}

	assert.Equal(t, int32(1), status)
	assert.Equal(t, errorMsg, text.ChompBytes(stderr.Bytes()), "hook stderr")
	assert.Equal(t, "", text.ChompBytes(stdout.Bytes()), "hook stdout")
}
