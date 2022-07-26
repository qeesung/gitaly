//go:build !gitaly_test_sha256

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

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

func TestInfoRefsUploadPack_successful(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testInfoRefsUploadPackSuccessful)
}

func testInfoRefsUploadPackSuccessful(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())
	tagID := gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	rpcRequest := &gitalypb.InfoRefsRequest{Repository: repo}
	response, err := makeInfoRefsUploadPackRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, rpcRequest)
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
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testInfoRefsUploadPackInternalRefs)
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
			expectedAdvertisements: func() []string {
				if featureflag.UploadPackHideRefs.IsDisabled(ctx) {
					return []string{
						"HEAD",
						"refs/heads/main\n",
						"refs/tmp/1\n",
					}
				}

				return []string{
					"HEAD",
					"refs/heads/main\n",
				}
			}(),
		},
		{
			ref: "refs/keep-around/1",
			expectedAdvertisements: func() []string {
				if featureflag.UploadPackHideRefs.IsDisabled(ctx) {
					return []string{
						"HEAD",
						"refs/heads/main\n",
						"refs/keep-around/1\n",
					}
				}

				return []string{
					"HEAD",
					"refs/heads/main\n",
				}
			}(),
		},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

			commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())
			gittest.Exec(t, cfg, "-C", repoPath, "update-ref", tc.ref, commitID.String())

			var expectedAdvertisements []string
			for _, expectedRef := range tc.expectedAdvertisements {
				expectedAdvertisements = append(expectedAdvertisements, commitID.String()+" "+expectedRef)
			}

			response, err := makeInfoRefsUploadPackRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, &gitalypb.InfoRefsRequest{
				Repository: repo,
			})
			require.NoError(t, err)
			requireAdvertisedRefs(t, string(response), "git-upload-pack", expectedAdvertisements)
		})
	}
}

func TestInfoRefsUploadPackRepositoryDoesntExist(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testInfoRefsUploadPackRepositoryDoesntExist)
}

func testInfoRefsUploadPackRepositoryDoesntExist(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	serverSocketPath := runSmartHTTPServer(t, cfg)

	rpcRequest := &gitalypb.InfoRefsRequest{Repository: &gitalypb.Repository{
		StorageName:  cfg.Storages[0].Name,
		RelativePath: "doesnt/exist",
	}}

	_, err := makeInfoRefsUploadPackRequest(ctx, t, serverSocketPath, cfg.Auth.Token, rpcRequest)

	expectedErr := helper.ErrNotFoundf(`GetRepoPath: not a git repository: "` + cfg.Storages[0].Path + `/doesnt/exist"`)
	if testhelper.IsPraefectEnabled() {
		expectedErr = helper.ErrNotFoundf(`accessor call: route repository accessor: consistent storages: repository "default"/"doesnt/exist" not found`)
	}

	testhelper.RequireGrpcError(t, expectedErr, err)
}

func TestInfoRefsUploadPackPartialClone(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testInfoRefsUploadPackPartialClone)
}

func testInfoRefsUploadPackPartialClone(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	request := &gitalypb.InfoRefsRequest{
		Repository: repo,
	}

	partialResponse, err := makeInfoRefsUploadPackRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, request)
	require.NoError(t, err)
	partialRefs := stats.ReferenceDiscovery{}
	err = partialRefs.Parse(bytes.NewReader(partialResponse))
	require.NoError(t, err)

	for _, c := range []string{"allow-tip-sha1-in-want", "allow-reachable-sha1-in-want", "filter"} {
		require.Contains(t, partialRefs.Caps, c)
	}
}

func TestInfoRefsUploadPackGitConfigOptions(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testInfoRefsUploadPackGitConfigOptions)
}

func testInfoRefsUploadPackGitConfigOptions(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())

	// transfer.hideRefs=refs will hide every ref that info-refs would normally
	// output, allowing us to test that the custom configuration is respected
	rpcRequest := &gitalypb.InfoRefsRequest{
		Repository:       repo,
		GitConfigOptions: []string{"transfer.hideRefs=refs"},
	}
	response, err := makeInfoRefsUploadPackRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, rpcRequest)
	require.NoError(t, err)
	requireAdvertisedRefs(t, string(response), "git-upload-pack", []string{
		commitID.String() + " HEAD",
	})
}

func TestInfoRefsUploadPackGitProtocol(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testInfoRefsUploadPackGitProtocol)
}

func testInfoRefsUploadPackGitProtocol(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)

	protocolDetectingFactory := gittest.NewProtocolDetectingCommandFactory(ctx, t, cfg)

	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithGitCommandFactory(protocolDetectingFactory),
	})
	cfg.SocketPath = server.Address()

	repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	rpcRequest := &gitalypb.InfoRefsRequest{
		Repository:  repo,
		GitProtocol: git.ProtocolV2,
	}

	client, conn := newSmartHTTPClient(t, server.Address(), cfg.Auth.Token)
	defer testhelper.MustClose(t, conn)

	c, err := client.InfoRefsUploadPack(ctx, rpcRequest)
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

func makeInfoRefsUploadPackRequest(ctx context.Context, t *testing.T, serverSocketPath, token string, rpcRequest *gitalypb.InfoRefsRequest) ([]byte, error) {
	t.Helper()

	client, conn := newSmartHTTPClient(t, serverSocketPath, token)
	defer conn.Close()

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

func TestInfoRefsReceivePackSuccessful(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testInfoRefsReceivePackSuccessful)
}

func testInfoRefsReceivePackSuccessful(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())
	tagID := gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	response, err := makeInfoRefsReceivePackRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, &gitalypb.InfoRefsRequest{
		Repository: repo,
	})
	require.NoError(t, err)

	requireAdvertisedRefs(t, string(response), "git-receive-pack", []string{
		commitID.String() + " refs/heads/main",
		tagID.String() + " refs/tags/v1.0.0\n",
	})
}

func TestInfoRefsReceivePackHiddenRefs(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testInfoRefsReceivePackHiddenRefs)
}

func testInfoRefsReceivePackHiddenRefs(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)

	testcfg.BuildGitalyHooks(t, cfg)

	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repoProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)
	txManager := transaction.NewManager(cfg, backchannel.NewRegistry())

	pool, err := objectpool.NewObjectPool(
		config.NewLocator(cfg),
		gittest.NewCommandFactory(t, cfg),
		nil,
		txManager,
		housekeeping.NewManager(cfg.Prometheus, txManager),
		repo.GetStorageName(),
		gittest.NewObjectPoolName(t),
	)
	require.NoError(t, err)

	require.NoError(t, pool.Create(ctx, repo))
	defer func() {
		require.NoError(t, pool.Remove(ctx))
	}()

	commitID := gittest.WriteCommit(t, cfg, pool.FullPath(), gittest.WithBranch(t.Name()))

	require.NoError(t, pool.Link(ctx, repo))

	rpcRequest := &gitalypb.InfoRefsRequest{Repository: repoProto}

	response, err := makeInfoRefsReceivePackRequest(ctx, t, cfg.SocketPath, cfg.Auth.Token, rpcRequest)
	require.NoError(t, err)
	require.NotContains(t, string(response), commitID+" .have")
}

func TestInfoRefsReceivePackRepoNotFound(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testInfoRefsReceivePackRepoNotFound)
}

func testInfoRefsReceivePackRepoNotFound(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)

	serverSocketPath := runSmartHTTPServer(t, cfg)

	repo := &gitalypb.Repository{StorageName: cfg.Storages[0].Name, RelativePath: "testdata/scratch/another_repo"}
	rpcRequest := &gitalypb.InfoRefsRequest{Repository: repo}
	_, err := makeInfoRefsReceivePackRequest(ctx, t, serverSocketPath, cfg.Auth.Token, rpcRequest)

	expectedErr := helper.ErrNotFoundf(`GetRepoPath: not a git repository: "` + cfg.Storages[0].Path + "/" + repo.RelativePath + `"`)
	if testhelper.IsPraefectEnabled() {
		expectedErr = helper.ErrNotFoundf(`accessor call: route repository accessor: consistent storages: repository "default"/"testdata/scratch/another_repo" not found`)
	}

	testhelper.RequireGrpcError(t, expectedErr, err)
}

func TestInfoRefsReceivePackRepoNotSet(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testInfoRefsReceivePackRepoNotSet)
}

func testInfoRefsReceivePackRepoNotSet(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)

	serverSocketPath := runSmartHTTPServer(t, cfg)

	rpcRequest := &gitalypb.InfoRefsRequest{}
	_, err := makeInfoRefsReceivePackRequest(ctx, t, serverSocketPath, cfg.Auth.Token, rpcRequest)
	testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
}

func makeInfoRefsReceivePackRequest(ctx context.Context, t *testing.T, serverSocketPath, token string, rpcRequest *gitalypb.InfoRefsRequest) ([]byte, error) {
	t.Helper()

	client, conn := newSmartHTTPClient(t, serverSocketPath, token)
	defer conn.Close()

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

func TestInfoRefsUploadPackCache(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testInfoRefsUploadPackCache)
}

func testInfoRefsUploadPackCache(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)

	locator := config.NewLocator(cfg)
	cache := cache.New(cfg, locator)

	streamer := mockStreamer{
		Streamer: cache,
	}
	mockInfoRefCache := newInfoRefCache(&streamer)

	gitalyServer := startSmartHTTPServer(t, cfg, withInfoRefCache(mockInfoRefCache))
	cfg.SocketPath = gitalyServer.Address()

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())
	tagID := gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	rpcRequest := &gitalypb.InfoRefsRequest{Repository: repo}

	// The key computed for the cache entry takes into account all feature flags. Because
	// Praefect explicitly injects all unset feature flags, the key is thus differend depending
	// on whether Praefect is in use or not. We thus manually inject all feature flags here such
	// that they're forced to the same state.
	for _, ff := range featureflag.DefinedFlags() {
		ctx = featureflag.OutgoingCtxWithFeatureFlag(ctx, ff, true)
		ctx = featureflag.IncomingCtxWithFeatureFlag(ctx, ff, true)
	}

	assertNormalResponse := func(addr string) {
		response, err := makeInfoRefsUploadPackRequest(ctx, t, addr, cfg.Auth.Token, rpcRequest)
		require.NoError(t, err)

		requireAdvertisedRefs(t, string(response), "git-upload-pack", []string{
			commitID.String() + " HEAD",
			commitID.String() + " refs/heads/main\n",
			tagID.String() + " refs/tags/v1.0.0\n",
			commitID.String() + " refs/tags/v1.0.0^{}\n",
		})
	}

	assertNormalResponse(gitalyServer.Address())
	rewrittenRequest := &gitalypb.InfoRefsRequest{Repository: gittest.RewrittenRepository(ctx, t, cfg, repo)}
	require.FileExists(t, pathToCachedResponse(t, ctx, cache, rewrittenRequest))

	replacedContents := []string{
		"first line",
		"meow meow meow meow",
		"woof woof woof woof",
		"last line",
	}

	// replace cached response file to prove the info-ref uses the cache
	replaceCachedResponse(t, ctx, cache, rewrittenRequest, strings.Join(replacedContents, "\n"))
	response, err := makeInfoRefsUploadPackRequest(ctx, t, gitalyServer.Address(), cfg.Auth.Token, rpcRequest)
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

	// failed requests should not cache response
	invalidReq := &gitalypb.InfoRefsRequest{
		Repository: &gitalypb.Repository{
			RelativePath: "fake_repo",
			StorageName:  repo.StorageName,
		},
	} // invalid request because repo is empty
	invalidRepoCleanup := createInvalidRepo(t, filepath.Join(testhelper.GitlabTestStoragePath(), invalidReq.Repository.RelativePath))
	defer invalidRepoCleanup()

	_, err = makeInfoRefsUploadPackRequest(ctx, t, gitalyServer.Address(), cfg.Auth.Token, invalidReq)
	testhelper.RequireGrpcCode(t, err, codes.NotFound)
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

func createInvalidRepo(t testing.TB, repoDir string) func() {
	for _, subDir := range []string{"objects", "refs", "HEAD"} {
		require.NoError(t, os.MkdirAll(filepath.Join(repoDir, subDir), 0o755))
	}
	return func() { require.NoError(t, os.RemoveAll(repoDir)) }
}

func replaceCachedResponse(t testing.TB, ctx context.Context, cache *cache.DiskCache, req *gitalypb.InfoRefsRequest, newContents string) {
	path := pathToCachedResponse(t, ctx, cache, req)
	require.NoError(t, os.WriteFile(path, []byte(newContents), 0o644))
}

func setInfoRefsUploadPackMethod(ctx context.Context) context.Context {
	return testhelper.SetCtxGrpcMethod(ctx, "/gitaly.SmartHTTPService/InfoRefsUploadPack")
}

func pathToCachedResponse(t testing.TB, ctx context.Context, cache *cache.DiskCache, req *gitalypb.InfoRefsRequest) string {
	ctx = setInfoRefsUploadPackMethod(ctx)
	path, err := cache.KeyPath(ctx, req.GetRepository(), req)
	require.NoError(t, err)
	return path
}
