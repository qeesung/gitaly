package hook

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/metadata"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func TestPreReceiveInvalidArgument(t *testing.T) {
	serverSocketPath, stop := runHooksServer(t, config.Config.Hooks)
	defer stop()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	stream, err := client.PreReceiveHook(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&gitalypb.PreReceiveHookRequest{}))
	_, err = stream.Recv()

	testhelper.RequireGrpcError(t, err, codes.InvalidArgument)
}

func sendPreReceiveHookRequest(t *testing.T, stream gitalypb.HookService_PreReceiveHookClient, stdin io.Reader) ([]byte, []byte, int32, error) {
	go func() {
		writer := streamio.NewWriter(func(p []byte) error {
			return stream.Send(&gitalypb.PreReceiveHookRequest{Stdin: p})
		})
		_, err := io.Copy(writer, stdin)
		require.NoError(t, err)
		require.NoError(t, stream.CloseSend(), "close send")
	}()

	var status int32
	var stdout, stderr bytes.Buffer
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return stdout.Bytes(), stderr.Bytes(), -1, err
		}

		_, err = stdout.Write(resp.GetStdout())
		require.NoError(t, err)
		_, err = stderr.Write(resp.GetStderr())
		require.NoError(t, err)

		status = resp.GetExitStatus().GetValue()
		require.NoError(t, err)
	}

	return stdout.Bytes(), stderr.Bytes(), status, nil
}

func receivePreReceive(t *testing.T, stream gitalypb.HookService_PreReceiveHookClient, stdin io.Reader) ([]byte, []byte, int32) {
	stdout, stderr, status, err := sendPreReceiveHookRequest(t, stream, stdin)
	require.NoError(t, err)
	return stdout, stderr, status
}

func TestPreReceive(t *testing.T) {
	rubyDir := config.Config.Ruby.Dir
	defer func(rubyDir string) {
		config.Config.Ruby.Dir = rubyDir
	}(rubyDir)

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
		stdin          io.Reader
		req            gitalypb.PreReceiveHookRequest
		status         int32
		stdout, stderr string
	}{
		{
			desc:  "everything OK",
			stdin: bytes.NewBufferString("a\nb\nc\nd\ne\nf\ng"),
			req: gitalypb.PreReceiveHookRequest{
				Repository: testRepo,
				EnvironmentVariables: []string{
					"GL_ID=key-123",
					"GL_PROTOCOL=protocol",
					"GL_USERNAME=username",
					"GL_REPOSITORY=repository",
				},
			},
			status: 0,
			stdout: "OK",
			stderr: "",
		},
		{
			desc:  "missing stdin",
			stdin: bytes.NewBuffer(nil),
			req: gitalypb.PreReceiveHookRequest{
				Repository: testRepo,
				EnvironmentVariables: []string{
					"GL_ID=key-123",
					"GL_PROTOCOL=protocol",
					"GL_USERNAME=username",
					"GL_REPOSITORY=repository",
				},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
		{
			desc:  "missing gl_protocol",
			stdin: bytes.NewBufferString("a\nb\nc\nd\ne\nf\ng"),
			req: gitalypb.PreReceiveHookRequest{
				Repository: testRepo,
				EnvironmentVariables: []string{
					"GL_ID=key-123",
					"GL_USERNAME=username",
					"GL_PROTOCOL=",
					"GL_REPOSITORY=repository",
				},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
		{
			desc:  "missing gl_id",
			stdin: bytes.NewBufferString("a\nb\nc\nd\ne\nf\ng"),
			req: gitalypb.PreReceiveHookRequest{
				Repository: testRepo,
				EnvironmentVariables: []string{
					"GL_ID=",
					"GL_PROTOCOL=protocol",
					"GL_USERNAME=username",
					"GL_REPOSITORY=repository",
				},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
		{
			desc:  "missing gl_username",
			stdin: bytes.NewBufferString("a\nb\nc\nd\ne\nf\ng"),
			req: gitalypb.PreReceiveHookRequest{
				Repository: testRepo,
				EnvironmentVariables: []string{
					"GL_ID=key-123",
					"GL_PROTOCOL=protocol",
					"GL_USERNAME=",
					"GL_REPOSITORY=repository",
				},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
		{
			desc:  "missing gl_repository",
			stdin: bytes.NewBufferString("a\nb\nc\nd\ne\nf\ng"),
			req: gitalypb.PreReceiveHookRequest{
				Repository: testRepo,
				EnvironmentVariables: []string{
					"GL_ID=key-123",
					"GL_PROTOCOL=protocol",
					"GL_USERNAME=username",
					"GL_REPOSITORY=",
				},
			},
			status: 1,
			stdout: "",
			stderr: "FAIL",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			stream, err := client.PreReceiveHook(ctx)
			require.NoError(t, err)
			require.NoError(t, stream.Send(&tc.req))

			stdout, stderr, status := receivePreReceive(t, stream, tc.stdin)

			assert.Equal(t, tc.status, status)
			assert.Equal(t, tc.stderr, text.ChompBytes(stderr), "hook stderr")
			assert.Equal(t, tc.stdout, text.ChompBytes(stdout), "hook stdout")
		})
	}
}

func TestPreReceiveHook_GitlabAPIAccess(t *testing.T) {
	user, password := "user", "password"
	secretToken := "secret123"
	glID, glRepository := "key-123", "repository"
	changes := "changes123"
	protocol := "http"
	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	gitObjectDirRel := "git/object/dir"
	gitAlternateObjectRelDirs := []string{"alt/obj/dir/1", "alt/obj/dir/2"}

	gitObjectDirAbs := filepath.Join(testRepoPath, gitObjectDirRel)
	var gitAlternateObjectAbsDirs []string

	for _, gitAltObjectRel := range gitAlternateObjectRelDirs {
		gitAlternateObjectAbsDirs = append(gitAlternateObjectAbsDirs, filepath.Join(testRepoPath, gitAltObjectRel))
	}

	tmpDir, cleanup := testhelper.TempDir(t)
	defer cleanup()
	secretFilePath := filepath.Join(tmpDir, ".gitlab_shell_secret")
	testhelper.WriteShellSecretFile(t, tmpDir, secretToken)

	testRepo.GitObjectDirectory = gitObjectDirRel
	testRepo.GitAlternateObjectDirectories = gitAlternateObjectRelDirs

	server := testhelper.NewGitlabTestServer(testhelper.GitlabTestServerOptions{
		User:                        user,
		Password:                    password,
		SecretToken:                 secretToken,
		GLID:                        glID,
		GLRepository:                glRepository,
		Changes:                     changes,
		PostReceiveCounterDecreased: true,
		Protocol:                    protocol,
		GitPushOptions:              nil,
		GitObjectDir:                gitObjectDirAbs,
		GitAlternateObjectDirs:      gitAlternateObjectAbsDirs,
		RepoPath:                    testRepoPath,
	})

	defer server.Close()

	gitlabConfig := config.Gitlab{
		URL: server.URL,
		HTTPSettings: config.HTTPSettings{
			User:     user,
			Password: password,
		},
		SecretFile: secretFilePath,
	}

	gitlabAPI, err := NewGitlabAPI(gitlabConfig)
	require.NoError(t, err)

	serverSocketPath, stop := runHooksServerWithAPI(t, gitlabAPI, config.Hooks{})
	defer stop()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	stdin := bytes.NewBufferString(changes)
	req := gitalypb.PreReceiveHookRequest{
		Repository: testRepo,
		EnvironmentVariables: []string{
			"GL_ID=" + glID,
			"GL_PROTOCOL=" + protocol,
			"GL_USERNAME=username",
			"GL_REPOSITORY=" + glRepository,
			fmt.Sprintf("%s=true", featureflag.GoPreReceiveHookEnvVar),
		},
	}

	stream, err := client.PreReceiveHook(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&req))

	stdout, stderr, status := receivePreReceive(t, stream, stdin)

	require.Equal(t, int32(0), status)
	assert.Equal(t, "", text.ChompBytes(stderr), "hook stderr")
	assert.Equal(t, "", text.ChompBytes(stdout), "hook stdout")
}

func preReceiveHandler(increased bool) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		res.Header().Set("Content-Type", "application/json")
		res.WriteHeader(http.StatusOK)
		res.Write([]byte(fmt.Sprintf("{\"reference_counter_increased\": %v}", increased)))
	}
}

func allowedHandler(allowed bool) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		res.Header().Set("Content-Type", "application/json")
		if allowed {
			res.WriteHeader(http.StatusOK)
			res.Write([]byte(`{"status": true}`))
		} else {
			res.WriteHeader(http.StatusUnauthorized)
			res.Write([]byte(`{"message":"not allowed"}`))
		}
	}
}

func TestPreReceive_APIErrors(t *testing.T) {
	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	testCases := []struct {
		desc               string
		allowedHandler     http.HandlerFunc
		preReceiveHandler  http.HandlerFunc
		expectedExitStatus int32
		expectedStderr     string
	}{
		{
			desc: "/allowed endpoint returns 401",
			allowedHandler: http.HandlerFunc(
				func(res http.ResponseWriter, req *http.Request) {
					res.Header().Set("Content-Type", "application/json")
					res.WriteHeader(http.StatusUnauthorized)
					res.Write([]byte(`{"message":"not allowed"}`))
				}),
			expectedExitStatus: 1,
			expectedStderr:     "GitLab: not allowed",
		},
		{
			desc:               "/pre_receive endpoint fails to increase reference coutner",
			allowedHandler:     allowedHandler(true),
			preReceiveHandler:  preReceiveHandler(false),
			expectedExitStatus: 1,
		},
	}

	tmpDir, cleanup := testhelper.TempDir(t)
	defer cleanup()
	secretFilePath := filepath.Join(tmpDir, ".gitlab_shell_secret")
	testhelper.WriteShellSecretFile(t, tmpDir, "token")

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.Handle("/api/v4/internal/allowed", tc.allowedHandler)
			mux.Handle("/api/v4/internal/pre_receive", tc.preReceiveHandler)
			srv := httptest.NewServer(mux)
			defer srv.Close()

			gitlabConfig := config.Gitlab{
				URL:        srv.URL,
				SecretFile: secretFilePath,
			}

			gitlabAPI, err := NewGitlabAPI(gitlabConfig)
			require.NoError(t, err)

			serverSocketPath, stop := runHooksServerWithAPI(t, gitlabAPI, config.Hooks{})
			defer stop()

			client, conn := newHooksClient(t, serverSocketPath)
			defer conn.Close()

			ctx, cancel := testhelper.Context()
			defer cancel()

			stream, err := client.PreReceiveHook(ctx)
			require.NoError(t, err)
			require.NoError(t, stream.Send(&gitalypb.PreReceiveHookRequest{
				Repository: testRepo,
				EnvironmentVariables: []string{
					"GL_ID=key-123",
					"GL_PROTOCOL=web",
					"GL_USERNAME=username",
					"GL_REPOSITORY=repository",
					fmt.Sprintf("%s=true", featureflag.GoPreReceiveHookEnvVar),
				},
			}))
			require.NoError(t, stream.CloseSend())

			_, stderr, status := receivePreReceive(t, stream, &bytes.Buffer{})

			require.Equal(t, tc.expectedExitStatus, status)
			assert.Equal(t, tc.expectedStderr, text.ChompBytes(stderr), "hook stderr")
		})
	}
}

func TestPreReceiveHook_CustomHookErrors(t *testing.T) {
	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	mux := http.NewServeMux()
	mux.Handle("/api/v4/internal/allowed", allowedHandler(true))
	mux.Handle("/api/v4/internal/pre_receive", preReceiveHandler(true))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmpDir, cleanup := testhelper.TempDir(t)
	defer cleanup()
	secretFilePath := filepath.Join(tmpDir, ".gitlab_shell_secret")
	testhelper.WriteShellSecretFile(t, tmpDir, "token")

	customHookReturnCode := int32(128)
	customHookReturnMsg := "custom hook error"
	testhelper.WriteCustomHook(testRepoPath, "pre-receive", []byte(fmt.Sprintf(`#!/bin/bash
echo '%s' 1>&2
exit %d
`, customHookReturnMsg, customHookReturnCode)))

	gitlabConfig := config.Gitlab{
		URL:        srv.URL,
		SecretFile: secretFilePath,
	}

	gitlabAPI, err := NewGitlabAPI(gitlabConfig)
	require.NoError(t, err)

	serverSocketPath, stop := runHooksServerWithAPI(t, gitlabAPI, config.Hooks{})
	defer stop()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	stream, err := client.PreReceiveHook(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&gitalypb.PreReceiveHookRequest{
		Repository: testRepo,
		EnvironmentVariables: []string{
			"GL_ID=key-123",
			"GL_PROTOCOL=web",
			"GL_USERNAME=username",
			"GL_REPOSITORY=repository",
			fmt.Sprintf("%s=true", featureflag.GoPreReceiveHookEnvVar),
		},
	}))

	require.NoError(t, stream.CloseSend())

	_, stderr, status := receivePreReceive(t, stream, &bytes.Buffer{})

	require.Equal(t, customHookReturnCode, status)
	assert.Equal(t, customHookReturnMsg, text.ChompBytes(stderr), "hook stderr")
}

type testTransactionServer struct {
	handler func(in *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error)
}

func (s *testTransactionServer) VoteTransaction(ctx context.Context, in *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
	if s.handler != nil {
		return s.handler(in)
	}
	return nil, nil
}

func TestPreReceiveHook_Primary(t *testing.T) {
	rubyDir := config.Config.Ruby.Dir
	defer func(rubyDir string) {
		config.Config.Ruby.Dir = rubyDir
	}(rubyDir)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	config.Config.Ruby.Dir = filepath.Join(cwd, "testdata")

	testCases := []struct {
		desc               string
		primary            bool
		useRubyHook        bool
		allowedHandler     http.HandlerFunc
		preReceiveHandler  http.HandlerFunc
		stdin              []byte
		hookExitCode       int32
		expectedExitStatus int32
		expectedStderr     string
		expectedReftxHash  []byte
	}{
		{
			desc:               "primary checks for permissions",
			primary:            true,
			allowedHandler:     allowedHandler(false),
			expectedExitStatus: 1,
			expectedStderr:     "GitLab: not allowed",
		},
		{
			desc:               "secondary checks for permissions",
			primary:            false,
			allowedHandler:     allowedHandler(false),
			expectedExitStatus: 0,
			expectedReftxHash:  []byte{},
		},
		{
			desc:               "primary tries to increase reference counter",
			primary:            true,
			allowedHandler:     allowedHandler(true),
			preReceiveHandler:  preReceiveHandler(false),
			expectedExitStatus: 1,
			expectedStderr:     "",
		},
		{
			desc:               "secondary does not try to increase reference counter",
			primary:            false,
			allowedHandler:     allowedHandler(true),
			preReceiveHandler:  preReceiveHandler(false),
			expectedExitStatus: 0,
			expectedReftxHash:  []byte{},
		},
		{
			desc:               "primary executes hook",
			primary:            true,
			allowedHandler:     allowedHandler(true),
			preReceiveHandler:  preReceiveHandler(true),
			hookExitCode:       123,
			expectedExitStatus: 123,
		},
		{
			desc:               "secondary does not execute hook",
			primary:            false,
			allowedHandler:     allowedHandler(true),
			preReceiveHandler:  preReceiveHandler(true),
			hookExitCode:       123,
			expectedExitStatus: 0,
			expectedReftxHash:  []byte{},
		},
		{
			desc:               "primary hook triggers transaction",
			primary:            true,
			stdin:              []byte("foobar"),
			allowedHandler:     allowedHandler(true),
			preReceiveHandler:  preReceiveHandler(true),
			hookExitCode:       0,
			expectedExitStatus: 0,
			expectedReftxHash:  []byte("foobar"),
		},
		{
			desc:               "secondary hook triggers transaction",
			primary:            false,
			stdin:              []byte("foobar"),
			allowedHandler:     allowedHandler(true),
			preReceiveHandler:  preReceiveHandler(true),
			hookExitCode:       0,
			expectedExitStatus: 0,
			expectedReftxHash:  []byte("foobar"),
		},
		{
			desc:               "primary Ruby hook triggers transaction",
			primary:            true,
			useRubyHook:        true,
			stdin:              []byte("foobar"),
			allowedHandler:     allowedHandler(true),
			preReceiveHandler:  preReceiveHandler(true),
			hookExitCode:       0,
			expectedExitStatus: 0,
			expectedReftxHash:  []byte("foobar"),
		},
		{
			desc:               "secondary Ruby hook triggers transaction",
			primary:            false,
			useRubyHook:        true,
			stdin:              []byte("foobar"),
			allowedHandler:     allowedHandler(true),
			preReceiveHandler:  preReceiveHandler(true),
			hookExitCode:       0,
			expectedExitStatus: 0,
			expectedReftxHash:  []byte("foobar"),
		},
	}

	transactionServer := &testTransactionServer{}
	grpcServer := grpc.NewServer()
	gitalypb.RegisterRefTransactionServer(grpcServer, transactionServer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	errQ := make(chan error)
	go func() {
		errQ <- grpcServer.Serve(listener)
	}()
	defer func() {
		grpcServer.Stop()
		require.NoError(t, <-errQ)
	}()

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			var reftxHash []byte
			transactionServer.handler = func(in *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				reftxHash = in.ReferenceUpdatesHash
				return &gitalypb.VoteTransactionResponse{
					State: gitalypb.VoteTransactionResponse_COMMIT,
				}, nil
			}

			testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
			defer cleanupFn()

			mux := http.NewServeMux()
			mux.Handle("/api/v4/internal/allowed", tc.allowedHandler)
			mux.Handle("/api/v4/internal/pre_receive", tc.preReceiveHandler)
			srv := httptest.NewServer(mux)
			defer srv.Close()

			tmpDir, cleanup := testhelper.TempDir(t)
			defer cleanup()

			secretFilePath := filepath.Join(tmpDir, ".gitlab_shell_secret")
			testhelper.WriteShellSecretFile(t, tmpDir, "token")
			testhelper.WriteCustomHook(testRepoPath, "pre-receive", []byte(fmt.Sprintf("#!/bin/bash\nexit %d", tc.hookExitCode)))

			gitlabAPI, err := NewGitlabAPI(config.Gitlab{
				URL:        srv.URL,
				SecretFile: secretFilePath,
			})
			require.NoError(t, err)

			serverSocketPath, stop := runHooksServerWithAPI(t, gitlabAPI, config.Hooks{})
			defer stop()

			client, conn := newHooksClient(t, serverSocketPath)
			defer conn.Close()

			ctx, cancel := testhelper.Context()
			defer cancel()

			transactionServer := metadata.PraefectServer{
				ListenAddr: "tcp://" + listener.Addr().String(),
			}
			transactionServerEnv, err := transactionServer.Env()
			require.NoError(t, err)

			transaction := metadata.Transaction{
				ID:      1234,
				Node:    "node-1",
				Primary: tc.primary,
			}
			transactionEnv, err := transaction.Env()
			require.NoError(t, err)

			environment := []string{
				"GL_ID=key-123",
				"GL_PROTOCOL=web",
				"GL_USERNAME=username",
				"GL_REPOSITORY=repository",
				transactionEnv,
				transactionServerEnv,
			}
			if !tc.useRubyHook {
				environment = append(environment, fmt.Sprintf("%s=true", featureflag.GoPreReceiveHookEnvVar))
			}

			stream, err := client.PreReceiveHook(ctx)
			require.NoError(t, err)
			require.NoError(t, stream.Send(&gitalypb.PreReceiveHookRequest{
				Repository:           testRepo,
				EnvironmentVariables: environment,
			}))
			require.NoError(t, stream.Send(&gitalypb.PreReceiveHookRequest{
				Stdin: tc.stdin,
			}))
			require.NoError(t, stream.CloseSend())

			_, stderr, status, _ := sendPreReceiveHookRequest(t, stream, &bytes.Buffer{})

			var expectedReftxHash []byte
			if tc.expectedReftxHash != nil {
				hash := sha1.Sum(tc.expectedReftxHash)
				expectedReftxHash = hash[:]
			}

			require.Equal(t, tc.expectedExitStatus, status)
			require.Equal(t, tc.expectedStderr, text.ChompBytes(stderr))
			require.Equal(t, expectedReftxHash[:], reftxHash)
		})
	}
}
