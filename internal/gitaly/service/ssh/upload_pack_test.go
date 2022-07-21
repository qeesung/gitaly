//go:build !gitaly_test_sha256

package ssh

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v15/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

func runTestWithAndWithoutConfigOptions(t *testing.T, tf func(t *testing.T, opts ...testcfg.Option), opts ...testcfg.Option) {
	t.Run("no config options", func(t *testing.T) { tf(t) })

	if len(opts) > 0 {
		t.Run("with config options", func(t *testing.T) {
			tf(t, opts...)
		})
	}
}

// runClone runs the given Git command with gitaly-ssh set up as its SSH command. It will thus
// invoke the Gitaly server's SSHUploadPack or SSHUploadPackWithSidechannel endpoint.
func runClone(
	ctx context.Context,
	t *testing.T,
	cfg config.Cfg,
	withSidechannel bool,
	cloneCmd git.Cmd,
	request *gitalypb.SSHUploadPackRequest,
) error {
	payload, err := protojson.Marshal(request)
	require.NoError(t, err)

	var flagsWithValues []string
	for flag, value := range featureflag.FromContext(ctx) {
		flagsWithValues = append(flagsWithValues, flag.FormatWithValue(value))
	}

	env := []string{
		fmt.Sprintf("GITALY_ADDRESS=%s", cfg.SocketPath),
		fmt.Sprintf("GITALY_PAYLOAD=%s", payload),
		fmt.Sprintf("GITALY_FEATUREFLAGS=%s", strings.Join(flagsWithValues, ",")),
		fmt.Sprintf(`GIT_SSH_COMMAND=%s upload-pack`, cfg.BinaryPath("gitaly-ssh")),
	}
	if withSidechannel {
		env = append(env, "GITALY_USE_SIDECHANNEL=1")
	}

	var output bytes.Buffer
	gitCommand, err := gittest.NewCommandFactory(t, cfg).NewWithoutRepo(ctx,
		cloneCmd, git.WithStdout(&output), git.WithStderr(&output), git.WithEnv(env...), git.WithDisabledHooks(),
	)
	require.NoError(t, err)

	if err := gitCommand.Wait(); err != nil {
		return fmt.Errorf("Failed to run `git clone`: %q", output.Bytes())
	}

	return nil
}

func requireRevisionsEqual(t *testing.T, cfg config.Cfg, repoPathA, repoPathB, revision string) {
	t.Helper()
	require.Equal(t,
		text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPathA, "rev-parse", revision+"^{}")),
		text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPathB, "rev-parse", revision+"^{}")),
	)
}

func TestUploadPack_timeout(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testUploadPackTimeout, testcfg.WithPackObjectsCacheEnabled())
}

func testUploadPackTimeout(t *testing.T, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)

	cfg.SocketPath = runSSHServerWithOptions(t, cfg, []ServerOpt{WithUploadPackRequestTimeout(10 * time.Microsecond)})

	repo, _ := gittest.CreateRepository(testhelper.Context(t), t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	client, conn := newSSHClient(t, cfg.SocketPath)
	defer conn.Close()
	ctx := testhelper.Context(t)

	stream, err := client.SSHUploadPack(ctx)
	require.NoError(t, err)

	// The first request is not limited by timeout, but also not under attacker control
	require.NoError(t, stream.Send(&gitalypb.SSHUploadPackRequest{Repository: repo}))

	// Because the client says nothing, the server would block. Because of
	// the timeout, it won't block forever, and return with a non-zero exit
	// code instead.
	requireFailedSSHStream(t, func() (int32, error) {
		resp, err := stream.Recv()
		if err != nil {
			return 0, err
		}

		var code int32
		if status := resp.GetExitStatus(); status != nil {
			code = status.Value
		}

		return code, nil
	})
}

func TestUploadPackWithSidechannel_client(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(testhelper.Context(t), t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})
	commitID := gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "HEAD^{commit}")

	registry := sidechannel.NewRegistry()
	clientHandshaker := sidechannel.NewClientHandshaker(testhelper.NewDiscardingLogEntry(t), registry)
	conn, err := grpc.Dial(cfg.SocketPath,
		grpc.WithTransportCredentials(clientHandshaker.ClientHandshake(insecure.NewCredentials())),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(cfg.Auth.Token)),
	)
	require.NoError(t, err)

	client := gitalypb.NewSSHServiceClient(conn)
	defer testhelper.MustClose(t, conn)

	for _, tc := range []struct {
		desc             string
		request          *gitalypb.SSHUploadPackWithSidechannelRequest
		client           func(clientConn *sidechannel.ClientConn, cancelContext func()) error
		expectedErr      error
		expectedResponse *gitalypb.SSHUploadPackWithSidechannelResponse
	}{
		{
			desc: "successful clone",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository: repo,
			},
			client: func(clientConn *sidechannel.ClientConn, _ func()) error {
				gittest.WritePktlineString(t, clientConn, "want "+text.ChompBytes(commitID)+" multi_ack\n")
				gittest.WritePktlineFlush(t, clientConn)
				gittest.WritePktlineString(t, clientConn, "done\n")

				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedResponse: &gitalypb.SSHUploadPackWithSidechannelResponse{},
		},
		{
			desc: "successful clone with protocol v2",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			client: func(clientConn *sidechannel.ClientConn, _ func()) error {
				gittest.WritePktlineString(t, clientConn, "command=fetch\n")
				gittest.WritePktlineString(t, clientConn, "agent=git/2.36.1\n")
				gittest.WritePktlineString(t, clientConn, "object-format=sha1\n")
				gittest.WritePktlineDelim(t, clientConn)
				gittest.WritePktlineString(t, clientConn, "want "+text.ChompBytes(commitID)+"\n")
				gittest.WritePktlineString(t, clientConn, "done\n")
				gittest.WritePktlineFlush(t, clientConn)

				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedResponse: &gitalypb.SSHUploadPackWithSidechannelResponse{},
		},
		{
			desc: "client talks protocol v0 but v2 is requested",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			client: func(clientConn *sidechannel.ClientConn, _ func()) error {
				gittest.WritePktlineString(t, clientConn, "want "+text.ChompBytes(commitID)+" multi_ack\n")
				gittest.WritePktlineFlush(t, clientConn)
				gittest.WritePktlineString(t, clientConn, "done\n")

				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedErr: helper.ErrInternalf(
				"cmd wait: exit status 128, stderr: %q",
				"fatal: unknown capability 'want 1e292f8fedd741b75372e19097c76d327140c312 multi_ack'\n",
			),
		},
		{
			desc: "client talks protocol v2 but v0 is requested",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository: repo,
			},
			client: func(clientConn *sidechannel.ClientConn, _ func()) error {
				gittest.WritePktlineString(t, clientConn, "command=fetch\n")
				gittest.WritePktlineString(t, clientConn, "agent=git/2.36.1\n")
				gittest.WritePktlineString(t, clientConn, "object-format=sha1\n")
				gittest.WritePktlineDelim(t, clientConn)
				gittest.WritePktlineString(t, clientConn, "want "+text.ChompBytes(commitID)+"\n")
				gittest.WritePktlineString(t, clientConn, "done\n")
				gittest.WritePktlineFlush(t, clientConn)

				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedErr: helper.ErrInternalf(
				"cmd wait: exit status 128, stderr: %q",
				"fatal: git upload-pack: protocol error, expected to get object ID, not 'command=fetch'\n",
			),
		},
		{
			desc: "missing input",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			client: func(clientConn *sidechannel.ClientConn, _ func()) error {
				require.NoError(t, clientConn.CloseWrite())
				return nil
			},
			expectedResponse: &gitalypb.SSHUploadPackWithSidechannelResponse{},
		},
		{
			desc: "short write",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			client: func(clientConn *sidechannel.ClientConn, _ func()) error {
				gittest.WritePktlineString(t, clientConn, "command=fetch\n")

				_, err := io.WriteString(clientConn, "0011agent")
				require.NoError(t, err)
				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedErr: helper.ErrCanceledf("cmd wait: exit status 128, stderr: %q", "fatal: the remote end hung up unexpectedly\n"),
		},
		{
			desc: "garbage",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			client: func(clientConn *sidechannel.ClientConn, _ func()) error {
				gittest.WritePktlineString(t, clientConn, "foobar")
				require.NoError(t, clientConn.CloseWrite())
				return nil
			},
			expectedErr: helper.ErrInternalf("cmd wait: exit status 128, stderr: %q", "fatal: unknown capability 'foobar'\n"),
		},
		{
			desc: "close and cancellation",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			client: func(clientConn *sidechannel.ClientConn, cancelContext func()) error {
				gittest.WritePktlineString(t, clientConn, "command=fetch\n")
				gittest.WritePktlineString(t, clientConn, "agent=git/2.36.1\n")

				require.NoError(t, clientConn.CloseWrite())
				cancelContext()

				return nil
			},
			expectedErr: helper.ErrCanceled(context.Canceled),
		},
		{
			desc: "cancellation and close",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			client: func(clientConn *sidechannel.ClientConn, cancelContext func()) error {
				gittest.WritePktlineString(t, clientConn, "command=fetch\n")
				gittest.WritePktlineString(t, clientConn, "agent=git/2.36.1\n")

				cancelContext()
				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedErr: helper.ErrCanceled(context.Canceled),
		},
		{
			desc: "cancellation without close",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			client: func(clientConn *sidechannel.ClientConn, cancelContext func()) error {
				gittest.WritePktlineString(t, clientConn, "command=fetch\n")
				gittest.WritePktlineString(t, clientConn, "agent=git/2.36.1\n")

				cancelContext()

				return nil
			},
			expectedErr: helper.ErrCanceled(context.Canceled),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(testhelper.Context(t))

			ctx, waiter := sidechannel.RegisterSidechannel(ctx, registry, func(clientConn *sidechannel.ClientConn) (returnedErr error) {
				errCh := make(chan error, 1)
				go func() {
					_, err := io.Copy(io.Discard, clientConn)
					errCh <- err
				}()
				defer func() {
					if err := <-errCh; err != nil && returnedErr == nil {
						returnedErr = err
					}
				}()

				return tc.client(clientConn, cancel)
			})
			defer testhelper.MustClose(t, waiter)

			response, err := client.SSHUploadPackWithSidechannel(ctx, tc.request)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
			testhelper.ProtoEqual(t, tc.expectedResponse, response)
		})
	}
}

func requireFailedSSHStream(t *testing.T, recv func() (int32, error)) {
	done := make(chan struct{})
	var code int32
	var err error

	go func() {
		for err == nil {
			code, err = recv()
		}
		close(done)
	}()

	select {
	case <-done:
		testhelper.RequireGrpcCode(t, err, codes.Internal)
		require.NotEqual(t, 0, code, "exit status")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for SSH stream")
	}
}

func TestUploadPack_validation(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)

	serverSocketPath := runSSHServer(t, cfg)

	client, conn := newSSHClient(t, serverSocketPath)
	defer conn.Close()

	for _, tc := range []struct {
		desc        string
		request     *gitalypb.SSHUploadPackRequest
		expectedErr error
	}{
		{
			desc: "missing relative path",
			request: &gitalypb.SSHUploadPackRequest{
				Repository: &gitalypb.Repository{
					StorageName:  cfg.Storages[0].Name,
					RelativePath: "",
				},
			},
			expectedErr: func() error {
				if testhelper.IsPraefectEnabled() {
					return helper.ErrInvalidArgumentf("repo scoped: invalid Repository")
				}
				return helper.ErrInvalidArgumentf("GetPath: relative path missing from storage_name:%q", "default")
			}(),
		},
		{
			desc: "missing repository",
			request: &gitalypb.SSHUploadPackRequest{
				Repository: nil,
			},
			expectedErr: func() error {
				if testhelper.IsPraefectEnabled() {
					return helper.ErrInvalidArgumentf("repo scoped: empty Repository")
				}
				return helper.ErrInvalidArgumentf("GetStorageByName: no such storage: \"\"")
			}(),
		},
		{
			desc: "data in first request",
			request: &gitalypb.SSHUploadPackRequest{
				Repository: &gitalypb.Repository{
					StorageName:  cfg.Storages[0].Name,
					RelativePath: "path/to/repo",
				},
				Stdin: []byte("Fail"),
			},
			expectedErr: func() error {
				if testhelper.IsPraefectEnabled() {
					return helper.ErrNotFoundf("accessor call: route repository accessor: consistent storages: repository %q/%q not found", cfg.Storages[0].Name, "path/to/repo")
				}
				return helper.ErrInvalidArgumentf("non-empty stdin in first request")
			}(),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			stream, err := client.SSHUploadPack(ctx)
			require.NoError(t, err)
			require.NoError(t, stream.Send(tc.request))
			require.NoError(t, stream.CloseSend())

			err = recvUntilError(t, stream)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func TestUploadPack_successful(t *testing.T) {
	t.Parallel()

	for _, withSidechannel := range []bool{true, false} {
		t.Run(fmt.Sprintf("sidechannel=%v", withSidechannel), func(t *testing.T) {
			runTestWithAndWithoutConfigOptions(t, func(t *testing.T, opts ...testcfg.Option) {
				testUploadPackSuccessful(t, withSidechannel, opts...)
			})
		})
	}
}

func testUploadPackSuccessful(t *testing.T, sidechannel bool, opts ...testcfg.Option) {
	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t, opts...)

	testcfg.BuildGitalyHooks(t, cfg)
	testcfg.BuildGitalySSH(t, cfg)

	negotiationMetrics := prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"feature"})
	protocolDetectingFactory := gittest.NewProtocolDetectingCommandFactory(ctx, t, cfg)

	cfg.SocketPath = runSSHServerWithOptions(t, cfg, []ServerOpt{
		WithPackfileNegotiationMetrics(negotiationMetrics),
	}, testserver.WithGitCommandFactory(protocolDetectingFactory))

	repo, repoPath := gittest.CreateRepository(testhelper.Context(t), t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	for _, tc := range []struct {
		desc             string
		request          *gitalypb.SSHUploadPackRequest
		cloneFlags       []git.Option
		deepen           float64
		verify           func(t *testing.T, localRepoPath string)
		expectedProtocol string
	}{
		{
			desc: "full clone",
			request: &gitalypb.SSHUploadPackRequest{
				Repository: repo,
			},
		},
		{
			desc: "full clone with protocol v2",
			request: &gitalypb.SSHUploadPackRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			expectedProtocol: git.ProtocolV2,
		},
		{
			desc: "shallow clone",
			request: &gitalypb.SSHUploadPackRequest{
				Repository: repo,
			},
			cloneFlags: []git.Option{
				git.ValueFlag{Name: "--depth", Value: "1"},
			},
			deepen: 1,
		},
		{
			desc: "shallow clone with protocol v2",
			request: &gitalypb.SSHUploadPackRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			cloneFlags: []git.Option{
				git.ValueFlag{Name: "--depth", Value: "1"},
			},
			deepen:           1,
			expectedProtocol: git.ProtocolV2,
		},
		{
			desc: "partial clone",
			request: &gitalypb.SSHUploadPackRequest{
				Repository: repo,
			},
			cloneFlags: []git.Option{
				git.ValueFlag{Name: "--filter", Value: "blob:limit=2048"},
			},
			verify: func(t *testing.T, repoPath string) {
				// Ruby file which is ~1kB in size and not present in HEAD
				blobLessThanLimit := git.ObjectID("6ee41e85cc9bf33c10b690df09ca735b22f3790f")
				// Image which is ~100kB in size and not present in HEAD
				blobGreaterThanLimit := git.ObjectID("18079e308ff9b3a5e304941020747e5c39b46c88")

				gittest.RequireObjectNotExists(t, cfg, repoPath, blobGreaterThanLimit)
				gittest.RequireObjectExists(t, cfg, repoPath, blobLessThanLimit)
			},
		},
		{
			desc: "hidden tags",
			cloneFlags: []git.Option{
				git.Flag{Name: "--mirror"},
			},
			request: &gitalypb.SSHUploadPackRequest{
				Repository: repo,
				GitConfigOptions: []string{
					"transfer.hideRefs=refs/tags",
				},
			},
			verify: func(t *testing.T, localRepoPath string) {
				// Assert that there is at least one tag that should've been cloned
				// if refs weren't hidden as expected
				require.NotEmpty(t, gittest.Exec(t, cfg, "-C", repoPath, "tag"))

				// And then verify that we did indeed hide tags as expected, which
				// is demonstrated by not having fetched any tags.
				require.Empty(t, gittest.Exec(t, cfg, "-C", localRepoPath, "tag"))
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			localRepoPath := testhelper.TempDir(t)

			negotiationMetrics.Reset()
			protocolDetectingFactory.Reset(t)

			require.NoError(t, runClone(ctx, t, cfg, sidechannel, git.SubCmd{
				Name:  "clone",
				Args:  []string{"git@localhost:test/test.git", localRepoPath},
				Flags: tc.cloneFlags,
			}, tc.request))

			requireRevisionsEqual(t, cfg, repoPath, localRepoPath, "refs/heads/master")

			metric, err := negotiationMetrics.GetMetricWithLabelValues("deepen")
			require.NoError(t, err)
			require.Equal(t, tc.deepen, promtest.ToFloat64(metric))

			if tc.verify != nil {
				tc.verify(t, localRepoPath)
			}

			protocol := protocolDetectingFactory.ReadProtocol(t)
			if tc.expectedProtocol != "" {
				require.Contains(t, protocol, fmt.Sprintf("GIT_PROTOCOL=%s\n", git.ProtocolV2))
			} else {
				require.Empty(t, protocol)
			}
		})
	}
}

func TestUploadPack_packObjectsHook(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t, testcfg.WithPackObjectsCacheEnabled())

	filterDir := testhelper.TempDir(t)
	outputPath := filepath.Join(filterDir, "output")
	cfg.BinDir = filterDir

	testcfg.BuildGitalySSH(t, cfg)

	// We're using a custom pack-objetcs hook for git-upload-pack. In order
	// to assure that it's getting executed as expected, we're writing a
	// custom script which replaces the hook binary. It doesn't do anything
	// special, but writes an error message and errors out and should thus
	// cause the clone to fail with this error message.
	testhelper.WriteExecutable(t, cfg.BinaryPath("gitaly-hooks"), []byte(fmt.Sprintf(
		`#!/bin/bash
		set -eo pipefail
		echo 'I was invoked' >'%s'
		shift
		exec git "$@"
	`, outputPath)))

	cfg.SocketPath = runSSHServer(t, cfg)

	repo, _ := gittest.CreateRepository(testhelper.Context(t), t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	localRepoPath := testhelper.TempDir(t)

	err := runClone(ctx, t, cfg, false, git.SubCmd{
		Name: "clone", Args: []string{"git@localhost:test/test.git", localRepoPath},
	}, &gitalypb.SSHUploadPackRequest{
		Repository: repo,
	})
	require.NoError(t, err)

	require.Equal(t, []byte("I was invoked\n"), testhelper.MustReadFile(t, outputPath))
}

func TestUploadPack_withoutSideband(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testUploadPackWithoutSideband, testcfg.WithPackObjectsCacheEnabled())
}

func testUploadPackWithoutSideband(t *testing.T, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)

	testcfg.BuildGitalySSH(t, cfg)
	testcfg.BuildGitalyHooks(t, cfg)

	cfg.SocketPath = runSSHServer(t, cfg)

	repo, _ := gittest.CreateRepository(testhelper.Context(t), t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	// While Git knows the side-band-64 capability, some other clients don't. There is no way
	// though to have Git not use that capability, so we're instead manually crafting a packfile
	// negotiation without that capability and send it along.
	negotiation := bytes.NewBuffer([]byte{})
	gittest.WritePktlineString(t, negotiation, "want 1e292f8fedd741b75372e19097c76d327140c312 multi_ack_detailed thin-pack include-tag ofs-delta agent=git/2.29.1")
	gittest.WritePktlineString(t, negotiation, "want 1e292f8fedd741b75372e19097c76d327140c312")
	gittest.WritePktlineFlush(t, negotiation)
	gittest.WritePktlineString(t, negotiation, "done")

	request := &gitalypb.SSHUploadPackRequest{
		Repository: repo,
	}
	payload, err := protojson.Marshal(request)
	require.NoError(t, err)

	// As we're not using the sideband, the remote process will write both to stdout and stderr.
	// Those simultaneous writes to both stdout and stderr created a race as we could've invoked
	// two concurrent `SendMsg`s on the gRPC stream. And given that `SendMsg` is not thread-safe
	// a deadlock would result.
	uploadPack := exec.Command(cfg.BinaryPath("gitaly-ssh"), "upload-pack", "dontcare", "dontcare")
	uploadPack.Env = []string{
		fmt.Sprintf("GITALY_ADDRESS=%s", cfg.SocketPath),
		fmt.Sprintf("GITALY_PAYLOAD=%s", payload),
	}
	uploadPack.Stdin = negotiation

	out, err := uploadPack.CombinedOutput()
	require.NoError(t, err)
	require.True(t, uploadPack.ProcessState.Success())
	require.Contains(t, string(out), "refs/heads/master")
	require.Contains(t, string(out), "Counting objects")
	require.Contains(t, string(out), "PACK")
}

func TestUploadPack_invalidStorage(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	testcfg.BuildGitalySSH(t, cfg)

	repo, _ := gittest.CreateRepository(testhelper.Context(t), t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	localRepoPath := testhelper.TempDir(t)

	err := runClone(ctx, t, cfg, false, git.SubCmd{
		Name: "clone",
		Args: []string{
			"git@localhost:test/test.git", localRepoPath,
		},
	}, &gitalypb.SSHUploadPackRequest{
		Repository: &gitalypb.Repository{
			StorageName:  "foobar",
			RelativePath: repo.GetRelativePath(),
		},
	})
	require.Error(t, err)

	if testhelper.IsPraefectEnabled() {
		require.Contains(t, err.Error(), "rpc error: code = InvalidArgument desc = repo scoped: invalid Repository")
	} else {
		require.Contains(t, err.Error(), "rpc error: code = InvalidArgument desc = GetStorageByName: no such storage: \\\"foobar\\\"")
	}
}

func TestUploadPack_gitFailure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(testhelper.Context(t), t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	client, conn := newSSHClient(t, cfg.SocketPath)
	defer conn.Close()

	// Writing an invalid config will allow repo to pass the `IsGitDirectory` check but still
	// trigger an error when git tries to access the repo.
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "config"), []byte("Not a valid gitconfig"), 0o644))

	stream, err := client.SSHUploadPack(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&gitalypb.SSHUploadPackRequest{Repository: repo}))
	require.NoError(t, stream.CloseSend())

	err = recvUntilError(t, stream)
	testhelper.RequireGrpcError(t, helper.ErrInternalf(`cmd wait: exit status 128, stderr: "fatal: bad config line 1 in file ./config\n"`), err)
}

func recvUntilError(t *testing.T, stream gitalypb.SSHService_SSHUploadPackClient) error {
	for {
		response, err := stream.Recv()
		require.Nil(t, response.GetStdout())
		if err != nil {
			return err
		}
	}
}
