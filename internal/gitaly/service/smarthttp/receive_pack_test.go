package smarthttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/grpc"
)

func TestPostReceivePack_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)
	cfg.GitlabShell.Dir = "/foo/bar/gitlab-shell"

	testcfg.BuildGitalyHooks(t, cfg)

	// We need to intercept the update hook, but proxy the reference-transactions hook.
	logger := testhelper.SharedLogger(t)
	backchannelRegistry := backchannel.NewRegistry()
	transactionRegistry := storagemgr.NewTransactionRegistry()
	hookManager := gitalyhook.NewManager(
		cfg,
		config.NewLocator(cfg),
		logger,
		gittest.NewCommandFactory(t, cfg),
		transaction.NewManager(cfg, logger, backchannelRegistry),
		gitlab.NewMockClient(t, gitlab.MockAllowed, gitlab.MockPreReceive, gitlab.MockPostReceive),
		gitalyhook.NewTransactionRegistry(transactionRegistry),
		gitalyhook.NewProcReceiveRegistry(),
		nil,
	)

	// When the WAL is enabled, the proc-receive hook is executed which also manually invokes the
	// update hook. Here we use a mock hook manager to intercept the update hook and capture the
	// environment variables.
	var capturedHookEnv []string
	var preReceiveCount, postReceiveCount int
	mockHookManager := gitalyhook.NewMockManager(t,
		func(_ *testing.T, _ context.Context, _ *gitalypb.Repository, _, _ []string, stdin io.Reader, _, _ io.Writer) error {
			// Discard the stream before returning so the HookService RPC does not return before the stdin has been
			// fully consumed.
			_, err := io.Copy(io.Discard, stdin)
			assert.NoError(t, err)
			preReceiveCount++
			return nil
		},
		func(_ *testing.T, _ context.Context, _ *gitalypb.Repository, _, _ []string, stdin io.Reader, _, _ io.Writer) error {
			// Discard the stream before returning so the HookService RPC does not return before the stdin has been
			// fully consumed.
			_, err := io.Copy(io.Discard, stdin)
			assert.NoError(t, err)
			postReceiveCount++
			return nil
		},
		func(_ *testing.T, _ context.Context, _ *gitalypb.Repository, _, _, _ string, env []string, _, _ io.Writer) error {
			capturedHookEnv = env
			return nil
		},
		func(t *testing.T, ctx context.Context, state gitalyhook.ReferenceTransactionState, env []string, stdin io.Reader) error {
			return hookManager.ReferenceTransactionHook(ctx, state, env, stdin)
		},
		gitalyhook.NewProcReceiveRegistry(),
	)

	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithHookManager(mockHookManager),
		testserver.WithTransactionRegistry(transactionRegistry),
		testserver.WithBackchannelRegistry(backchannelRegistry),
	})
	cfg.SocketPath = server.Address()

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo.GlProjectPath = "project/path"

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))
	push := setupSimplePush(t, ctx, cfg, repoPath, "refs/heads/branch")

	// Below, we test whether extracting the hooks payload leads to the expected
	// results. Part of this payload are feature flags, so we need to get them into a
	// deterministic state such that we can compare them properly. While we wouldn't
	// need to inject them in "normal" Gitaly tests, Praefect will inject all unset
	// feature flags and set them to `false` -- as a result, we have a mismatch between
	// the context's feature flags we see here and the context's metadata as it would
	// arrive on the proxied Gitaly. To fix this, we thus inject all feature flags
	// explicitly here.
	for _, ff := range featureflag.DefinedFlags() {
		ctx = featureflag.OutgoingCtxWithFeatureFlag(ctx, ff, true)
		ctx = featureflag.IncomingCtxWithFeatureFlag(ctx, ff, true)
	}

	client := newSmartHTTPClient(t, server.Address(), cfg.Auth.Token)
	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	response := push.perform(t, stream, &gitalypb.PostReceivePackRequest{
		Repository:   repo,
		GlUsername:   "user",
		GlId:         "123",
		GlRepository: "project-456",
	})

	requireSideband(t, []string{
		gittest.Pktlinef(t, "\x01%s", strings.Join([]string{
			gittest.Pktlinef(t, "unpack ok\n"),
			gittest.Pktlinef(t, "ok refs/heads/branch\n"),
			"0000",
		}, "")),
	}, response)

	// The fact that this command succeeds means that we got the commit correctly, no further
	// checks should be needed.
	gittest.Exec(t, cfg, "-C", repoPath, "show", push.refUpdates[0].to.String())

	payload, err := git.HooksPayloadFromEnv(capturedHookEnv)
	require.NoError(t, err)

	expectedHooks := git.ReceivePackHooks

	// Compare the repository up front so that we can use require.Equal for
	// the remaining values.
	expectedRepo := gittest.RewrittenRepository(t, ctx, cfg, repo)
	if testhelper.IsWALEnabled() {
		// The repository should be quarantined.
		require.NotEmpty(t, payload.Repo.GitObjectDirectory)
		payload.Repo.GitObjectDirectory = "OVERRIDDEN"
		expectedRepo.GitObjectDirectory = "OVERRIDDEN"
		require.NotEmpty(t, payload.Repo.GitAlternateObjectDirectories)
		payload.Repo.GitAlternateObjectDirectories = []string{"OVERRIDDEN"}
		expectedRepo.GitAlternateObjectDirectories = []string{"OVERRIDDEN"}
		// The following values may change so we don't want to assert them for equality.
		require.NotEmpty(t, payload.Repo.RelativePath)
		payload.Repo.RelativePath = "OVERRIDDEN"
		expectedRepo.RelativePath = "OVERRIDDEN"
		// When transactions are enabled the update hook is manually invoked as part of the
		// proc-receive hook. Consequently, the requested hooks only specify the update hook.
		expectedHooks = git.UpdateHook
	}

	testhelper.ProtoEqual(t, expectedRepo, payload.Repo)
	payload.Repo = nil

	// If running tests with Praefect, then the transaction would be set, but we have no way of
	// figuring out their actual contents. So let's just remove it, too.
	payload.Transaction = nil

	var expectedFeatureFlags []git.FeatureFlagWithValue
	for feature, enabled := range featureflag.FromContext(ctx) {
		expectedFeatureFlags = append(expectedFeatureFlags, git.FeatureFlagWithValue{
			Flag: feature, Enabled: enabled,
		})
	}

	// Compare here without paying attention to the order given that flags aren't sorted and
	// unset the struct member afterwards.
	require.ElementsMatch(t, expectedFeatureFlags, payload.FeatureFlagsWithValue)
	payload.FeatureFlagsWithValue = nil

	var transactionID storage.TransactionID
	if testhelper.IsWALEnabled() {
		transactionID = 2
	}

	require.Equal(t, git.HooksPayload{
		ObjectFormat:        gittest.DefaultObjectHash.Format,
		RuntimeDir:          cfg.RuntimeDir,
		InternalSocket:      cfg.InternalSocketPath(),
		InternalSocketToken: cfg.Auth.Token,
		UserDetails: &git.UserDetails{
			UserID:   "123",
			Username: "user",
			Protocol: "http",
		},
		RequestedHooks: expectedHooks,
		TransactionID:  transactionID,
	}, payload)

	require.Equal(t, 1, preReceiveCount)
	require.Equal(t, 1, postReceiveCount)
}

func TestPostReceivePack_hiddenRefs(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionProcReceive).Run(t, testPostReceivePackHiddenRefs)
}

func testPostReceivePackHiddenRefs(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)
	testcfg.BuildGitalyHooks(t, cfg)

	client := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)

	for _, ref := range []string{
		"refs/environments/1",
		"refs/merge-requests/1/head",
		"refs/merge-requests/1/merge",
		"refs/pipelines/1",
	} {
		ref := ref

		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
			repoProto.GlProjectPath = "project/path"

			gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference(ref))
			push := setupSimplePush(t, ctx, cfg, repoPath, git.ReferenceName(ref))

			stream, err := client.PostReceivePack(ctx)
			require.NoError(t, err)

			response := push.perform(t, stream, &gitalypb.PostReceivePackRequest{
				Repository:   repoProto,
				GlUsername:   "user",
				GlId:         "123",
				GlRepository: "project-456",
			})

			require.Contains(t, response, fmt.Sprintf("%s deny updating a hidden ref", ref))
		})
	}
}

func TestPostReceivePack_protocolV2(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionProcReceive).Run(t, testPostReceivePackProtocolV2)
}

func testPostReceivePackProtocolV2(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)

	protocolDetectingFactory := gittest.NewProtocolDetectingCommandFactory(t, ctx, cfg)

	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithGitCommandFactory(protocolDetectingFactory),
	})
	cfg.SocketPath = server.Address()

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
	push := setupSimplePush(t, ctx, cfg, repoPath, git.DefaultRef)

	client := newSmartHTTPClient(t, server.Address(), cfg.Auth.Token)

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	push.perform(t, stream, &gitalypb.PostReceivePackRequest{
		Repository:   repo,
		GlId:         "user-123",
		GlRepository: "project-123",
		GitProtocol:  git.ProtocolV2,
	})

	envData := protocolDetectingFactory.ReadProtocol(t)
	require.Equal(t, fmt.Sprintf("GIT_PROTOCOL=%s\n", git.ProtocolV2), envData)

	// The fact that this command succeeds means that we got the commit correctly, no further checks should be needed.
	gittest.Exec(t, cfg, "-C", repoPath, "show", push.refUpdates[0].to.String())
}

func TestPostReceivePack_packfiles(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionProcReceive).Run(t, testPostReceivePackPackfiles)
}

func testPostReceivePackPackfiles(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = startSmartHTTPServer(t, cfg).Address()
	testcfg.BuildGitalyHooks(t, cfg)

	client := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
	gittest.Exec(t, cfg, "-C", repoPath, "-c", "pack.writeReverseIndex=false", "repack", "-Adb")

	push := setupSimplePush(t, ctx, cfg, repoPath, git.DefaultRef)

	// Verify the before-state of the repository. It should have a single packfile, ...
	packfiles, err := filepath.Glob(filepath.Join(repoPath, "objects", "pack", "*.pack"))
	require.NoError(t, err)
	require.Len(t, packfiles, 1)

	// ... but no reverse index. `gittest.CreateRepository()` uses `CreateRepositoryFromURL()`
	// with a file path that doesn't use `file://` as prefix. As a result, Git uses the local
	// transport and just copies objects over without generating a reverse index. This is thus
	// expected as we don't want to perform a "real" clone, which would be a lot more expensive.
	reverseIndices, err := filepath.Glob(filepath.Join(repoPath, "objects", "pack", "*.rev"))
	require.NoError(t, err)
	require.Empty(t, reverseIndices)

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	push.perform(t, stream, &gitalypb.PostReceivePackRequest{
		Repository:   repo,
		GlId:         "user-123",
		GlRepository: "project-123",
		GitProtocol:  git.ProtocolV2,
		// By default, Git would unpack the received packfile if it has less than
		// 100 objects. Decrease this limit so that we indeed end up with another
		// new packfile even though we only push a small set of objects.
		GitConfigOptions: []string{
			"receive.unpackLimit=0",
		},
	})

	// We now should have two packfiles, ...
	packfiles, err = filepath.Glob(filepath.Join(repoPath, "objects", "pack", "*.pack"))
	require.NoError(t, err)
	require.Len(t, packfiles, 2)

	// ... and one reverse index for the newly added packfile.
	reverseIndices, err = filepath.Glob(filepath.Join(repoPath, "objects", "pack", "*.rev"))
	require.NoError(t, err)
	require.Len(t, reverseIndices, 1)
}

func TestPostReceivePack_rejectViaGitConfigOptions(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionProcReceive).Run(t, testPostReceivePackRejectViaGitConfigOptions)
}

func testPostReceivePackRejectViaGitConfigOptions(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
	push := setupSimplePush(t, ctx, cfg, repoPath, git.DefaultRef)

	client := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	response := push.perform(t, stream, &gitalypb.PostReceivePackRequest{
		Repository:       repo,
		GlId:             "user-123",
		GlRepository:     "project-123",
		GitConfigOptions: []string{"receive.MaxInputSize=1"},
	})

	requireSideband(t, []string{
		gittest.Pktlinef(t, "\x02fatal: pack exceeds maximum allowed size\n"),
		gittest.Pktlinef(t, "\x01%s", strings.Join([]string{
			gittest.Pktlinef(t, "unpack unpack-objects abnormal exit\n"),
			gittest.Pktlinef(t, "ng %s unpacker error\n", git.DefaultRef),
			"0000",
		}, "")),
	}, response)
}

func TestPostReceivePack_rejectViaHooks(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionProcReceive).Run(t, testPostReceivePackRejectViaHooks)
}

func testPostReceivePackRejectViaHooks(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg, git.WithHooksPath(testhelper.TempDir(t)))

	testhelper.WriteExecutable(t, filepath.Join(gitCmdFactory.HooksPath(ctx), "pre-receive"), []byte("#!/bin/sh\nexit 1"))

	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithGitCommandFactory(gitCmdFactory),
	})
	cfg.SocketPath = server.Address()

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
	push := setupSimplePush(t, ctx, cfg, repoPath, git.DefaultRef)

	client := newSmartHTTPClient(t, server.Address(), cfg.Auth.Token)

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	response := push.perform(t, stream, &gitalypb.PostReceivePackRequest{
		Repository:   repo,
		GlId:         "user-123",
		GlRepository: "project-123",
	})

	requireSideband(t, []string{
		gittest.Pktlinef(t, "\x01%s", strings.Join([]string{
			gittest.Pktlinef(t, "unpack ok\n"),
			gittest.Pktlinef(t, "ng %s pre-receive hook declined\n", git.DefaultRef),
			"0000",
		}, "")),
	}, response)
}

func TestPostReceivePack_requestValidation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	client := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)

	repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

	for _, tc := range []struct {
		desc        string
		request     *gitalypb.PostReceivePackRequest
		expectedErr error
	}{
		{
			desc: "Repository doesn't exist",
			request: &gitalypb.PostReceivePackRequest{
				Repository: &gitalypb.Repository{
					StorageName:  "fake",
					RelativePath: "path",
				},
				GlId: "user-123",
			},
			expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
				"%w", storage.NewStorageNotFoundError("fake"),
			)),
		},
		{
			desc:        "Repository is nil",
			request:     &gitalypb.PostReceivePackRequest{Repository: nil, GlId: "user-123"},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "Empty GlId",
			request: &gitalypb.PostReceivePackRequest{
				Repository: repoProto,
				GlId:       "",
			},
			expectedErr: structerr.NewInvalidArgument("empty GlId"),
		},
		{
			desc: "Data exists on first request",
			request: &gitalypb.PostReceivePackRequest{
				Repository: repoProto,
				GlId:       "user-123",
				Data:       []byte("Fail"),
			},
			expectedErr: structerr.NewInvalidArgument("non-empty Data"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			stream, err := client.PostReceivePack(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(tc.request))
			require.NoError(t, stream.CloseSend())

			err = drainPostReceivePackResponse(stream)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func TestPostReceivePack_invalidObjects(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionProcReceive).Run(t, testPostReceivePackInvalidObjects)
}

func testPostReceivePackInvalidObjects(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)

	testcfg.BuildGitalyHooks(t, cfg)
	server := startSmartHTTPServer(t, cfg)
	cfg.SocketPath = server.Address()

	client := newSmartHTTPClient(t, server.Address(), cfg.Auth.Token)

	for _, tc := range []struct {
		desc             string
		prepareCommit    func(t *testing.T, repoPath string) bytes.Buffer
		expectedSideband []string
		expectObject     bool
	}{
		{
			desc: "invalid timezone",
			prepareCommit: func(t *testing.T, repoPath string) bytes.Buffer {
				tree := gittest.ResolveRevision(t, cfg, repoPath, "HEAD^{tree}").String()
				head := gittest.ResolveRevision(t, cfg, repoPath, "HEAD^{commit}").String()

				var buf bytes.Buffer
				buf.WriteString("tree " + tree + "\n")
				buf.WriteString("parent " + head + "\n")
				buf.WriteString("author Au Thor <author@example.com> 1313584730 +051800\n")
				buf.WriteString("committer Au Thor <author@example.com> 1313584730 +051800\n")
				buf.WriteString("\n")
				buf.WriteString("Commit message\n")
				return buf
			},
			expectedSideband: []string{
				gittest.Pktlinef(t, "\x01%s", strings.Join([]string{
					gittest.Pktlinef(t, "unpack ok\n"),
					gittest.Pktlinef(t, "ok %s\n", git.DefaultRef),
					"0000",
				}, "")),
			},
			expectObject: true,
		},
		{
			desc: "missing author and committer date",
			prepareCommit: func(t *testing.T, repoPath string) bytes.Buffer {
				tree := gittest.ResolveRevision(t, cfg, repoPath, "HEAD^{tree}").String()
				head := gittest.ResolveRevision(t, cfg, repoPath, "HEAD^{commit}").String()

				var buf bytes.Buffer
				buf.WriteString("tree " + tree + "\n")
				buf.WriteString("parent " + head + "\n")
				buf.WriteString("author Au Thor <author@example.com>\n")
				buf.WriteString("committer Au Thor <author@example.com>\n")
				buf.WriteString("\n")
				buf.WriteString("Commit message\n")
				return buf
			},
			expectedSideband: []string{
				gittest.Pktlinef(t, "\x01%s", strings.Join([]string{
					gittest.Pktlinef(t, "unpack ok\n"),
					gittest.Pktlinef(t, "ok %s\n", git.DefaultRef),
					"0000",
				}, "")),
			},
			expectObject: true,
		},
		{
			desc: "zero-padded file mode",
			prepareCommit: func(t *testing.T, repoPath string) bytes.Buffer {
				head := gittest.ResolveRevision(t, cfg, repoPath, "HEAD^{commit}").String()

				subtree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Mode: "100644", Path: "file", Content: "content"},
				})

				brokenTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "subdir", Mode: "040000", OID: subtree},
				})

				var buf bytes.Buffer
				buf.WriteString("tree " + brokenTree.String() + "\n")
				buf.WriteString("parent " + head + "\n")
				buf.WriteString("author Au Thor <author@example.com>\n")
				buf.WriteString("committer Au Thor <author@example.com>\n")
				buf.WriteString("\n")
				buf.WriteString("Commit message\n")
				return buf
			},
			expectedSideband: []string{
				gittest.Pktlinef(t, "\x01%s", strings.Join([]string{
					gittest.Pktlinef(t, "unpack ok\n"),
					gittest.Pktlinef(t, "ok %s\n", git.DefaultRef),
					"0000",
				}, "")),
			},
			expectObject: true,
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
			gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

			push := setupPush(t, ctx, cfg, repoPath, func(repoPath string) []refUpdate {
				commitBuffer := tc.prepareCommit(t, repoPath)
				commitID := text.ChompBytes(gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: &commitBuffer},
					"-C", repoPath, "hash-object", "--literally", "-t", "commit", "--stdin", "-w",
				))

				currentHead := gittest.ResolveRevision(t, cfg, repoPath, string(git.DefaultRef))

				return []refUpdate{
					{
						ref:  git.DefaultRef,
						from: currentHead,
						to:   git.ObjectID(commitID),
					},
				}
			})

			stream, err := client.PostReceivePack(ctx)
			require.NoError(t, err)
			response := push.perform(t, stream, &gitalypb.PostReceivePackRequest{
				Repository:   repoProto,
				GlId:         "user-123",
				GlRepository: "project-456",
			})

			requireSideband(t, tc.expectedSideband, response)

			if tc.expectObject {
				gittest.RequireObjectExists(t, cfg, repoPath, push.refUpdates[0].to+"^{commit}")
			} else {
				gittest.RequireObjectNotExists(t, cfg, repoPath, push.refUpdates[0].to)
			}
		})
	}
}

func TestPostReceivePack_fsck(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionProcReceive).Run(t, testPostReceivePackFsck)
}

func testPostReceivePackFsck(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)
	testcfg.BuildGitalyHooks(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	oldCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	push := setupPush(t, ctx, cfg, repoPath, func(repoPath string) []refUpdate {
		blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("some content"))

		// We're creating a new commit which has a root tree with duplicate entries. git-mktree(1)
		// allows us to create these trees just fine, but git-fsck(1) complains.
		newCommitID := gittest.WriteCommit(t, cfg, repoPath,
			gittest.WithTreeEntries(
				gittest.TreeEntry{OID: blobID, Path: "dup", Mode: "100644"},
				gittest.TreeEntry{OID: blobID, Path: "dup", Mode: "100644"},
			),
			gittest.WithParents(oldCommitID),
		)

		return []refUpdate{
			{
				ref:  git.DefaultRef,
				from: oldCommitID,
				to:   newCommitID,
			},
		}
	})

	client := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	response := push.perform(t, stream, &gitalypb.PostReceivePackRequest{
		Repository:   repo,
		GlId:         "user-123",
		GlRepository: "project-456",
	})

	require.Contains(t, response, "duplicateEntries: contains duplicate file")
}

func TestPostReceivePack_hooks(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionProcReceive).Run(t, testPostReceivePackHooks)
}

func testPostReceivePackHooks(t *testing.T, ctx context.Context) {
	t.Parallel()

	const (
		secretToken  = "secret token"
		glRepository = "some_repo"
		glID         = "key-123"
	)

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)
	cfg.GitlabShell.Dir = testhelper.TempDir(t)
	cfg.Auth.Token = "abc123"
	cfg.Gitlab.SecretFile = gitlab.WriteShellSecretFile(t, cfg.GitlabShell.Dir, secretToken)

	testcfg.BuildGitalyHooks(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
	push := setupSimplePush(t, ctx, cfg, repoPath, git.DefaultRef)

	hookContent := []byte("#!/bin/sh\necho 'Hello stdout';exit 0")
	gittest.WriteCustomHook(t, repoPath, "post-receive", hookContent)

	changes := fmt.Sprintf("%s %s %s\n", push.refUpdates[0].from, push.refUpdates[0].to, push.refUpdates[0].ref)

	var cleanup func()
	cfg.Gitlab.URL, cleanup = gitlab.NewTestServer(t, gitlab.TestServerOptions{
		User:                        "",
		Password:                    "",
		SecretToken:                 secretToken,
		GLID:                        glID,
		GLRepository:                glRepository,
		Changes:                     changes,
		PostReceiveCounterDecreased: true,
		Protocol:                    "http",
	})
	defer cleanup()

	gittest.WriteCheckNewObjectExistsHook(t, repoPath)

	client := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	response := push.perform(t, stream, &gitalypb.PostReceivePackRequest{
		Repository:   repo,
		GlId:         glID,
		GlRepository: glRepository,
	})

	requireSidebandContains(t, []string{
		gittest.Pktlinef(t, "\x01%s", strings.Join([]string{
			gittest.Pktlinef(t, "unpack ok\n"),
			gittest.Pktlinef(t, "ok %s\n", git.DefaultRef),
			"0000",
		}, "")),
		gittest.Pktlinef(t, "\x02Hello stdout\n"),
	}, response, false)
}

func TestPostReceivePack_transactionsViaPraefect(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionProcReceive).Run(t, testPostReceivePackTransactionsViaPraefect)
}

func testPostReceivePackTransactionsViaPraefect(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))
	push := setupSimplePush(t, ctx, cfg, repoPath, "refs/heads/branch")

	testcfg.BuildGitalyHooks(t, cfg)

	opts := gitlab.TestServerOptions{
		User:         "gitlab_user-1234",
		Password:     "gitlabsecret9887",
		SecretToken:  "secret token",
		GLID:         "key-1234",
		GLRepository: "some_repo",
		RepoPath:     repoPath,
	}

	serverURL, cleanup := gitlab.NewTestServer(t, opts)
	defer cleanup()

	cfg.GitlabShell.Dir = testhelper.TempDir(t)
	cfg.Gitlab.URL = serverURL
	cfg.Gitlab.HTTPSettings.User = opts.User
	cfg.Gitlab.HTTPSettings.Password = opts.Password
	cfg.Gitlab.SecretFile = filepath.Join(cfg.GitlabShell.Dir, ".gitlab_shell_secret")

	gitlab.WriteShellSecretFile(t, cfg.GitlabShell.Dir, opts.SecretToken)

	client := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	response := push.perform(t, stream, &gitalypb.PostReceivePackRequest{
		Repository:   repo,
		GlId:         opts.GLID,
		GlRepository: opts.GLRepository,
	})

	requireSideband(t, []string{
		gittest.Pktlinef(t, "\x01%s", strings.Join([]string{
			gittest.Pktlinef(t, "unpack ok\n"),
			gittest.Pktlinef(t, "ok refs/heads/branch\n"),
			"0000",
		}, "")),
	}, response)
}

type testTransactionServer struct {
	gitalypb.UnimplementedRefTransactionServer
	called int
}

func (t *testTransactionServer) VoteTransaction(ctx context.Context, in *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
	t.called++
	return &gitalypb.VoteTransactionResponse{
		State: gitalypb.VoteTransactionResponse_COMMIT,
	}, nil
}

func TestPostReceivePack_referenceTransactionHook(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionProcReceive).Run(t, testPostReceivePackReferenceTransactionHook)
}

func testPostReceivePackReferenceTransactionHook(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctxWithoutTransaction := ctx

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)

	refTransactionServer := &testTransactionServer{}

	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithDisablePraefect(),
	})
	cfg.SocketPath = server.Address()

	ctx, err := txinfo.InjectTransaction(ctxWithoutTransaction, 1234, "primary", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	client := newMuxedSmartHTTPClient(t, ctx, server.Address(), cfg.Auth.Token, func() backchannel.Server {
		srv := grpc.NewServer()
		gitalypb.RegisterRefTransactionServer(srv, refTransactionServer)
		return srv
	})

	t.Run("update", func(t *testing.T) {
		stream, err := client.PostReceivePack(ctx)
		require.NoError(t, err)

		repo, repoPath := gittest.CreateRepository(t, ctxWithoutTransaction, cfg)
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))
		push := setupSimplePush(t, ctx, cfg, repoPath, "refs/heads/branch")

		response := push.perform(t, stream, &gitalypb.PostReceivePackRequest{
			Repository:   repo,
			GlId:         "key-1234",
			GlRepository: "some_repo",
		})

		requireSideband(t, []string{
			gittest.Pktlinef(t, "\x01%s", strings.Join([]string{
				gittest.Pktlinef(t, "unpack ok\n"),
				gittest.Pktlinef(t, "ok refs/heads/branch\n"),
				"0000",
			}, "")),
		}, response)
		require.Equal(t, 6, refTransactionServer.called)
	})

	t.Run("delete", func(t *testing.T) {
		refTransactionServer.called = 0

		stream, err := client.PostReceivePack(ctx)
		require.NoError(t, err)

		repo, repoPath := gittest.CreateRepository(t, ctxWithoutTransaction, cfg)

		// Create a new branch which we're about to delete.
		oldObjectID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("delete-me"))
		push := setupPush(t, ctx, cfg, repoPath, func(repoPath string) []refUpdate {
			return []refUpdate{
				{
					ref:  "refs/heads/delete-me",
					from: oldObjectID,
					to:   gittest.DefaultObjectHash.ZeroOID,
				},
			}
		})

		// Pack references because this used to generate two transactions: one for the packed-refs file and one
		// for the loose ref. We only expect a single transaction though, given that the
		// packed-refs transaction should get filtered out.
		gittest.Exec(t, cfg, "-C", repoPath, "pack-refs", "--all")

		response := push.perform(t, stream, &gitalypb.PostReceivePackRequest{
			Repository:   repo,
			GlId:         "key-1234",
			GlRepository: "some_repo",
		})

		requireSideband(t, []string{
			"0033\x01000eunpack ok\n001cok refs/heads/delete-me\n0000",
		}, response)
		require.Equal(t, 6, refTransactionServer.called)
	})
}

func TestPostReceivePack_notAllowed(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionProcReceive).Run(t, testPostReceivePackNotAllowed)
}

func testPostReceivePackNotAllowed(t *testing.T, ctx context.Context) {
	cfg := testcfg.Build(t)

	testcfg.BuildGitalyHooks(t, cfg)

	refTransactionServer := &testTransactionServer{}

	hookManager := gitalyhook.NewMockManager(
		t,
		func(
			t *testing.T,
			ctx context.Context,
			repo *gitalypb.Repository,
			pushOptions, env []string,
			stdin io.Reader, stdout, stderr io.Writer,
		) error {
			return errors.New("not allowed")
		},
		gitalyhook.NopPostReceive,
		gitalyhook.NopUpdate,
		gitalyhook.NopReferenceTransaction,
		gitalyhook.NewProcReceiveRegistry(),
	)

	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithDisablePraefect(), testserver.WithHookManager(hookManager),
	})
	cfg.SocketPath = server.Address()

	ctxWithoutTransaction := ctx
	ctx, err := txinfo.InjectTransaction(ctxWithoutTransaction, 1234, "primary", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	client := newMuxedSmartHTTPClient(t, ctx, server.Address(), cfg.Auth.Token, func() backchannel.Server {
		srv := grpc.NewServer()
		gitalypb.RegisterRefTransactionServer(srv, refTransactionServer)
		return srv
	})

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	repo, repoPath := gittest.CreateRepository(t, ctxWithoutTransaction, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))
	push := setupSimplePush(t, ctx, cfg, repoPath, "refs/heads/branch")

	push.perform(t, stream, &gitalypb.PostReceivePackRequest{
		Repository:   repo,
		GlId:         "key-1234",
		GlRepository: "some_repo",
	})

	require.Equal(t, 1, refTransactionServer.called)
}

type refUpdate struct {
	ref      git.ReferenceName
	from, to git.ObjectID
}

type push struct {
	body       bytes.Buffer
	refUpdates []refUpdate
}

// setupPush sets up a push by creating the packfile negotiation as well as the packfile that result from the changes
// created by the callback function.
func setupPush(t *testing.T, ctx context.Context, cfg config.Cfg, sourceRepoPath string, createChanges func(repoPath string) []refUpdate) push {
	t.Helper()

	repoPath := filepath.Join(cfg.Storages[0].Path, gittest.NewRepositoryName(t))
	gittest.Exec(t, cfg, "clone", "--bare", "--mirror", sourceRepoPath, repoPath)

	refUpdates := createChanges(repoPath)

	// ReceivePack request is a packet line followed by a packet flush, then the pack file of the objects we want to push.
	// This is explained a bit in https://git-scm.com/book/en/v2/Git-Internals-Transfer-Protocols#_uploading_data
	// We form the packet line the same way git executable does: https://github.com/git/git/blob/d1a13d3fcb252631361a961cb5e2bf10ed467cba/send-pack.c#L524-L527
	var request bytes.Buffer
	var haves, wants []git.ObjectID
	for i, refUpdate := range refUpdates {
		pktline := fmt.Sprintf("%s %s %s", refUpdate.from, refUpdate.to, refUpdate.ref)
		if i == 0 {
			pktline += fmt.Sprintf("\x00 report-status side-band-64k object-format=%s agent=git/2.12.0", gittest.DefaultObjectHash.Format)
		}
		gittest.WritePktlineString(t, &request, pktline)

		if refUpdate.from != gittest.DefaultObjectHash.ZeroOID {
			haves = append(haves, refUpdate.from)
		}
		if refUpdate.to != gittest.DefaultObjectHash.ZeroOID {
			wants = append(wants, refUpdate.to)
		}
	}
	gittest.WritePktlineFlush(t, &request)

	// We need to get a pack file containing the objects we want to push, so we use git pack-objects
	// which expects a list of revisions passed through standard input. The list format means
	// pack the objects needed if I have oldHead but not newHead (think of it from the perspective of the remote repo).
	// For more info, check the man pages of both `git-pack-objects` and `git-rev-list --objects`.
	var stdin bytes.Buffer
	for _, have := range haves {
		_, err := stdin.Write([]byte("^" + have + "\n"))
		require.NoError(t, err)
	}
	for _, want := range wants {
		_, err := stdin.Write([]byte(want + "\n"))
		require.NoError(t, err)
	}

	// The options passed are the same ones used when doing an actual push.
	gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: &stdin, Stdout: &request},
		"-C", repoPath, "pack-objects", "--stdout", "--revs", "--thin", "--delta-base-offset", "-q",
	)

	return push{
		body:       request,
		refUpdates: refUpdates,
	}
}

// setupSimplePush sets up a simple push by creating a new fast-forwardable commit on top of the given, preexisting
// reference.
func setupSimplePush(t *testing.T, ctx context.Context, cfg config.Cfg, sourceRepoPath string, reference git.ReferenceName) push {
	oldCommitID := gittest.ResolveRevision(t, cfg, sourceRepoPath, string(reference))

	return setupPush(t, ctx, cfg, sourceRepoPath, func(repoPath string) []refUpdate {
		return []refUpdate{
			{
				ref:  reference,
				from: oldCommitID,
				to:   gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(oldCommitID)),
			},
		}
	})
}

func (p push) perform(t *testing.T, stream gitalypb.SmartHTTPService_PostReceivePackClient, firstRequest *gitalypb.PostReceivePackRequest) string {
	require.NoError(t, stream.Send(firstRequest))

	sw := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.PostReceivePackRequest{Data: p})
	})
	_, err := io.Copy(sw, &p.body)
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())

	var response bytes.Buffer
	rr := streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetData(), err
	})
	_, err = io.Copy(&response, rr)
	require.NoError(t, err)

	return response.String()
}

func drainPostReceivePackResponse(stream gitalypb.SmartHTTPService_PostReceivePackClient) error {
	var err error
	for err == nil {
		_, err = stream.Recv()
	}
	return err
}

// requireSideband compares the actual sideband data to expected sideband data. This function is
// required to filter out any keep-alive packets which Git may send over the sideband and which are
// kind of unpredictable for us.
func requireSideband(tb testing.TB, expectedSidebandMessages []string, actualInput string) {
	requireSidebandContains(tb, expectedSidebandMessages, actualInput, true)
}

func requireSidebandContains(tb testing.TB, expectedSidebandMessages []string, actualInput string, exact bool) {
	tb.Helper()

	scanner := pktline.NewScanner(strings.NewReader(actualInput))

	var actualSidebandMessages []string
	for scanner.Scan() {
		payload := scanner.Bytes()

		// Flush packets terminate the communication via side-channels, so we expect them to
		// come.
		if pktline.IsFlush(payload) {
			if exact {
				require.Equal(tb, expectedSidebandMessages, actualSidebandMessages)
			} else {
				require.ElementsMatch(tb, expectedSidebandMessages, actualSidebandMessages)
			}

			return
		}

		// git-receive-pack(1) by default sends keep-alive packets every 5 seconds after it
		// has received the full packfile. We must filter out these keep-alive packets to
		// not break tests on machines which are really slow to execute.
		if string(payload) == "0005\x01" {
			continue
		}

		actualSidebandMessages = append(actualSidebandMessages, string(payload))
	}

	require.FailNow(tb, "expected to receive a flush to terminate the protocol")
}
