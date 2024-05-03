package smarthttp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v16/auth"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bundleuri"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	clientCapabilities = `multi_ack_detailed no-done side-band-64k thin-pack include-tag ofs-delta deepen-since deepen-not filter agent=git/2.18.0`
)

type (
	requestMaker func(t *testing.T, ctx context.Context, serverSocketPath, token string, in *gitalypb.PostUploadPackWithSidechannelRequest, body io.Reader) (*bytes.Buffer, error)
)

func runTestWithAndWithoutConfigOptions(
	t *testing.T,
	ctx context.Context,
	tf func(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option),
	makeRequest requestMaker,
	opts ...testcfg.Option,
) {
	t.Run("no config options", func(t *testing.T) { tf(t, ctx, makeRequest) })

	if len(opts) > 0 {
		t.Run("with config options", func(t *testing.T) {
			tf(t, ctx, makeRequest, opts...)
		})
	}
}

func TestServer_PostUploadWithChannel(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testServerPostUploadWithChannel)
}

func testServerPostUploadWithChannel(t *testing.T, ctx context.Context) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, ctx, testServerPostUpload, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUpload(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	testcfg.BuildGitalyHooks(t, cfg)

	negotiationMetrics := prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"feature"})
	cfg.SocketPath = runSmartHTTPServer(t, cfg, WithPackfileNegotiationMetrics(negotiationMetrics))

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	_, localRepoPath := gittest.CreateRepository(t, ctx, cfg)

	oldCommit := gittest.WriteCommit(t, cfg, repoPath)
	newCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"), gittest.WithParents(oldCommit))

	// UploadPack request is a "want" packet line followed by a packet flush, then many "have" packets followed by a packet flush.
	// This is explained a bit in https://git-scm.com/book/en/v2/Git-Internals-Transfer-Protocols#_downloading_data
	requestBuffer := &bytes.Buffer{}
	gittest.WritePktlineString(t, requestBuffer, fmt.Sprintf("want %s %s\n", newCommit, clientCapabilities))
	gittest.WritePktlineFlush(t, requestBuffer)
	gittest.WritePktlineString(t, requestBuffer, fmt.Sprintf("have %s\n", oldCommit))
	gittest.WritePktlineFlush(t, requestBuffer)

	req := &gitalypb.PostUploadPackWithSidechannelRequest{Repository: repo}
	responseBuffer, err := makeRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, req, requestBuffer)
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

func TestServer_PostUploadPackSidechannel_gitConfigOptions(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testServerPostUploadPackSidechannelGitConfigOptions)
}

func testServerPostUploadPackSidechannelGitConfigOptions(t *testing.T, ctx context.Context) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, ctx, testServerPostUploadPackGitConfigOptions, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUploadPackGitConfigOptions(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	testcfg.BuildGitalyHooks(t, cfg)

	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	// We write two commits: the first commit is a common base commit that is available via
	// normal refs. And the second commit is a child of the base commit, but its reference is
	// created as `refs/hidden/csv`. This allows us to hide this reference and thus verify that
	// the gitconfig indeed is applied because we should not be able to fetch the hidden ref.
	baseID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("base commit"),
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
		rpcRequest := &gitalypb.PostUploadPackWithSidechannelRequest{Repository: repo}

		response, err := makeRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, rpcRequest, bytes.NewReader(requestBody.Bytes()))
		require.NoError(t, err)
		_, _, count := extractPackDataFromResponse(t, response)
		require.Equal(t, 1, count, "pack should have the hidden ID as single object")
	})

	t.Run("failing request because of hidden ref config", func(t *testing.T) {
		rpcRequest := &gitalypb.PostUploadPackWithSidechannelRequest{
			Repository: repo,
			GitConfigOptions: []string{
				"uploadpack.hideRefs=refs/hidden",
				"uploadpack.allowAnySHA1InWant=false",
			},
		}
		response, err := makeRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, rpcRequest, bytes.NewReader(requestBody.Bytes()))
		testhelper.RequireGrpcError(t, structerr.NewFailedPrecondition("running upload-pack: waiting for upload-pack: exit status 128"), err)

		// The failure message proves that upload-pack failed because of
		// GitConfigOptions, and that proves that passing GitConfigOptions works.
		require.Equal(t,
			gittest.Pktlinef(t, "ERR upload-pack: not our ref %v", hiddenID),
			response.String(),
		)
	})
}

func TestServer_PostUploadPackWithSidechannel_gitProtocol(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testServerPostUploadPackWithSidechannelGitProtocol)
}

func testServerPostUploadPackWithSidechannelGitProtocol(t *testing.T, ctx context.Context) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, ctx, testServerPostUploadPackGitProtocol, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUploadPackGitProtocol(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	protocolDetectingFactory := gittest.NewProtocolDetectingCommandFactory(t, ctx, cfg)
	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithGitCommandFactory(protocolDetectingFactory),
	})
	cfg.SocketPath = server.Address()

	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	// command=ls-refs does not exist in protocol v0, so if this succeeds, we're talking v2
	requestBody := &bytes.Buffer{}
	gittest.WritePktlineString(t, requestBody, "command=ls-refs\n")
	gittest.WritePktlineString(t, requestBody, fmt.Sprintf("object-format=%s\n", gittest.DefaultObjectHash.Format))
	gittest.WritePktlineDelim(t, requestBody)
	gittest.WritePktlineString(t, requestBody, "peel\n")
	gittest.WritePktlineString(t, requestBody, "symrefs\n")
	gittest.WritePktlineFlush(t, requestBody)

	rpcRequest := &gitalypb.PostUploadPackWithSidechannelRequest{
		Repository:  repo,
		GitProtocol: git.ProtocolV2,
	}

	_, err := makeRequest(t, ctx, server.Address(), cfg.Auth.Token, rpcRequest, requestBody)
	require.NoError(t, err)

	envData := protocolDetectingFactory.ReadProtocol(t)
	require.Equal(t, fmt.Sprintf("GIT_PROTOCOL=%s\n", git.ProtocolV2), envData)
}

func TestServer_PostUploadPackWithSidechannel_suppressDeepenExitError(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testServerPostUploadPackWithSidechannelSuppressDeepenExitError)
}

func testServerPostUploadPackWithSidechannelSuppressDeepenExitError(t *testing.T, ctx context.Context) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, ctx, testServerPostUploadPackSuppressDeepenExitError, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUploadPackSuppressDeepenExitError(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	commitID := gittest.WriteCommit(t, cfg, repoPath)

	var requestBody bytes.Buffer
	gittest.WritePktlineString(t, &requestBody, fmt.Sprintf("want %s %s\n", commitID, clientCapabilities))
	gittest.WritePktlineString(t, &requestBody, "deepen 1")
	gittest.WritePktlineFlush(t, &requestBody)

	rpcRequest := &gitalypb.PostUploadPackWithSidechannelRequest{Repository: repo}
	response, err := makeRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, rpcRequest, &requestBody)
	require.NoError(t, err)
	require.Equal(t, gittest.Pktlinef(t, "shallow %s", commitID)+"0000", response.String())
}

func TestServer_PostUploadPackWithSidechannel_usesPackObjectsHook(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testServerPostUploadPackWithSidechannelUsesPackObjectsHook)
}

func testServerPostUploadPackWithSidechannelUsesPackObjectsHook(t *testing.T, ctx context.Context) {
	t.Parallel()

	testServerPostUploadPackUsesPackObjectsHook(t, ctx, makePostUploadPackWithSidechannelRequest)
}

func testServerPostUploadPackUsesPackObjectsHook(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, append(opts, testcfg.WithPackObjectsCacheEnabled())...)
	cfg.BinDir = testhelper.TempDir(t)

	outputPath := filepath.Join(cfg.BinDir, "output")
	//nolint:gitaly-linters
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

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	oldHead := gittest.WriteCommit(t, cfg, repoPath)
	newHead := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(oldHead), gittest.WithBranch("master"))

	var requestBuffer bytes.Buffer
	gittest.WritePktlineString(t, &requestBuffer, fmt.Sprintf("want %s %s\n", newHead, clientCapabilities))
	gittest.WritePktlineFlush(t, &requestBuffer)
	gittest.WritePktlineString(t, &requestBuffer, fmt.Sprintf("have %s\n", oldHead))
	gittest.WritePktlineFlush(t, &requestBuffer)

	_, err := makeRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, &gitalypb.PostUploadPackWithSidechannelRequest{
		Repository: repo,
	}, &requestBuffer)
	require.NoError(t, err)

	contents := testhelper.MustReadFile(t, outputPath)
	require.Equal(t, "I was invoked\n", string(contents))
}

func TestServer_PostUploadPack_validation(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testServerPostUploadPackValidation)
}

func testServerPostUploadPackValidation(t *testing.T, ctx context.Context) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, ctx, testServerPostUploadPackValidationRequest, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUploadPackValidationRequest(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	t.Parallel()

	cfg := testcfg.Build(t, opts...)
	serverSocketPath := runSmartHTTPServer(t, cfg)
	cfg.SocketPath = serverSocketPath

	for _, tc := range []struct {
		desc        string
		request     *gitalypb.PostUploadPackWithSidechannelRequest
		expectedErr error
	}{
		{
			desc: "nonexistent repository",
			request: &gitalypb.PostUploadPackWithSidechannelRequest{
				Repository: &gitalypb.Repository{StorageName: "fake", RelativePath: "path"},
			},
			expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
				"%w", storage.NewStorageNotFoundError("fake"),
			)),
		},
		{
			desc:        "unset repository",
			request:     &gitalypb.PostUploadPackWithSidechannelRequest{Repository: nil},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := makeRequest(t, ctx, serverSocketPath, cfg.Auth.Token, tc.request, bytes.NewBuffer(nil))
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func TestServer_PostUploadPackSidechannel_validation(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testServerPostUploadPackSidechannelValidation)
}

func testServerPostUploadPackSidechannelValidation(t *testing.T, ctx context.Context) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, ctx, testServerPostUploadPackWithSideChannelValidation, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func TestServer_PostUploadPackWithBundleURI(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx = featureflag.ContextWithFeatureFlag(ctx, featureflag.BundleURI, true)

	tempDir := testhelper.TempDir(t)
	keyFile, err := os.Create(filepath.Join(tempDir, "secret.key"))
	require.NoError(t, err)
	_, err = keyFile.WriteString("super-secret-key")
	require.NoError(t, err)
	require.NoError(t, keyFile.Close())

	baseURL := "https://example.com"

	testCases := []struct {
		desc            string
		sinkURI         string
		setup           func(t *testing.T, ctx context.Context, cfg config.Cfg, sink *bundleuri.Sink, repoProto *gitalypb.Repository, repoPath string)
		expectBundleURI bool
	}{
		{
			desc:    "no backup sink",
			sinkURI: "",
		},
		{
			desc:    "no bundle",
			sinkURI: "mem://bundleuri",
		},
		{
			desc:    "broken URL signing",
			sinkURI: "mem://bundleuri",
			setup: func(t *testing.T, ctx context.Context, cfg config.Cfg, sink *bundleuri.Sink, repoProto *gitalypb.Repository, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "README", Content: "much"}),
					gittest.WithBranch("main"))

				repo := localrepo.NewTestRepo(t, cfg, repoProto)
				require.NoError(t, sink.Generate(ctx, repo))
			},
		},
		{
			desc:    "valid bundle",
			sinkURI: "file://" + testhelper.TempDir(t) + "?base_url=" + baseURL + "&no_tmp_dir=true&secret_key_path=" + keyFile.Name(),
			setup: func(t *testing.T, ctx context.Context, cfg config.Cfg, sink *bundleuri.Sink, repoProto *gitalypb.Repository, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "README", Content: "much"}),
					gittest.WithBranch("main"))

				repo := localrepo.NewTestRepo(t, cfg, repoProto)
				require.NoError(t, sink.Generate(ctx, repo))
			},
			expectBundleURI: true,
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			testhelper.SkipQuarantinedTest(
				t,
				"https://gitlab.com/gitlab-org/gitaly/-/issues/5982",
				"TestServer_PostUploadPackWithBundleURI/backup_without_bundle_path",
			)

			t.Parallel()

			var sink *bundleuri.Sink
			var err error
			if tc.sinkURI != "" {
				sink, err = bundleuri.NewSink(ctx, tc.sinkURI)
				require.NoError(t, err)
			}

			cfg := testcfg.Build(t)
			logger := testhelper.NewLogger(t)
			hook := testhelper.AddLoggerHook(logger)
			server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
				testserver.WithBundleURISink(sink),
				testserver.WithLogger(logger),
			})
			cfg.SocketPath = server.Address()

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
			if tc.setup != nil {
				tc.setup(t, ctx, cfg, sink, repoProto, repoPath)
			}

			requestBody := &bytes.Buffer{}
			gittest.WritePktlineString(t, requestBody, "command=bundle-uri\n")
			gittest.WritePktlineString(t, requestBody, fmt.Sprintf("object-format=%s\n", gittest.DefaultObjectHash.Format))
			gittest.WritePktlineFlush(t, requestBody)

			hook.Reset()
			req := &gitalypb.PostUploadPackWithSidechannelRequest{Repository: repoProto, GitProtocol: git.ProtocolV2}
			responseBuffer, err := makePostUploadPackWithSidechannelRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, req, requestBody)
			require.NoError(t, err)

			server.Shutdown()

			var logEntry *logrus.Entry
			for _, e := range hook.AllEntries() {
				if strings.HasPrefix(e.Message, "finished unary call") {
					logEntry = e
					break
				}
			}
			require.NotNil(t, logEntry)
			require.Equal(t, "finished unary call with code OK", logEntry.Message)

			bundleURI, ok := logEntry.Data["bundle_uri"]

			if tc.expectBundleURI {
				require.Contains(t, responseBuffer.String(), "bundle.default.uri="+baseURL)
				require.True(t, ok)
				require.True(t, bundleURI.(bool))
			} else {
				require.False(t, ok)
				require.NotContains(t, responseBuffer.String(), "bundle.default.uri")
			}
		})
	}
}

func testServerPostUploadPackWithSideChannelValidation(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	serverSocketPath := runSmartHTTPServer(t, cfg)

	for _, tc := range []struct {
		desc        string
		req         *gitalypb.PostUploadPackWithSidechannelRequest
		expectedErr error
	}{
		{
			desc: "Repository doesn't exist",
			req:  &gitalypb.PostUploadPackWithSidechannelRequest{Repository: &gitalypb.Repository{StorageName: "fake", RelativePath: "path"}},
			expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
				"%w", storage.NewStorageNotFoundError("fake"),
			)),
		},
		{
			desc:        "Repository no provided",
			req:         &gitalypb.PostUploadPackWithSidechannelRequest{Repository: nil},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := makeRequest(t, ctx, serverSocketPath, cfg.Auth.Token, tc.req, bytes.NewBuffer(nil))
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
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

func TestServer_PostUploadPackWithSidechannel_partialClone(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testServerPostUploadPackWithSidechannelPartialClone)
}

func testServerPostUploadPackWithSidechannelPartialClone(t *testing.T, ctx context.Context) {
	t.Parallel()

	runTestWithAndWithoutConfigOptions(t, ctx, testServerPostUploadPackPartialClone, makePostUploadPackWithSidechannelRequest, testcfg.WithPackObjectsCacheEnabled())
}

func testServerPostUploadPackPartialClone(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	testcfg.BuildGitalyHooks(t, cfg)

	negotiationMetrics := prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"feature"})
	cfg.SocketPath = runSmartHTTPServer(t, cfg, WithPackfileNegotiationMetrics(negotiationMetrics))

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	_, localRepoPath := gittest.CreateRepository(t, ctx, cfg)

	blobLessThanLimit := gittest.WriteBlob(t, cfg, repoPath, bytes.Repeat([]byte{1}, 100))
	blobGreaterThanLimit := gittest.WriteBlob(t, cfg, repoPath, bytes.Repeat([]byte{1}, 1000))

	oldCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "small", Mode: "100644", OID: blobLessThanLimit},
		gittest.TreeEntry{Path: "large", Mode: "100644", OID: blobGreaterThanLimit},
	))
	newCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"), gittest.WithParents(oldCommit))

	var requestBuffer bytes.Buffer
	gittest.WritePktlineString(t, &requestBuffer, fmt.Sprintf("want %s %s\n", newCommit, clientCapabilities))
	gittest.WritePktlineString(t, &requestBuffer, fmt.Sprintf("filter %s\n", "blob:limit=200"))
	gittest.WritePktlineFlush(t, &requestBuffer)
	gittest.WritePktlineString(t, &requestBuffer, "done\n")
	gittest.WritePktlineFlush(t, &requestBuffer)

	req := &gitalypb.PostUploadPackWithSidechannelRequest{Repository: repo}
	responseBuffer, err := makeRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, req, &requestBuffer)
	require.NoError(t, err)

	pack, version, entries := extractPackDataFromResponse(t, responseBuffer)
	require.NotEmpty(t, pack, "Expected to find a pack file in response, found none")

	gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: bytes.NewReader(pack)},
		"-C", localRepoPath, "unpack-objects", fmt.Sprintf("--pack_header=%d,%d", version, entries),
	)

	gittest.RequireObjectExists(t, cfg, localRepoPath, blobLessThanLimit)
	gittest.RequireObjectExists(t, cfg, repoPath, blobGreaterThanLimit)
	gittest.RequireObjectNotExists(t, cfg, localRepoPath, blobGreaterThanLimit)

	metric, err := negotiationMetrics.GetMetricWithLabelValues("filter")
	require.NoError(t, err)
	require.Equal(t, 1.0, promtest.ToFloat64(metric))
}

func TestServer_PostUploadPackWithSidechannel_allowAnySHA1InWant(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testServerPostUploadPackWithSidechannelAllowAnySHA1InWant)
}

func testServerPostUploadPackWithSidechannelAllowAnySHA1InWant(t *testing.T, ctx context.Context) {
	t.Parallel()

	testServerPostUploadPackAllowAnySHA1InWant(t, ctx, makePostUploadPackWithSidechannelRequest)
}

func testServerPostUploadPackAllowAnySHA1InWant(t *testing.T, ctx context.Context, makeRequest requestMaker, opts ...testcfg.Option) {
	cfg := testcfg.Build(t, opts...)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	_, localRepoPath := gittest.CreateRepository(t, ctx, cfg)

	testcfg.BuildGitalyHooks(t, cfg)
	newCommit := gittest.WriteCommit(t, cfg, repoPath)

	var requestBuffer bytes.Buffer
	gittest.WritePktlineString(t, &requestBuffer, fmt.Sprintf("want %s %s\n", newCommit, clientCapabilities))
	gittest.WritePktlineFlush(t, &requestBuffer)
	gittest.WritePktlineString(t, &requestBuffer, "done\n")
	gittest.WritePktlineFlush(t, &requestBuffer)

	req := &gitalypb.PostUploadPackWithSidechannelRequest{Repository: repo}
	responseBuffer, err := makeRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, req, &requestBuffer)
	require.NoError(t, err)

	pack, version, entries := extractPackDataFromResponse(t, responseBuffer)
	require.NotEmpty(t, pack, "Expected to find a pack file in response, found none")

	gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: bytes.NewReader(pack)},
		"-C", localRepoPath, "unpack-objects", fmt.Sprintf("--pack_header=%d,%d", version, entries),
	)

	gittest.RequireObjectExists(t, cfg, localRepoPath, newCommit)
}

func dialSmartHTTPServerWithSidechannel(t *testing.T, serverSocketPath, token string, registry *sidechannel.Registry) *grpc.ClientConn {
	t.Helper()

	clientHandshaker := sidechannel.NewClientHandshaker(testhelper.SharedLogger(t), registry)
	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(clientHandshaker.ClientHandshake(insecure.NewCredentials())),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(token)),
	}

	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	require.NoError(t, err)

	return conn
}

func makePostUploadPackWithSidechannelRequest(t *testing.T, ctx context.Context, serverSocketPath, token string, in *gitalypb.PostUploadPackWithSidechannelRequest, body io.Reader) (*bytes.Buffer, error) {
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

	rpcRequest := &gitalypb.PostUploadPackWithSidechannelRequest{
		Repository:       in.GetRepository(),
		GitConfigOptions: in.GetGitConfigOptions(),
		GitProtocol:      in.GetGitProtocol(),
	}
	_, err := client.PostUploadPackWithSidechannel(ctxOut, rpcRequest)
	if err == nil {
		testhelper.MustClose(t, waiter)
	} else if err := waiter.Close(); err != nil && !errors.Is(err, sidechannel.ErrCallbackDidNotRun) {
		// When the request failed the sidechannel may not even have been used, so we need
		// to catch the `ErrCallbackDidNotRun` error here.
		require.NoError(t, err)
	}

	return responseBuffer, err
}
