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
	gitalyauth "gitlab.com/gitlab-org/gitaly/v16/auth"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
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
	t *testing.T,
	ctx context.Context,
	cfg config.Cfg,
	withSidechannel bool,
	request *gitalypb.SSHUploadPackRequest,
	args ...string,
) error {
	payload, err := protojson.Marshal(request)
	require.NoError(t, err)

	var flagsWithValues []string
	for flag, value := range featureflag.FromContext(ctx) {
		flagsWithValues = append(flagsWithValues, flag.FormatWithValue(value))
	}

	var output bytes.Buffer
	cloneCmd := gittest.NewCommand(t, cfg, append([]string{"clone"}, args...)...)
	cloneCmd.Stdout = &output
	cloneCmd.Stderr = &output
	cloneCmd.Env = append(cloneCmd.Env,
		fmt.Sprintf("GITALY_ADDRESS=%s", cfg.SocketPath),
		fmt.Sprintf("GITALY_PAYLOAD=%s", payload),
		fmt.Sprintf("GITALY_FEATUREFLAGS=%s", strings.Join(flagsWithValues, ",")),
		fmt.Sprintf(`GIT_SSH_COMMAND=%s upload-pack`, cfg.BinaryPath("gitaly-ssh")),
	)
	if withSidechannel {
		cloneCmd.Env = append(cloneCmd.Env, "GITALY_USE_SIDECHANNEL=1")
	}

	if err := cloneCmd.Run(); err != nil {
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
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t, opts...)

	// Use a ticker channel so that we can observe that the ticker is being created. The channel
	// is unbuffered on purpose so that we can assert that it is getting created exactly at the
	// time we expect it to be.
	tickerCh := make(chan *helper.ManualTicker)

	cfg.SocketPath = runSSHServerWithOptions(t, cfg, []ServerOpt{
		WithUploadPackRequestTimeoutTickerFactory(func() helper.Ticker {
			// Create a ticker that will immediately tick when getting reset so that the
			// server-side can observe this as an emulated timeout.
			ticker := helper.NewManualTicker()
			ticker.ResetFunc = func() {
				ticker.Tick()
			}
			tickerCh <- ticker
			return ticker
		}),
	})

	repo, repoPath := gittest.CreateRepository(t, testhelper.Context(t), cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	client := newSSHClient(t, cfg.SocketPath)

	stream, err := client.SSHUploadPack(ctx)
	require.NoError(t, err)

	// The first request is not limited by timeout, but also not under attacker control
	require.NoError(t, stream.Send(&gitalypb.SSHUploadPackRequest{Repository: repo}))

	// We should now see that the ticker limiting the request is being created. We don't need to
	// use the ticker, but this statement is only there in order to verify that the ticker is
	// indeed getting created at the expected point in time.
	<-tickerCh

	// Because the client says nothing, the server would block. Because of
	// the timeout, it won't block forever, and return with a non-zero exit
	// code instead.
	requireFailedSSHStream(t, structerr.NewDeadlineExceeded("waiting for packfile negotiation: context canceled"), func() (int32, error) {
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

	repo, repoPath := gittest.CreateRepository(t, testhelper.Context(t), cfg)
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	registry := sidechannel.NewRegistry()
	clientHandshaker := sidechannel.NewClientHandshaker(testhelper.SharedLogger(t), registry)
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
				gittest.WritePktlinef(t, clientConn, "want %s multi_ack\n", commitID)
				gittest.WritePktlineFlush(t, clientConn)
				gittest.WritePktlineString(t, clientConn, "done\n")

				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedResponse: &gitalypb.SSHUploadPackWithSidechannelResponse{
				Bytes: int64(554),
				PackfileNegotiationStatistics: &gitalypb.PackfileNegotiationStatistics{
					Packets: 2,
					Caps:    []string{"multi_ack"},
					Wants:   1,
				},
			},
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
				gittest.WritePktlinef(t, clientConn, "object-format=%s\n", gittest.DefaultObjectHash.Format)
				gittest.WritePktlineDelim(t, clientConn)
				gittest.WritePktlinef(t, clientConn, "want %s\n", commitID)
				gittest.WritePktlineString(t, clientConn, "done\n")
				gittest.WritePktlineFlush(t, clientConn)

				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedResponse: &gitalypb.SSHUploadPackWithSidechannelResponse{
				Bytes: int64(536),
				PackfileNegotiationStatistics: &gitalypb.PackfileNegotiationStatistics{
					Packets: 5,
					Wants:   1,
				},
			},
		},
		{
			desc: "client talks protocol v0 but v2 is requested",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			client: func(clientConn *sidechannel.ClientConn, _ func()) error {
				gittest.WritePktlinef(t, clientConn, "want %s multi_ack\n", commitID)
				gittest.WritePktlineFlush(t, clientConn)
				gittest.WritePktlineString(t, clientConn, "done\n")

				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedErr: structerr.NewInternal(
				"cmd wait: exit status 128, stderr: \"fatal: unknown capability 'want %s multi_ack'\\n\"",
				commitID,
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
				gittest.WritePktlinef(t, clientConn, "object-format=%s\n", gittest.DefaultObjectHash.Format)
				gittest.WritePktlineDelim(t, clientConn)
				gittest.WritePktlinef(t, clientConn, "want %s\n", commitID)
				gittest.WritePktlineString(t, clientConn, "done\n")
				gittest.WritePktlineFlush(t, clientConn)

				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedErr: structerr.NewInternal(
				"cmd wait: exit status 128, stderr: %q",
				"fatal: git upload-pack: protocol error, expected to get object ID, not 'command=fetch'\n",
			),
		},
		{
			desc: "request missing object",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository: repo,
			},
			client: func(clientConn *sidechannel.ClientConn, _ func()) error {
				oid := strings.Repeat("1", gittest.DefaultObjectHash.EncodedLen())

				gittest.WritePktlinef(t, clientConn, "want %s multi_ack\n", oid)
				gittest.WritePktlineFlush(t, clientConn)
				gittest.WritePktlineString(t, clientConn, "done\n")

				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedErr: structerr.NewInternal("cmd wait: exit status 128, stderr: %q",
				"fatal: git upload-pack: not our ref "+strings.Repeat("1", gittest.DefaultObjectHash.EncodedLen())+"\n",
			),
		},
		{
			desc: "request invalidly formatted object",
			request: &gitalypb.SSHUploadPackWithSidechannelRequest{
				Repository: repo,
			},
			client: func(clientConn *sidechannel.ClientConn, _ func()) error {
				gittest.WritePktlineString(t, clientConn, "want 1111 multi_ack\n")
				gittest.WritePktlineFlush(t, clientConn)
				gittest.WritePktlineString(t, clientConn, "done\n")

				require.NoError(t, clientConn.CloseWrite())

				return nil
			},
			expectedErr: structerr.NewInternal("cmd wait: exit status 128, stderr: %q",
				"fatal: git upload-pack: protocol error, expected to get object ID, not 'want 1111 multi_ack'\n",
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
			expectedResponse: &gitalypb.SSHUploadPackWithSidechannelResponse{
				Bytes:                         int64(154),
				PackfileNegotiationStatistics: &gitalypb.PackfileNegotiationStatistics{},
			},
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
			expectedErr: structerr.NewCanceled("user canceled the fetch"),
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
			expectedErr: structerr.NewInternal("cmd wait: exit status 128, stderr: %q", "fatal: unknown capability 'foobar'\n"),
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
			expectedErr: structerr.NewCanceled("%w", context.Canceled),
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
			expectedErr: structerr.NewCanceled("%w", context.Canceled),
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
			expectedErr: structerr.NewCanceled("%w", context.Canceled),
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
			if tc.expectedResponse != nil && response != nil {
				// The payload size is not deterministic(it's a different value in local-test end ci-test), so we set it to the expected value.
				tc.expectedResponse.PackfileNegotiationStatistics.PayloadSize = response.PackfileNegotiationStatistics.PayloadSize
			}
			testhelper.ProtoEqual(t, tc.expectedResponse, response)
		})
	}
}

func requireFailedSSHStream(t *testing.T, expectedErr error, recv func() (int32, error)) {
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
		testhelper.RequireGrpcError(t, expectedErr, err)
		require.NotEqual(t, 0, code, "exit status")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for SSH stream")
	}
}

func TestUploadPack_validation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)
	client := newSSHClient(t, cfg.SocketPath)

	repo, _ := gittest.CreateRepository(t, ctx, cfg)

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
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryPathNotSet),
		},
		{
			desc: "missing repository",
			request: &gitalypb.SSHUploadPackRequest{
				Repository: nil,
			},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "data in first request",
			request: &gitalypb.SSHUploadPackRequest{
				Repository: repo,
				Stdin:      []byte("Fail"),
			},
			expectedErr: structerr.NewInvalidArgument("non-empty stdin in first request"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
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
	protocolDetectingFactory := gittest.NewProtocolDetectingCommandFactory(t, ctx, cfg)

	cfg.SocketPath = runSSHServerWithOptions(t, cfg, []ServerOpt{
		WithPackfileNegotiationMetrics(negotiationMetrics),
	}, testserver.WithGitCommandFactory(protocolDetectingFactory))

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	smallBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("foobar"))
	largeBlobID := gittest.WriteBlob(t, cfg, repoPath, bytes.Repeat([]byte("1"), 2048))

	// We set up the commits so that HEAD does not reference the above two blobs. If it did we'd
	// fetch the blobs regardless of `--filter=blob:limit`.
	rootCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "small", Mode: "100644", OID: smallBlobID},
		gittest.TreeEntry{Path: "large", Mode: "100644", OID: largeBlobID},
	))
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(rootCommitID), gittest.WithBranch("main"), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "unrelated", Mode: "100644", Content: "something"},
	))
	gittest.WriteTag(t, cfg, repoPath, "v1.0.0", rootCommitID.Revision())

	for _, tc := range []struct {
		desc             string
		request          *gitalypb.SSHUploadPackRequest
		cloneArgs        []string
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
			cloneArgs: []string{
				"--depth=1",
			},
			deepen: 1,
		},
		{
			desc: "shallow clone with protocol v2",
			request: &gitalypb.SSHUploadPackRequest{
				Repository:  repo,
				GitProtocol: git.ProtocolV2,
			},
			cloneArgs: []string{
				"--depth=1",
			},
			deepen:           1,
			expectedProtocol: git.ProtocolV2,
		},
		{
			desc: "partial clone",
			request: &gitalypb.SSHUploadPackRequest{
				Repository: repo,
			},
			cloneArgs: []string{
				"--filter=blob:limit=1024",
			},
			verify: func(t *testing.T, repoPath string) {
				gittest.RequireObjectNotExists(t, cfg, repoPath, largeBlobID)
				gittest.RequireObjectExists(t, cfg, repoPath, smallBlobID)
			},
		},
		{
			desc: "hidden tags",
			cloneArgs: []string{
				"--mirror",
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

			require.NoError(t, runClone(t, ctx, cfg, sidechannel, tc.request,
				append([]string{
					"git@localhost:test/test.git", localRepoPath,
				}, tc.cloneArgs...)...,
			))

			requireRevisionsEqual(t, cfg, repoPath, localRepoPath, "refs/heads/main")

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

	// We're using a custom pack-objects hook for git-upload-pack. In order
	// to assure that it's getting executed as expected, we're writing a
	// custom script which replaces the hook binary. It doesn't do anything
	// special, but writes an error message and errors out and should thus
	// cause the clone to fail with this error message.
	//nolint:gitaly-linters
	testhelper.WriteExecutable(t, cfg.BinaryPath("gitaly-hooks"), []byte(fmt.Sprintf(
		`#!/usr/bin/env bash
		set -eo pipefail
		echo 'I was invoked' >'%s'
		shift
		exec git "$@"
	`, outputPath)))

	cfg.SocketPath = runSSHServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, testhelper.Context(t), cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	localRepoPath := testhelper.TempDir(t)

	require.NoError(t, runClone(t, ctx, cfg, false, &gitalypb.SSHUploadPackRequest{
		Repository: repo,
	}, "git@localhost:test/test.git", localRepoPath))

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

	repo, repoPath := gittest.CreateRepository(t, testhelper.Context(t), cfg)
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	// While Git knows the side-band-64 capability, some other clients don't. There is no way
	// though to have Git not use that capability, so we're instead manually crafting a packfile
	// negotiation without that capability and send it along.
	negotiation := bytes.NewBuffer([]byte{})
	gittest.WritePktlinef(t, negotiation, "want %s multi_ack_detailed thin-pack include-tag ofs-delta agent=git/2.29.1", commitID)
	gittest.WritePktlinef(t, negotiation, "want %s", commitID)
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
	require.Contains(t, string(out), git.DefaultRef.String())
	require.Contains(t, string(out), "Counting objects")
	require.Contains(t, string(out), "PACK")
}

func TestUploadPack_invalidStorage(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	testcfg.BuildGitalySSH(t, cfg)

	repo, _ := gittest.CreateRepository(t, testhelper.Context(t), cfg)

	localRepoPath := testhelper.TempDir(t)

	err := runClone(t, ctx, cfg, false, &gitalypb.SSHUploadPackRequest{
		Repository: &gitalypb.Repository{
			StorageName:  "foobar",
			RelativePath: repo.GetRelativePath(),
		},
	}, "git@localhost:test/test.git", localRepoPath)
	require.Error(t, err)

	require.Contains(t, err.Error(), fmt.Sprintf("rpc error: code = InvalidArgument desc = %s", storage.NewStorageNotFoundError("foobar")))
}

func TestUploadPack_gitFailure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, testhelper.Context(t), cfg)

	client := newSSHClient(t, cfg.SocketPath)

	// Writing an invalid config will allow repo to pass the `ValidateRepository` check but still
	// trigger an error when git tries to access the repo.
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "config"), []byte("Not a valid gitconfig"), perm.SharedFile))

	stream, err := client.SSHUploadPack(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&gitalypb.SSHUploadPackRequest{Repository: repo}))
	require.NoError(t, stream.CloseSend())

	err = recvUntilError(t, stream)
	testhelper.RequireGrpcError(t, structerr.NewInternal(`cmd wait: exit status 128, stderr: "fatal: bad config line 1 in file ./config\n"`), err)
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
