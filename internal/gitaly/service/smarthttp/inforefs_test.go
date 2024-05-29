package smarthttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

func TestInfoRefsUploadPack_successful(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testInfoRefsUploadPackSuccessful)
}

func testInfoRefsUploadPackSuccessful(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())
	tagID := gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	rpcRequest := &gitalypb.InfoRefsRequest{Repository: repo}
	response, err := makeInfoRefsUploadPackRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, rpcRequest)
	require.NoError(t, err)
	requireAdvertisedRefs(t, string(response), "git-upload-pack", []string{
		commitID.String() + " HEAD",
		commitID.String() + " refs/heads/main\n",
		tagID.String() + " refs/tags/v1.0.0\n",
		commitID.String() + " refs/tags/v1.0.0^{}\n",
	})
}

func TestInfoRefsUploadPack_internalRefs(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testInfoRefsUploadPackInternalRefs)
}

func testInfoRefsUploadPackInternalRefs(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	for _, tc := range []struct {
		ref                    string
		expectedAdvertisements []string
	}{
		{
			ref: "refs/merge-requests/1/head",
			expectedAdvertisements: []string{
				"HEAD",
				"refs/heads/main\n",
				"refs/merge-requests/1/head\n",
			},
		},
		{
			ref: "refs/environments/1",
			expectedAdvertisements: []string{
				"HEAD",
				"refs/environments/1\n",
				"refs/heads/main\n",
			},
		},
		{
			ref: "refs/pipelines/1",
			expectedAdvertisements: []string{
				"HEAD",
				"refs/heads/main\n",
				"refs/pipelines/1\n",
			},
		},
		{
			ref: "refs/tmp/1",
			expectedAdvertisements: []string{
				"HEAD",
				"refs/heads/main\n",
			},
		},
		{
			ref: "refs/keep-around/1",
			expectedAdvertisements: []string{
				"HEAD",
				"refs/heads/main\n",
			},
		},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

			commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())
			gittest.Exec(t, cfg, "-C", repoPath, "update-ref", tc.ref, commitID.String())

			var expectedAdvertisements []string
			for _, expectedRef := range tc.expectedAdvertisements {
				expectedAdvertisements = append(expectedAdvertisements, commitID.String()+" "+expectedRef)
			}

			response, err := makeInfoRefsUploadPackRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, &gitalypb.InfoRefsRequest{
				Repository: repo,
			})
			require.NoError(t, err)
			requireAdvertisedRefs(t, string(response), "git-upload-pack", expectedAdvertisements)
		})
	}
}

func TestInfoRefsUploadPack_validate(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testInfoRefsUploadPackValidate)
}

func testInfoRefsUploadPackValidate(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	serverSocketPath := runSmartHTTPServer(t, cfg)

	for _, tc := range []struct {
		desc        string
		req         *gitalypb.InfoRefsRequest
		expectedErr error
	}{
		{
			desc:        "repository not provided",
			req:         &gitalypb.InfoRefsRequest{Repository: nil},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "not existing repository",
			req: &gitalypb.InfoRefsRequest{Repository: &gitalypb.Repository{
				StorageName:  cfg.Storages[0].Name,
				RelativePath: "doesnt/exist",
			}},
			expectedErr: testhelper.ToInterceptedMetadata(
				structerr.New("%w", storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, "doesnt/exist")),
			),
		},
	} {
		_, err := makeInfoRefsUploadPackRequest(t, ctx, serverSocketPath, cfg.Auth.Token, tc.req)
		testhelper.RequireGrpcError(t, tc.expectedErr, err)
	}
}

func TestInfoRefsUploadPack_partialClone(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testInfoRefsUploadPackPartialClone)
}

func testInfoRefsUploadPackPartialClone(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	partialResponse, err := makeInfoRefsUploadPackRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, &gitalypb.InfoRefsRequest{
		Repository: repo,
	})
	require.NoError(t, err)
	partialRefs := stats.ReferenceDiscovery{}
	err = partialRefs.Parse(bytes.NewReader(partialResponse))
	require.NoError(t, err)

	for _, c := range []string{"allow-tip-sha1-in-want", "allow-reachable-sha1-in-want", "filter"} {
		require.Contains(t, partialRefs.Caps, c)
	}
}

func TestInfoRefsUploadPack_gitConfigOptions(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testInfoRefsUploadPackGitConfigOptions)
}

func testInfoRefsUploadPackGitConfigOptions(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())

	// transfer.hideRefs=refs will hide every ref that info-refs would normally
	// output, allowing us to test that the custom configuration is respected
	rpcRequest := &gitalypb.InfoRefsRequest{
		Repository:       repo,
		GitConfigOptions: []string{"transfer.hideRefs=refs"},
	}
	response, err := makeInfoRefsUploadPackRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, rpcRequest)
	require.NoError(t, err)
	requireAdvertisedRefs(t, string(response), "git-upload-pack", []string{
		commitID.String() + " HEAD",
	})
}

func TestInfoRefsUploadPack_bundleURI(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx = featureflag.OutgoingCtxWithFeatureFlag(ctx, featureflag.BundleURI, true)

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())

	rpcRequest := &gitalypb.InfoRefsRequest{
		Repository:       repo,
		GitProtocol:      git.ProtocolV2,
		GitConfigOptions: []string{"transfer.bundleURI=true"},
	}
	response, err := makeInfoRefsUploadPackRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, rpcRequest)
	require.NoError(t, err)
	requireAdvertisedCapabilitiesV2(t, string(response), "git-upload-pack", []string{
		"bundle-uri",
	})
}

func TestInfoRefsUploadPack_gitProtocol(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testInfoRefsUploadPackGitProtocol)
}

func testInfoRefsUploadPackGitProtocol(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)

	protocolDetectingFactory := gittest.NewProtocolDetectingCommandFactory(t, ctx, cfg)

	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithGitCommandFactory(protocolDetectingFactory),
	})
	cfg.SocketPath = server.Address()

	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	client := newSmartHTTPClient(t, server.Address(), cfg.Auth.Token)

	c, err := client.InfoRefsUploadPack(ctx, &gitalypb.InfoRefsRequest{
		Repository:  repo,
		GitProtocol: git.ProtocolV2,
	})
	require.NoError(t, err)

	for {
		if _, err := c.Recv(); err != nil {
			require.Equal(t, io.EOF, err)
			break
		}
	}

	envData := protocolDetectingFactory.ReadProtocol(t)
	require.Contains(t, envData, fmt.Sprintf("GIT_PROTOCOL=%s\n", git.ProtocolV2))
}

func TestInfoRefsUploadPack_cacheNotStuckUponContextCancellation(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testInfoRefsUploadPackCacheNotStuckUponContextCancellation)
}

// We mainly test for goroutine leakage here, so if the server is hanging on finishing the request,
// we will be leaking a goroutine and the test framework will panic.
func testInfoRefsUploadPackCacheNotStuckUponContextCancellation(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)

	locator := config.NewLocator(cfg)
	logger := testhelper.NewLogger(t)
	cache := cache.New(cfg, locator, logger)

	streamer := mockStreamer{
		Streamer: cache,
	}
	mockInfoRefCache := newInfoRefCache(logger, &streamer)

	// This is to simulate a long-running `git upload-pack` command so that we can have time to cancel the request abruptly.
	gitCmdFactory := gittest.NewInterceptingCommandFactory(t, ctx, cfg, func(execEnv git.ExecutionEnvironment) string {
		return fmt.Sprintf(`#!/usr/bin/env bash
						if [[ "$@" =~ "upload-pack" ]]; then
							while true; do echo "foo"; done
							exit 1
						fi
						exec %q "$@"`, execEnv.BinaryPath)
	})
	server := startSmartHTTPServerWithOptions(t, cfg,
		[]ServerOpt{withInfoRefCache(mockInfoRefCache)},
		[]testserver.GitalyServerOpt{testserver.WithGitCommandFactory(gitCmdFactory)},
	)
	cfg.SocketPath = server.Address()

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())
	_ = gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	requestCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	rpcRequest := &gitalypb.InfoRefsRequest{Repository: repo}
	_, err := makeInfoRefsUploadPackRequest(t, requestCtx, cfg.SocketPath, cfg.Auth.Token, rpcRequest)
	require.Error(t, err)
}

func makeInfoRefsUploadPackRequest(t *testing.T, ctx context.Context, serverSocketPath, token string, rpcRequest *gitalypb.InfoRefsRequest) ([]byte, error) {
	t.Helper()

	client := newSmartHTTPClient(t, serverSocketPath, token)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, err := client.InfoRefsUploadPack(ctx, rpcRequest)
	require.NoError(t, err)

	response, err := io.ReadAll(streamio.NewReader(func() ([]byte, error) {
		resp, err := c.Recv()
		return resp.GetData(), err
	}))

	return response, err
}

func TestInfoRefsReceivePack_successful(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testInfoRefsReceivePackSuccessful)
}

func testInfoRefsReceivePackSuccessful(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())
	tagID := gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	response, err := makeInfoRefsReceivePackRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, &gitalypb.InfoRefsRequest{
		Repository: repo,
	})
	require.NoError(t, err)

	requireAdvertisedRefs(t, string(response), "git-receive-pack", []string{
		commitID.String() + " refs/heads/main",
		tagID.String() + " refs/tags/v1.0.0\n",
	})
}

func TestInfoRefsReceivePack_hiddenRefs(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testInfoRefsReceivePackHiddenRefs)
}

func testInfoRefsReceivePackHiddenRefs(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)

	testcfg.BuildGitalyHooks(t, cfg)

	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	_, poolPath := gittest.CreateObjectPool(t, ctx, cfg, repoProto, gittest.CreateObjectPoolConfig{
		LinkRepositoryToObjectPool: true,
	})
	commitID := gittest.WriteCommit(t, cfg, poolPath, gittest.WithBranch(t.Name()))

	response, err := makeInfoRefsReceivePackRequest(t, ctx, cfg.SocketPath, cfg.Auth.Token, &gitalypb.InfoRefsRequest{
		Repository: repoProto,
	})
	require.NoError(t, err)
	require.NotContains(t, string(response), commitID+" .have")
}

func TestInfoRefsReceivePack_validate(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testInfoRefsReceivePackValidate)
}

func testInfoRefsReceivePackValidate(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	serverSocketPath := runSmartHTTPServer(t, cfg)

	for _, tc := range []struct {
		desc        string
		req         *gitalypb.InfoRefsRequest
		expectedErr error
	}{
		{
			desc:        "repository not provided",
			req:         &gitalypb.InfoRefsRequest{Repository: nil},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "not existing repository",
			req: &gitalypb.InfoRefsRequest{Repository: &gitalypb.Repository{
				StorageName:  cfg.Storages[0].Name,
				RelativePath: "testdata/scratch/another_repo",
			}},
			expectedErr: testhelper.ToInterceptedMetadata(
				structerr.New("%w", storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, "testdata/scratch/another_repo")),
			),
		},
	} {
		_, err := makeInfoRefsReceivePackRequest(t, ctx, serverSocketPath, cfg.Auth.Token, tc.req)
		testhelper.RequireGrpcError(t, tc.expectedErr, err)
	}
}

func makeInfoRefsReceivePackRequest(t *testing.T, ctx context.Context, serverSocketPath, token string, rpcRequest *gitalypb.InfoRefsRequest) ([]byte, error) {
	t.Helper()

	client := newSmartHTTPClient(t, serverSocketPath, token)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, err := client.InfoRefsReceivePack(ctx, rpcRequest)
	require.NoError(t, err)

	response, err := io.ReadAll(streamio.NewReader(func() ([]byte, error) {
		resp, err := c.Recv()
		return resp.GetData(), err
	}))

	return response, err
}

func requireAdvertisedCapabilitiesV2(t *testing.T, responseBody, expectedService string, expectedCapabilities []string) {
	t.Helper()

	responseLines := strings.SplitAfter(responseBody, "\n")
	require.Greater(t, len(responseLines), 2)

	// The first line contains the service announcement
	require.Equal(t, gittest.Pktlinef(t, "# service=%s\n", expectedService), responseLines[0])

	// The second line contains the protocol version
	require.Equal(t, "0000"+gittest.Pktlinef(t, "version %d\n", 2), responseLines[1])

	// The third line and following lines contain capabilities
	for _, expectedCap := range expectedCapabilities {
		require.Contains(t, responseLines[2:], gittest.Pktlinef(t, "%s\n", expectedCap))
	}
}

func requireAdvertisedRefs(t *testing.T, responseBody, expectedService string, expectedRefs []string) {
	t.Helper()

	responseLines := strings.SplitAfter(responseBody, "\n")
	require.Greater(t, len(responseLines), 2)

	for i, expectedRef := range expectedRefs {
		expectedRefs[i] = gittest.Pktlinef(t, "%s", expectedRef)
	}

	// The first line contains the service announcement.
	require.Equal(t, gittest.Pktlinef(t, "# service=%s\n", expectedService), responseLines[0])

	// The second line contains the first reference as well as the capability announcement. We
	// thus split the string at "\x00" and ignore the capability announcement here.
	refAndCapabilities := strings.SplitN(responseLines[1], "\x00", 2)
	require.Len(t, refAndCapabilities, 2)
	// We just replace the first advertised reference to make it easier to compare refs.
	responseLines[1] = gittest.Pktlinef(t, "%s", refAndCapabilities[0][8:])

	require.Equal(t, responseLines[1:len(responseLines)-1], expectedRefs)
	require.Equal(t, "0000", responseLines[len(responseLines)-1])
}

type mockStreamer struct {
	cache.Streamer
	putStream func(context.Context, *gitalypb.Repository, proto.Message, io.Reader) error
}

func (ms *mockStreamer) PutStream(ctx context.Context, repo *gitalypb.Repository, req proto.Message, src io.Reader) error {
	if ms.putStream != nil {
		return ms.putStream(ctx, repo, req, src)
	}
	return ms.Streamer.PutStream(ctx, repo, req, src)
}

func TestInfoRefsUploadPack_cache(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.BundleURI,
	).Run(t, testInfoRefsUploadPackCache)
}

func testInfoRefsUploadPackCache(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)

	locator := config.NewLocator(cfg)
	logger := testhelper.NewLogger(t)
	cache := cache.New(cfg, locator, logger)

	streamer := mockStreamer{
		Streamer: cache,
	}
	mockInfoRefCache := newInfoRefCache(logger, &streamer)

	gitalyServer := startSmartHTTPServer(t, cfg, withInfoRefCache(mockInfoRefCache))
	cfg.SocketPath = gitalyServer.Address()

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())
	tagID := gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	rpcRequest := &gitalypb.InfoRefsRequest{Repository: repo}

	// The key computed for the cache entry takes into account all feature flags. Because
	// Praefect explicitly injects all unset feature flags, the key is thus different depending
	// on whether Praefect is in use or not. We thus manually inject all feature flags here such
	// that they're forced to the same state.
	for _, ff := range featureflag.DefinedFlags() {
		ctx = featureflag.OutgoingCtxWithFeatureFlag(ctx, ff, true)
		ctx = featureflag.IncomingCtxWithFeatureFlag(ctx, ff, true)
	}

	assertNormalResponse := func(addr string) {
		response, err := makeInfoRefsUploadPackRequest(t, ctx, addr, cfg.Auth.Token, rpcRequest)
		require.NoError(t, err)

		requireAdvertisedRefs(t, string(response), "git-upload-pack", []string{
			commitID.String() + " HEAD",
			commitID.String() + " refs/heads/main\n",
			tagID.String() + " refs/tags/v1.0.0\n",
			commitID.String() + " refs/tags/v1.0.0^{}\n",
		})
	}

	assertNormalResponse(gitalyServer.Address())
	rewrittenRequest := &gitalypb.InfoRefsRequest{Repository: gittest.RewrittenRepository(t, ctx, cfg, repo)}
	require.FileExists(t, pathToCachedResponse(t, ctx, cache, rewrittenRequest))

	replacedContents := []string{
		"first line",
		"meow meow meow meow",
		"woof woof woof woof",
		"last line",
	}

	// replace cached response file to prove the info-ref uses the cache
	replaceCachedResponse(t, ctx, cache, rewrittenRequest, strings.Join(replacedContents, "\n"))
	response, err := makeInfoRefsUploadPackRequest(t, ctx, gitalyServer.Address(), cfg.Auth.Token, rpcRequest)
	require.NoError(t, err)
	require.Equal(t, strings.Join(replacedContents, "\n"), string(response))

	invalidateCacheForRepo := func() {
		ender, err := cache.StartLease(rewrittenRequest.Repository)
		require.NoError(t, err)
		require.NoError(t, ender.EndLease(setInfoRefsUploadPackMethod(ctx)))
	}

	invalidateCacheForRepo()

	// replaced cache response is no longer valid
	assertNormalResponse(gitalyServer.Address())

	// Create a repository and break it by removing 'objects'.
	invalidRepo, invalidRepoPath := gittest.CreateRepository(t, ctx, cfg)
	require.NoError(t, os.RemoveAll(filepath.Join(invalidRepoPath, "objects")))

	// failed requests should not cache response
	invalidReq := &gitalypb.InfoRefsRequest{Repository: invalidRepo}

	// The request is expected to fail with FailedPrecondition as the 'objects' directory has been removed.
	expectedStatusCode := codes.FailedPrecondition
	if testhelper.IsWALEnabled() {
		// With transactions, the status code is internal as the transaction fails to be started.
		expectedStatusCode = codes.Internal
	}

	_, err = makeInfoRefsUploadPackRequest(t, ctx, gitalyServer.Address(), cfg.Auth.Token, invalidReq)
	testhelper.RequireGrpcCode(t, err, expectedStatusCode)
	require.NoFileExists(t, pathToCachedResponse(t, ctx, cache, invalidReq))

	// if an error occurs while putting stream, it should not interrupt
	// request from being served
	happened := false
	streamer.putStream = func(context.Context, *gitalypb.Repository, proto.Message, io.Reader) error {
		happened = true
		return errors.New("oopsie")
	}

	invalidateCacheForRepo()
	assertNormalResponse(gitalyServer.Address())
	require.True(t, happened)
}

func withInfoRefCache(cache infoRefCache) ServerOpt {
	return func(s *server) {
		s.infoRefCache = cache
	}
}

func replaceCachedResponse(tb testing.TB, ctx context.Context, cache *cache.DiskCache, req *gitalypb.InfoRefsRequest, newContents string) {
	path := pathToCachedResponse(tb, ctx, cache, req)
	require.NoError(tb, os.WriteFile(path, []byte(newContents), perm.SharedFile))
}

func setInfoRefsUploadPackMethod(ctx context.Context) context.Context {
	return testhelper.SetCtxGrpcMethod(ctx, "/gitaly.SmartHTTPService/InfoRefsUploadPack")
}

func pathToCachedResponse(tb testing.TB, ctx context.Context, cache *cache.DiskCache, req *gitalypb.InfoRefsRequest) string {
	ctx = setInfoRefsUploadPackMethod(ctx)
	path, err := cache.KeyPath(ctx, req.GetRepository(), req)
	require.NoError(tb, err)
	return path
}
