//go:build !gitaly_test_sha256

package smarthttp

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v15/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v15/internal/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	clientCapabilities = `multi_ack_detailed no-done side-band-64k thin-pack include-tag ofs-delta deepen-since deepen-not filter agent=git/2.18.0`
)

type (
	requestMaker func(ctx context.Context, t *testing.T, serverSocketPath, token string, in *gitalypb.PostUploadPackRequest, body io.Reader) (*bytes.Buffer, error)
)

func runTestWithAndWithoutConfigOptions(
	t *testing.T,
	tf func(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option),
	makeRequest requestMaker,
	opts ...testcfg.Option,
) {
	ctx := testhelper.Context(t)

	t.Run("no config options", func(t *testing.T) { tf(t, ctx, makeRequest) })

	if len(opts) > 0 {
		t.Run("with config options", func(t *testing.T) {
			tf(t, ctx, makeRequest, opts...)
		})
	}
}

func TestServer_PostUpload(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUpload, makePostUploadPackRequest, testcfg.WithPackObjectsCacheEnabled())
}

func TestServer_PostUploadWithChannel(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUpload, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUpload(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)

	negotiationMetrics := prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"feature"})
	cfg.SocketPath = runSmartHTTPServer(t, cfg, WithPackfileNegotiationMetrics(negotiationMetrics))

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})
	_, localRepoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	testcfg.BuildGitalyHooks(t, cfg)

	oldCommit, err := git.ObjectHashSHA1.FromHex("1e292f8fedd741b75372e19097c76d327140c312") // refs/heads/master
	require.NoError(t, err)
	newCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"), gittest.WithParents(oldCommit))

	// UploadPack request is a "want" packet line followed by a packet flush, then many "have" packets followed by a packet flush.
	// This is explained a bit in https://git-scm.com/book/en/v2/Git-Internals-Transfer-Protocols#_downloading_data
	requestBuffer := &bytes.Buffer{}
	gittest.WritePktlineString(t, requestBuffer, fmt.Sprintf("want %s %s\n", newCommit, clientCapabilities))
	gittest.WritePktlineFlush(t, requestBuffer)
	gittest.WritePktlineString(t, requestBuffer, fmt.Sprintf("have %s\n", oldCommit))
	gittest.WritePktlineFlush(t, requestBuffer)

	req := &gitalypb.PostUploadPackRequest{Repository: repo}
	responseBuffer, err := makeRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, req, requestBuffer)
	require.NoError(t, err)

	pack, version, entries := extractPackDataFromResponse(t, responseBuffer)
	require.NotEmpty(t, pack, "Expected to find a pack file in response, found none")

	gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: bytes.NewReader(pack)},
		"-C", localRepoPath, "unpack-objects", fmt.Sprintf("--pack_header=%d,%d", version, entries),
	)

	gittest.RequireObjectExists(t, cfg, localRepoPath, newCommit)

	metric, err := negotiationMetrics.GetMetricWithLabelValues("have")
	require.NoError(t, err)
	require.Equal(t, 1.0, promtest.ToFloat64(metric))
}

func TestServer_PostUploadPack_gitConfigOptions(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUploadPackGitConfigOptions, makePostUploadPackRequest, testcfg.WithPackObjectsCacheEnabled())
}

func TestServer_PostUploadPackSidechannel_gitConfigOptions(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUploadPackGitConfigOptions, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUploadPackGitConfigOptions(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	testcfg.BuildGitalyHooks(t, cfg)

	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	// We write two commits: the first commit is a common base commit that is available via
	// normal refs. And the second commit is a child of the base commit, but its reference is
	// created as `refs/hidden/csv`. This allows us to hide this reference and thus verify that
	// the gitconfig indeed is applied because we should not be able to fetch the hidden ref.
	baseID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("base commit"),
		gittest.WithParents(),
		gittest.WithBranch("main"),
	)
	hiddenID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("hidden commit"),
		gittest.WithParents(baseID),
	)
	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/hidden/csv", hiddenID.String())

	requestBody := &bytes.Buffer{}
	gittest.WritePktlineString(t, requestBody, fmt.Sprintf("want %s %s\n", hiddenID, clientCapabilities))
	gittest.WritePktlineFlush(t, requestBody)
	gittest.WritePktlineString(t, requestBody, fmt.Sprintf("have %s\n", baseID))
	gittest.WritePktlineFlush(t, requestBody)

	t.Run("sanity check: ref exists and can be fetched", func(t *testing.T) {
		rpcRequest := &gitalypb.PostUploadPackRequest{Repository: repo}

		response, err := makeRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, rpcRequest, bytes.NewReader(requestBody.Bytes()))
		require.NoError(t, err)
		_, _, count := extractPackDataFromResponse(t, response)
		require.Equal(t, 1, count, "pack should have the hidden ID as single object")
	})

	t.Run("failing request because of hidden ref config", func(t *testing.T) {
		rpcRequest := &gitalypb.PostUploadPackRequest{
			Repository: repo,
			GitConfigOptions: []string{
				"uploadpack.hideRefs=refs/hidden",
				"uploadpack.allowAnySHA1InWant=false",
			},
		}
		response, err := makeRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, rpcRequest, bytes.NewReader(requestBody.Bytes()))
		testhelper.RequireGrpcCode(t, err, codes.Unavailable)

		// The failure message proves that upload-pack failed because of
		// GitConfigOptions, and that proves that passing GitConfigOptions works.
		expected := fmt.Sprintf("0049ERR upload-pack: not our ref %v", hiddenID)
		require.Equal(t, expected, response.String(), "Ref is hidden, expected error message did not appear")
	})
}

func TestServer_PostUploadPack_gitProtocol(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUploadPackGitProtocol, makePostUploadPackRequest, testcfg.WithPackObjectsCacheEnabled())
}

func TestServer_PostUploadPackWithSidechannel_gitProtocol(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUploadPackGitProtocol, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUploadPackGitProtocol(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	protocolDetectingFactory := gittest.NewProtocolDetectingCommandFactory(ctx, t, cfg)
	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithGitCommandFactory(protocolDetectingFactory),
	})
	cfg.SocketPath = server.Address()

	repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	// command=ls-refs does not exist in protocol v0, so if this succeeds, we're talking v2
	requestBody := &bytes.Buffer{}
	gittest.WritePktlineString(t, requestBody, "command=ls-refs\n")
	gittest.WritePktlineDelim(t, requestBody)
	gittest.WritePktlineString(t, requestBody, "peel\n")
	gittest.WritePktlineString(t, requestBody, "symrefs\n")
	gittest.WritePktlineFlush(t, requestBody)

	rpcRequest := &gitalypb.PostUploadPackRequest{
		Repository:  repo,
		GitProtocol: git.ProtocolV2,
	}

	_, err := makeRequest(ctx, t, server.Address(), cfg.Auth.Token, rpcRequest, requestBody)
	require.NoError(t, err)

	envData := protocolDetectingFactory.ReadProtocol(t)
	require.Equal(t, fmt.Sprintf("GIT_PROTOCOL=%s\n", git.ProtocolV2), envData)
}

// This test is here because git-upload-pack returns a non-zero exit code
// on 'deepen' requests even though the request is being handled just
// fine from the client perspective.
func TestServer_PostUploadPack_suppressDeepenExitError(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUploadPackSuppressDeepenExitError, makePostUploadPackRequest, testcfg.WithPackObjectsCacheEnabled())
}

func TestServer_PostUploadPackWithSidechannel_suppressDeepenExitError(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUploadPackSuppressDeepenExitError, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUploadPackSuppressDeepenExitError(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	requestBody := &bytes.Buffer{}
	gittest.WritePktlineString(t, requestBody, fmt.Sprintf("want e63f41fe459e62e1228fcef60d7189127aeba95a %s\n", clientCapabilities))
	gittest.WritePktlineString(t, requestBody, "deepen 1")
	gittest.WritePktlineFlush(t, requestBody)

	rpcRequest := &gitalypb.PostUploadPackRequest{Repository: repo}
	response, err := makeRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, rpcRequest, requestBody)

	// This assertion is the main reason this test exists.
	assert.NoError(t, err)
	assert.Equal(t, `0034shallow e63f41fe459e62e1228fcef60d7189127aeba95a0000`, response.String())
}

func TestServer_PostUploadPack_usesPackObjectsHook(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	testServerPostUploadPackUsesPackObjectsHook(t, ctx, makePostUploadPackRequest)
}

func TestServer_PostUploadPackWithSidechannel_usesPackObjectsHook(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	testServerPostUploadPackUsesPackObjectsHook(t, ctx, makePostUploadPackWithSidechannelRequest)
}

func testServerPostUploadPackUsesPackObjectsHook(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, append(opts, testcfg.WithPackObjectsCacheEnabled())...)
	cfg.BinDir = testhelper.TempDir(t)

	outputPath := filepath.Join(cfg.BinDir, "output")
	hookScript := fmt.Sprintf("#!/bin/sh\necho 'I was invoked' >'%s'\nshift\nexec git \"$@\"\n", outputPath)

	// We're using a custom pack-objects hook for git-upload-pack. In order
	// to assure that it's getting executed as expected, we're writing a
	// custom script which replaces the hook binary. It doesn't do anything
	// special, but writes a message into a status file and then errors
	// out. In the best case we'd have just printed the error to stderr and
	// check the return error message. But it's unfortunately not
	// transferred back.
	testhelper.WriteExecutable(t, cfg.BinaryPath("gitaly-hooks"), []byte(hookScript))

	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	oldHead := bytes.TrimSpace(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "master~"))
	newHead := bytes.TrimSpace(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "master"))

	requestBuffer := &bytes.Buffer{}
	gittest.WritePktlineString(t, requestBuffer, fmt.Sprintf("want %s %s\n", newHead, clientCapabilities))
	gittest.WritePktlineFlush(t, requestBuffer)
	gittest.WritePktlineString(t, requestBuffer, fmt.Sprintf("have %s\n", oldHead))
	gittest.WritePktlineFlush(t, requestBuffer)

	_, err := makeRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, &gitalypb.PostUploadPackRequest{
		Repository: repo,
	}, requestBuffer)
	require.NoError(t, err)

	contents := testhelper.MustReadFile(t, outputPath)
	require.Equal(t, "I was invoked\n", string(contents))
}

func TestServer_PostUploadPack_validation(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUploadPackValidation, makePostUploadPackRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUploadPackValidation(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	serverSocketPath := runSmartHTTPServer(t, cfg)

	for _, tc := range []struct {
		desc    string
		request *gitalypb.PostUploadPackRequest
		code    codes.Code
	}{
		{
			desc: "Repository doesn't exist",
			request: &gitalypb.PostUploadPackRequest{
				Repository: &gitalypb.Repository{StorageName: "fake", RelativePath: "path"},
			},
			code: codes.InvalidArgument,
		},
		{
			desc:    "Repository is nil",
			request: &gitalypb.PostUploadPackRequest{Repository: nil},
			code:    codes.InvalidArgument,
		},
		{
			desc: "Data exists on first request",
			request: &gitalypb.PostUploadPackRequest{
				Repository: &gitalypb.Repository{
					StorageName:  cfg.Storages[0].Name,
					RelativePath: "path/to/repo",
				},
				Data: []byte("Fail"),
			},
			code: func() codes.Code {
				if testhelper.IsPraefectEnabled() {
					return codes.NotFound
				}

				return codes.InvalidArgument
			}(),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := makeRequest(ctx, t, serverSocketPath, cfg.Auth.Token, tc.request, bytes.NewBuffer(nil))
			testhelper.RequireGrpcCode(t, err, tc.code)
		})
	}
}

func TestServer_PostUploadPackSidechannel_validation(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUploadPackWithSideChannelValidation, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUploadPackWithSideChannelValidation(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	serverSocketPath := runSmartHTTPServer(t, cfg)

	rpcRequests := []*gitalypb.PostUploadPackRequest{
		{Repository: &gitalypb.Repository{StorageName: "fake", RelativePath: "path"}}, // Repository doesn't exist
		{Repository: nil}, // Repository is nil
	}

	for _, rpcRequest := range rpcRequests {
		t.Run(fmt.Sprintf("%v", rpcRequest), func(t *testing.T) {
			_, err := makeRequest(ctx, t, serverSocketPath, cfg.Auth.Token, rpcRequest, bytes.NewBuffer(nil))
			testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
		})
	}
}

// The response contains bunch of things; metadata, progress messages, and a pack file. We're only
// interested in the pack file and its header values.
func extractPackDataFromResponse(t *testing.T, buf *bytes.Buffer) ([]byte, int, int) {
	var pack []byte

	// The response should have the following format.
	// PKT-LINE
	// PKT-LINE
	// ...
	// 0000
	scanner := pktline.NewScanner(buf)
	for scanner.Scan() {
		pkt := scanner.Bytes()
		if pktline.IsFlush(pkt) {
			break
		}

		// The first data byte of the packet is the band designator. We only care about data in band 1.
		if data := pktline.Data(pkt); len(data) > 0 && data[0] == 1 {
			pack = append(pack, data[1:]...)
		}
	}

	require.NoError(t, scanner.Err())
	require.NotEmpty(t, pack, "pack data should not be empty")

	// The packet is structured as follows:
	// 4 bytes for signature, here it's "PACK"
	// 4 bytes for header version
	// 4 bytes for header entries
	// The rest is the pack file
	require.Equal(t, "PACK", string(pack[:4]), "Invalid packet signature")
	version := int(binary.BigEndian.Uint32(pack[4:8]))
	entries := int(binary.BigEndian.Uint32(pack[8:12]))
	pack = pack[12:]

	return pack, version, entries
}

func TestServer_PostUploadPack_partialClone(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUploadPackPartialClone, makePostUploadPackRequest, testcfg.WithPackObjectsCacheEnabled())
}

func TestServer_PostUploadPackWithSidechannel_partialClone(t *testing.T) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, testServerPostUploadPackPartialClone, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUploadPackPartialClone(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)

	negotiationMetrics := prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"feature"})
	cfg.SocketPath = runSmartHTTPServer(t, cfg, WithPackfileNegotiationMetrics(negotiationMetrics))

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})
	_, localRepoPath := gittest.CreateRepository(ctx, t, cfg)

	testcfg.BuildGitalyHooks(t, cfg)

	oldCommit, err := git.ObjectHashSHA1.FromHex("1e292f8fedd741b75372e19097c76d327140c312") // refs/heads/master
	require.NoError(t, err)
	newCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"), gittest.WithParents(oldCommit))

	var requestBuffer bytes.Buffer
	gittest.WritePktlineString(t, &requestBuffer, fmt.Sprintf("want %s %s\n", newCommit, clientCapabilities))
	gittest.WritePktlineString(t, &requestBuffer, fmt.Sprintf("filter %s\n", "blob:limit=200"))
	gittest.WritePktlineFlush(t, &requestBuffer)
	gittest.WritePktlineString(t, &requestBuffer, "done\n")
	gittest.WritePktlineFlush(t, &requestBuffer)

	req := &gitalypb.PostUploadPackRequest{Repository: repo}
	responseBuffer, err := makeRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, req, &requestBuffer)
	require.NoError(t, err)

	pack, version, entries := extractPackDataFromResponse(t, responseBuffer)
	require.NotEmpty(t, pack, "Expected to find a pack file in response, found none")

	gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: bytes.NewReader(pack)},
		"-C", localRepoPath, "unpack-objects", fmt.Sprintf("--pack_header=%d,%d", version, entries),
	)

	// a4a132b1b0d6720ca9254440a7ba8a6b9bbd69ec is README.md, which is a small file
	blobLessThanLimit := git.ObjectID("a4a132b1b0d6720ca9254440a7ba8a6b9bbd69ec")

	// c1788657b95998a2f177a4f86d68a60f2a80117f is CONTRIBUTING.md, which is > 200 bytese
	blobGreaterThanLimit := git.ObjectID("c1788657b95998a2f177a4f86d68a60f2a80117f")

	gittest.RequireObjectExists(t, cfg, localRepoPath, blobLessThanLimit)
	gittest.RequireObjectExists(t, cfg, repoPath, blobGreaterThanLimit)
	gittest.RequireObjectNotExists(t, cfg, localRepoPath, blobGreaterThanLimit)

	metric, err := negotiationMetrics.GetMetricWithLabelValues("filter")
	require.NoError(t, err)
	require.Equal(t, 1.0, promtest.ToFloat64(metric))
}

func TestServer_PostUploadPack_allowAnySHA1InWant(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	testServerPostUploadPackAllowAnySHA1InWant(t, ctx, makePostUploadPackRequest)
}

func TestServer_PostUploadPackWithSidechannel_allowAnySHA1InWant(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	testServerPostUploadPackAllowAnySHA1InWant(t, ctx, makePostUploadPackWithSidechannelRequest)
}

func testServerPostUploadPackAllowAnySHA1InWant(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})
	_, localRepoPath := gittest.CreateRepository(ctx, t, cfg)

	testcfg.BuildGitalyHooks(t, cfg)
	newCommit := gittest.WriteCommit(t, cfg, repoPath)

	var requestBuffer bytes.Buffer
	gittest.WritePktlineString(t, &requestBuffer, fmt.Sprintf("want %s %s\n", newCommit, clientCapabilities))
	gittest.WritePktlineFlush(t, &requestBuffer)
	gittest.WritePktlineString(t, &requestBuffer, "done\n")
	gittest.WritePktlineFlush(t, &requestBuffer)

	req := &gitalypb.PostUploadPackRequest{Repository: repo}
	responseBuffer, err := makeRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, req, &requestBuffer)
	require.NoError(t, err)

	pack, version, entries := extractPackDataFromResponse(t, responseBuffer)
	require.NotEmpty(t, pack, "Expected to find a pack file in response, found none")

	gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: bytes.NewReader(pack)},
		"-C", localRepoPath, "unpack-objects", fmt.Sprintf("--pack_header=%d,%d", version, entries),
	)

	gittest.RequireObjectExists(t, cfg, localRepoPath, newCommit)
}

func makePostUploadPackRequest(ctx context.Context, t *testing.T, serverSocketPath, token string, in *gitalypb.PostUploadPackRequest, body io.Reader) (*bytes.Buffer, error) {
	client, conn := newSmartHTTPClient(t, serverSocketPath, token)
	defer conn.Close()

	stream, err := client.PostUploadPack(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(in))

	if body != nil {
		sw := streamio.NewWriter(func(p []byte) error {
			return stream.Send(&gitalypb.PostUploadPackRequest{Data: p})
		})

		_, err = io.Copy(sw, body)
		require.NoError(t, err)
		require.NoError(t, stream.CloseSend())
	}

	responseBuffer := &bytes.Buffer{}
	rr := streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetData(), err
	})
	_, err = io.Copy(responseBuffer, rr)

	return responseBuffer, err
}

func dialSmartHTTPServerWithSidechannel(t *testing.T, serverSocketPath, token string, registry *sidechannel.Registry) *grpc.ClientConn {
	t.Helper()

	clientHandshaker := sidechannel.NewClientHandshaker(testhelper.NewDiscardingLogEntry(t), registry)
	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(clientHandshaker.ClientHandshake(insecure.NewCredentials())),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(token)),
	}

	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	require.NoError(t, err)

	return conn
}

func makePostUploadPackWithSidechannelRequest(ctx context.Context, t *testing.T, serverSocketPath, token string, in *gitalypb.PostUploadPackRequest, body io.Reader) (*bytes.Buffer, error) {
	t.Helper()

	registry := sidechannel.NewRegistry()
	conn := dialSmartHTTPServerWithSidechannel(t, serverSocketPath, token, registry)
	client := gitalypb.NewSmartHTTPServiceClient(conn)
	defer testhelper.MustClose(t, conn)

	responseBuffer := &bytes.Buffer{}
	ctxOut, waiter := sidechannel.RegisterSidechannel(ctx, registry, func(sideConn *sidechannel.ClientConn) error {
		var wg sync.WaitGroup
		defer wg.Wait()

		wg.Add(1)
		errC := make(chan error, 1)
		go func() {
			defer wg.Done()
			_, err := io.Copy(responseBuffer, sideConn)
			errC <- err
		}()

		if body != nil {
			if _, err := io.Copy(sideConn, body); err != nil {
				return err
			}
		}

		if err := sideConn.CloseWrite(); err != nil {
			return err
		}

		return <-errC
	})
	defer waiter.Close()

	rpcRequest := &gitalypb.PostUploadPackWithSidechannelRequest{
		Repository:       in.GetRepository(),
		GitConfigOptions: in.GetGitConfigOptions(),
		GitProtocol:      in.GetGitProtocol(),
	}
	_, err := client.PostUploadPackWithSidechannel(ctxOut, rpcRequest)
	if err == nil {
		require.NoError(t, waiter.Close())
	}

	return responseBuffer, err
}
