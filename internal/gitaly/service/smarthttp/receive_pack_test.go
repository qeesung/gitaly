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
	"gitlab.com/gitlab-org/gitaly/v14/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v14/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v14/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v14/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v14/streamio"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

const (
	uploadPackCapabilities = "report-status side-band-64k agent=git/2.12.0"
)

func TestSuccessfulReceivePackRequest(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.GitlabShell.Dir = "/foo/bar/gitlab-shell"
	gitCmdFactory, hookOutputFile := gittest.CaptureHookEnv(t, cfg)

	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithGitCommandFactory(gitCmdFactory),
	})
	cfg.SocketPath = server.Address()

	ctx := testhelper.Context(t)
	repo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	client, conn := newSmartHTTPClient(t, server.Address(), cfg.Auth.Token)
	defer conn.Close()

	// Below, we test whether extracting the hooks payload leads to the expected
	// results. Part of this payload are feature flags, so we need to get them into a
	// deterministic state such that we can compare them properly. While we wouldn't
	// need to inject them in "normal" Gitaly tests, Praefect will inject all unset
	// feature flags and set them to `false` -- as a result, we have a mismatch between
	// the context's feature flags we see here and the context's metadata as it would
	// arrive on the proxied Gitaly. To fix this, we thus inject all feature flags
	// explicitly here.
	for _, ff := range featureflag.All {
		ctx = featureflag.OutgoingCtxWithFeatureFlag(ctx, ff, true)
		ctx = featureflag.IncomingCtxWithFeatureFlag(ctx, ff, true)
	}

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	push := newTestPush(t, cfg, nil)

	projectPath := "project/path"

	repo.GlProjectPath = projectPath
	firstRequest := &gitalypb.PostReceivePackRequest{Repository: repo, GlUsername: "user", GlId: "123", GlRepository: "project-456"}
	response := doPush(t, stream, firstRequest, push.body)

	expectedResponse := "0049\x01000eunpack ok\n0019ok refs/heads/master\n0019ok refs/heads/branch\n00000000"
	require.Equal(t, expectedResponse, string(response), "Expected response to be %q, got %q", expectedResponse, response)

	// The fact that this command succeeds means that we got the commit correctly, no further checks should be needed.
	gittest.Exec(t, cfg, "-C", repoPath, "show", push.newHead)

	envData := testhelper.MustReadFile(t, hookOutputFile)
	payload, err := git.HooksPayloadFromEnv(strings.Split(string(envData), "\n"))
	require.NoError(t, err)

	// Compare the repository up front so that we can use require.Equal for
	// the remaining values.
	testhelper.ProtoEqual(t, gittest.RewrittenRepository(ctx, t, cfg, repo), payload.Repo)
	payload.Repo = nil

	// If running tests with Praefect, then the transaction would be set, but we have no way of
	// figuring out their actual contents. So let's just remove it, too.
	payload.Transaction = nil

	require.Equal(t, git.HooksPayload{
		RuntimeDir:          cfg.RuntimeDir,
		InternalSocket:      cfg.InternalSocketPath(),
		InternalSocketToken: cfg.Auth.Token,
		ReceiveHooksPayload: &git.ReceiveHooksPayload{
			UserID:   "123",
			Username: "user",
			Protocol: "http",
		},
		RequestedHooks: git.ReceivePackHooks,
		FeatureFlags:   featureflag.RawFromContext(ctx),
	}, payload)
}

func TestReceivePackHiddenRefs(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	ctx := testhelper.Context(t)
	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})
	repoProto.GlProjectPath = "project/path"

	testcfg.BuildGitalyHooks(t, cfg)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)
	oldHead, err := repo.ResolveRevision(ctx, "HEAD~")
	require.NoError(t, err)
	newHead, err := repo.ResolveRevision(ctx, "HEAD")
	require.NoError(t, err)

	client, conn := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)
	defer conn.Close()

	for _, ref := range []string{
		"refs/environments/1",
		"refs/merge-requests/1/head",
		"refs/merge-requests/1/merge",
		"refs/pipelines/1",
	} {
		t.Run(ref, func(t *testing.T) {
			request := &bytes.Buffer{}
			gittest.WritePktlineString(t, request, fmt.Sprintf("%s %s %s\x00 %s",
				oldHead, newHead, ref, uploadPackCapabilities))
			gittest.WritePktlineFlush(t, request)

			// The options passed are the same ones used when doing an actual push.
			revisions := strings.NewReader(fmt.Sprintf("^%s\n%s\n", oldHead, newHead))
			pack := gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: revisions},
				"-C", repoPath, "pack-objects", "--stdout", "--revs", "--thin", "--delta-base-offset", "-q",
			)
			request.Write(pack)

			stream, err := client.PostReceivePack(ctx)
			require.NoError(t, err)

			response := doPush(t, stream, &gitalypb.PostReceivePackRequest{
				Repository: repoProto, GlUsername: "user", GlId: "123", GlRepository: "project-456",
			}, request)

			require.Contains(t, string(response), fmt.Sprintf("%s deny updating a hidden ref", ref))
		})
	}
}

func TestSuccessfulReceivePackRequestWithGitProtocol(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)

	testcfg.BuildGitalyHooks(t, cfg)
	ctx := testhelper.Context(t)

	gitCmdFactory, readProto := gittest.NewProtocolDetectingCommandFactory(ctx, t, cfg)

	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithGitCommandFactory(gitCmdFactory),
	})

	cfg.SocketPath = server.Address()

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	client, conn := newSmartHTTPClient(t, server.Address(), cfg.Auth.Token)
	defer conn.Close()

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	push := newTestPush(t, cfg, nil)
	firstRequest := &gitalypb.PostReceivePackRequest{Repository: repo, GlId: "user-123", GlRepository: "project-123", GitProtocol: git.ProtocolV2}
	doPush(t, stream, firstRequest, push.body)

	envData := readProto()
	require.Equal(t, fmt.Sprintf("GIT_PROTOCOL=%s\n", git.ProtocolV2), envData)

	// The fact that this command succeeds means that we got the commit correctly, no further checks should be needed.
	gittest.Exec(t, cfg, "-C", repoPath, "show", push.newHead)
}

func TestFailedReceivePackRequestWithGitOpts(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)

	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	ctx := testhelper.Context(t)
	repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	client, conn := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)
	defer conn.Close()

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	push := newTestPush(t, cfg, nil)
	firstRequest := &gitalypb.PostReceivePackRequest{Repository: repo, GlId: "user-123", GlRepository: "project-123", GitConfigOptions: []string{"receive.MaxInputSize=1"}}
	response := doPush(t, stream, firstRequest, push.body)

	expectedResponse := "002e\x02fatal: pack exceeds maximum allowed size\n0081\x010028unpack unpack-objects abnormal exit\n0028ng refs/heads/master unpacker error\n0028ng refs/heads/branch unpacker error\n00000000"
	require.Equal(t, expectedResponse, string(response), "Expected response to be %q, got %q", expectedResponse, response)
}

func TestFailedReceivePackRequestDueToHooksFailure(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg, git.WithHooksPath(testhelper.TempDir(t)))
	ctx := testhelper.Context(t)

	hookContent := []byte("#!/bin/sh\nexit 1")
	require.NoError(t, os.WriteFile(filepath.Join(gitCmdFactory.HooksPath(ctx), "pre-receive"), hookContent, 0o755))

	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithGitCommandFactory(gitCmdFactory),
	})
	cfg.SocketPath = server.Address()

	repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	client, conn := newSmartHTTPClient(t, server.Address(), cfg.Auth.Token)
	defer conn.Close()

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	push := newTestPush(t, cfg, nil)
	firstRequest := &gitalypb.PostReceivePackRequest{Repository: repo, GlId: "user-123", GlRepository: "project-123"}
	response := doPush(t, stream, firstRequest, push.body)

	expectedResponse := "007d\x01000eunpack ok\n0033ng refs/heads/master pre-receive hook declined\n0033ng refs/heads/branch pre-receive hook declined\n00000000"
	require.Equal(t, expectedResponse, string(response), "Expected response to be %q, got %q", expectedResponse, response)
}

func doPush(t *testing.T, stream gitalypb.SmartHTTPService_PostReceivePackClient, firstRequest *gitalypb.PostReceivePackRequest, body io.Reader) []byte {
	require.NoError(t, stream.Send(firstRequest))

	sw := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.PostReceivePackRequest{Data: p})
	})
	_, err := io.Copy(sw, body)
	require.NoError(t, err)

	require.NoError(t, stream.CloseSend())

	responseBuffer := bytes.Buffer{}
	rr := streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetData(), err
	})
	_, err = io.Copy(&responseBuffer, rr)
	require.NoError(t, err)

	return responseBuffer.Bytes()
}

type pushData struct {
	newHead string
	body    io.Reader
}

func newTestPush(t *testing.T, cfg config.Cfg, fileContents []byte) *pushData {
	_, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
		WithWorktree: true,
	})

	oldHead, newHead := createCommit(t, cfg, repoPath, fileContents)

	// ReceivePack request is a packet line followed by a packet flush, then the pack file of the objects we want to push.
	// This is explained a bit in https://git-scm.com/book/en/v2/Git-Internals-Transfer-Protocols#_uploading_data
	// We form the packet line the same way git executable does: https://github.com/git/git/blob/d1a13d3fcb252631361a961cb5e2bf10ed467cba/send-pack.c#L524-L527
	requestBuffer := &bytes.Buffer{}

	pkt := fmt.Sprintf("%s %s refs/heads/master\x00 %s", oldHead, newHead, uploadPackCapabilities)
	fmt.Fprintf(requestBuffer, "%04x%s", len(pkt)+4, pkt)

	pkt = fmt.Sprintf("%s %s refs/heads/branch", git.ZeroOID, newHead)
	fmt.Fprintf(requestBuffer, "%04x%s", len(pkt)+4, pkt)

	fmt.Fprintf(requestBuffer, "%s", pktFlushStr)

	// We need to get a pack file containing the objects we want to push, so we use git pack-objects
	// which expects a list of revisions passed through standard input. The list format means
	// pack the objects needed if I have oldHead but not newHead (think of it from the perspective of the remote repo).
	// For more info, check the man pages of both `git-pack-objects` and `git-rev-list --objects`.
	stdin := strings.NewReader(fmt.Sprintf("^%s\n%s\n", oldHead, newHead))

	// The options passed are the same ones used when doing an actual push.
	pack := gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: stdin},
		"-C", repoPath, "pack-objects", "--stdout", "--revs", "--thin", "--delta-base-offset", "-q",
	)
	requestBuffer.Write(pack)

	return &pushData{newHead: newHead, body: requestBuffer}
}

// createCommit creates a commit on HEAD with a file containing the
// specified contents.
func createCommit(t *testing.T, cfg config.Cfg, repoPath string, fileContents []byte) (oldHead string, newHead string) {
	commitMsg := fmt.Sprintf("Testing ReceivePack RPC around %d", time.Now().Unix())
	committerName := "Scrooge McDuck"
	committerEmail := "scrooge@mcduck.com"

	// The latest commit ID on the remote repo
	oldHead = text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "master"))

	changedFile := "README.md"
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, changedFile), fileContents, 0o644))

	gittest.Exec(t, cfg, "-C", repoPath, "add", changedFile)
	gittest.Exec(t, cfg, "-C", repoPath,
		"-c", fmt.Sprintf("user.name=%s", committerName),
		"-c", fmt.Sprintf("user.email=%s", committerEmail),
		"commit", "-m", commitMsg)

	// The commit ID we want to push to the remote repo
	newHead = text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "master"))

	return oldHead, newHead
}

func TestFailedReceivePackRequestDueToValidationError(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)

	serverSocketPath := runSmartHTTPServer(t, cfg)

	client, conn := newSmartHTTPClient(t, serverSocketPath, cfg.Auth.Token)
	defer conn.Close()

	for _, tc := range []struct {
		desc    string
		request *gitalypb.PostReceivePackRequest
		code    codes.Code
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
			code: codes.InvalidArgument,
		},
		{
			desc:    "Repository is nil",
			request: &gitalypb.PostReceivePackRequest{Repository: nil, GlId: "user-123"},
			code:    codes.InvalidArgument,
		},
		{
			desc: "Empty GlId",
			request: &gitalypb.PostReceivePackRequest{
				Repository: &gitalypb.Repository{
					StorageName:  cfg.Storages[0].Name,
					RelativePath: "path/to/repo",
				},
				GlId: "",
			},
			code: func() codes.Code {
				if testhelper.IsPraefectEnabled() {
					return codes.NotFound
				}

				return codes.InvalidArgument
			}(),
		},
		{
			desc: "Data exists on first request",
			request: &gitalypb.PostReceivePackRequest{
				Repository: &gitalypb.Repository{
					StorageName:  cfg.Storages[0].Name,
					RelativePath: "path/to/repo",
				},
				GlId: "user-123",
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
			ctx := testhelper.Context(t)
			stream, err := client.PostReceivePack(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(tc.request))
			require.NoError(t, stream.CloseSend())

			err = drainPostReceivePackResponse(stream)
			testhelper.RequireGrpcCode(t, err, tc.code)
		})
	}
}

func TestPostReceivePack_invalidObjects(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)

	gitCmdFactory, _ := gittest.CaptureHookEnv(t, cfg)
	server := startSmartHTTPServerWithOptions(t, cfg, nil, []testserver.GitalyServerOpt{
		testserver.WithGitCommandFactory(gitCmdFactory),
	})
	cfg.SocketPath = server.Address()

	ctx := testhelper.Context(t)
	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	repo := localrepo.NewTestRepo(t, cfg, repoProto)
	_, localRepoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	client, conn := newSmartHTTPClient(t, server.Address(), cfg.Auth.Token)
	defer conn.Close()

	head := text.ChompBytes(gittest.Exec(t, cfg, "-C", localRepoPath, "rev-parse", "HEAD"))
	tree := text.ChompBytes(gittest.Exec(t, cfg, "-C", localRepoPath, "rev-parse", "HEAD^{tree}"))

	for _, tc := range []struct {
		desc             string
		prepareCommit    func(t *testing.T, repoPath string) bytes.Buffer
		expectedResponse string
		expectObject     bool
	}{
		{
			desc: "invalid timezone",
			prepareCommit: func(t *testing.T, repoPath string) bytes.Buffer {
				var buf bytes.Buffer
				buf.WriteString("tree " + tree + "\n")
				buf.WriteString("parent " + head + "\n")
				buf.WriteString("author Au Thor <author@example.com> 1313584730 +051800\n")
				buf.WriteString("committer Au Thor <author@example.com> 1313584730 +051800\n")
				buf.WriteString("\n")
				buf.WriteString("Commit message\n")
				return buf
			},
			expectedResponse: "0030\x01000eunpack ok\n0019ok refs/heads/master\n00000000",
			expectObject:     true,
		},
		{
			desc: "missing author and committer date",
			prepareCommit: func(t *testing.T, repoPath string) bytes.Buffer {
				var buf bytes.Buffer
				buf.WriteString("tree " + tree + "\n")
				buf.WriteString("parent " + head + "\n")
				buf.WriteString("author Au Thor <author@example.com>\n")
				buf.WriteString("committer Au Thor <author@example.com>\n")
				buf.WriteString("\n")
				buf.WriteString("Commit message\n")
				return buf
			},
			expectedResponse: "0030\x01000eunpack ok\n0019ok refs/heads/master\n00000000",
			expectObject:     true,
		},
		{
			desc: "zero-padded file mode",
			prepareCommit: func(t *testing.T, repoPath string) bytes.Buffer {
				subtree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Mode: "100644", Path: "file", Content: "content"},
				})
				subtreeID, err := subtree.Bytes()
				require.NoError(t, err)

				var treeContents bytes.Buffer
				treeContents.WriteString("040000 subdir\x00")
				treeContents.Write(subtreeID)

				brokenTree := gittest.ExecOpts(t, cfg, gittest.ExecConfig{
					Stdin: &treeContents,
				}, "-C", repoPath, "hash-object", "-w", "-t", "tree", "--stdin")

				var buf bytes.Buffer
				buf.WriteString("tree " + text.ChompBytes(brokenTree) + "\n")
				buf.WriteString("parent " + head + "\n")
				buf.WriteString("author Au Thor <author@example.com>\n")
				buf.WriteString("committer Au Thor <author@example.com>\n")
				buf.WriteString("\n")
				buf.WriteString("Commit message\n")
				return buf
			},
			expectedResponse: "0030\x01000eunpack ok\n0019ok refs/heads/master\n00000000",
			expectObject:     true,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			commitBuffer := tc.prepareCommit(t, localRepoPath)
			commitID := text.ChompBytes(gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: &commitBuffer},
				"-C", localRepoPath, "hash-object", "-t", "commit", "--stdin", "-w",
			))

			currentHead := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "HEAD"))

			stdin := strings.NewReader(fmt.Sprintf("^%s\n%s\n", currentHead, commitID))
			pack := gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: stdin},
				"-C", localRepoPath, "pack-objects", "--stdout", "--revs", "--thin", "--delta-base-offset", "-q",
			)

			pkt := fmt.Sprintf("%s %s refs/heads/master\x00 %s", currentHead, commitID, "report-status side-band-64k agent=git/2.12.0")
			body := &bytes.Buffer{}
			fmt.Fprintf(body, "%04x%s%s", len(pkt)+4, pkt, pktFlushStr)
			body.Write(pack)

			stream, err := client.PostReceivePack(ctx)
			require.NoError(t, err)
			firstRequest := &gitalypb.PostReceivePackRequest{
				Repository:   repoProto,
				GlId:         "user-123",
				GlRepository: "project-456",
			}
			response := doPush(t, stream, firstRequest, body)

			require.Contains(t, string(response), tc.expectedResponse)

			exists, err := repo.HasRevision(ctx, git.Revision(commitID+"^{commit}"))
			require.NoError(t, err)
			require.Equal(t, tc.expectObject, exists)
		})
	}
}

func TestReceivePackFsck(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	ctx := testhelper.Context(t)
	repo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	testcfg.BuildGitalyHooks(t, cfg)

	head := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "HEAD"))

	// We're creating a new commit which has a root tree with duplicate entries. git-mktree(1)
	// allows us to create these trees just fine, but git-fsck(1) complains.
	commit := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithTreeEntries(
			gittest.TreeEntry{OID: "4b825dc642cb6eb9a060e54bf8d69288fbee4904", Path: "dup", Mode: "040000"},
			gittest.TreeEntry{OID: "4b825dc642cb6eb9a060e54bf8d69288fbee4904", Path: "dup", Mode: "040000"},
		),
	)

	stdin := strings.NewReader(fmt.Sprintf("^%s\n%s\n", head, commit))
	pack := gittest.ExecOpts(t, cfg, gittest.ExecConfig{Stdin: stdin},
		"-C", repoPath, "pack-objects", "--stdout", "--revs", "--thin", "--delta-base-offset", "-q",
	)

	var body bytes.Buffer
	gittest.WritePktlineString(t, &body, fmt.Sprintf("%s %s refs/heads/master\x00 %s", head, commit, "report-status side-band-64k agent=git/2.12.0"))
	gittest.WritePktlineFlush(t, &body)
	_, err := body.Write(pack)
	require.NoError(t, err)

	client, conn := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)
	defer conn.Close()

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	response := doPush(t, stream, &gitalypb.PostReceivePackRequest{
		Repository:   repo,
		GlId:         "user-123",
		GlRepository: "project-456",
	}, &body)

	require.Contains(t, string(response), "duplicateEntries: contains duplicate file entries")
}

func drainPostReceivePackResponse(stream gitalypb.SmartHTTPService_PostReceivePackClient) error {
	var err error
	for err == nil {
		_, err = stream.Recv()
	}
	return err
}

func TestPostReceivePackToHooks(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	ctx := testhelper.Context(t)
	repo, testRepoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	testcfg.BuildGitalyHooks(t, cfg)

	const (
		secretToken  = "secret token"
		glRepository = "some_repo"
		glID         = "key-123"
	)

	var cleanup func()
	cfg.GitlabShell.Dir = testhelper.TempDir(t)

	cfg.Auth.Token = "abc123"
	cfg.Gitlab.SecretFile = gitlab.WriteShellSecretFile(t, cfg.GitlabShell.Dir, secretToken)

	push := newTestPush(t, cfg, nil)
	oldHead := text.ChompBytes(gittest.Exec(t, cfg, "-C", testRepoPath, "rev-parse", "HEAD"))

	changes := fmt.Sprintf("%s %s refs/heads/master\n", oldHead, push.newHead)

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

	gittest.WriteCheckNewObjectExistsHook(t, testRepoPath)

	client, conn := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)
	defer conn.Close()

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	firstRequest := &gitalypb.PostReceivePackRequest{
		Repository:   repo,
		GlId:         glID,
		GlRepository: glRepository,
	}

	response := doPush(t, stream, firstRequest, push.body)

	expectedResponse := "0049\x01000eunpack ok\n0019ok refs/heads/master\n0019ok refs/heads/branch\n00000000"
	require.Equal(t, expectedResponse, string(response), "Expected response to be %q, got %q", expectedResponse, response)
	require.Equal(t, io.EOF, drainPostReceivePackResponse(stream))
}

func TestPostReceiveWithTransactionsViaPraefect(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSmartHTTPServer(t, cfg)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	testcfg.BuildGitalyHooks(t, cfg)

	secretToken := "secret token"
	glID := "key-1234"
	glRepository := "some_repo"
	gitlabUser := "gitlab_user-1234"
	gitlabPassword := "gitlabsecret9887"

	opts := gitlab.TestServerOptions{
		User:         gitlabUser,
		Password:     gitlabPassword,
		SecretToken:  secretToken,
		GLID:         glID,
		GLRepository: glRepository,
		RepoPath:     repoPath,
	}

	serverURL, cleanup := gitlab.NewTestServer(t, opts)
	defer cleanup()

	gitlabShellDir := testhelper.TempDir(t)
	cfg.GitlabShell.Dir = gitlabShellDir
	cfg.Gitlab.URL = serverURL
	cfg.Gitlab.HTTPSettings.User = gitlabUser
	cfg.Gitlab.HTTPSettings.Password = gitlabPassword
	cfg.Gitlab.SecretFile = filepath.Join(gitlabShellDir, ".gitlab_shell_secret")

	gitlab.WriteShellSecretFile(t, gitlabShellDir, secretToken)

	client, conn := newSmartHTTPClient(t, cfg.SocketPath, cfg.Auth.Token)
	defer conn.Close()

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	push := newTestPush(t, cfg, nil)
	request := &gitalypb.PostReceivePackRequest{Repository: repo, GlId: glID, GlRepository: glRepository}
	response := doPush(t, stream, request, push.body)

	expectedResponse := "0049\x01000eunpack ok\n0019ok refs/heads/master\n0019ok refs/heads/branch\n00000000"
	require.Equal(t, expectedResponse, string(response), "Expected response to be %q, got %q", expectedResponse, response)
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

func TestPostReceiveWithReferenceTransactionHook(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)

	testcfg.BuildGitalyHooks(t, cfg)

	refTransactionServer := &testTransactionServer{}

	addr := testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterSmartHTTPServiceServer(srv, NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetTxManager(),
			deps.GetDiskCache(),
		))
		gitalypb.RegisterHookServiceServer(srv, hook.NewServer(deps.GetHookManager(), deps.GetGitCmdFactory(), deps.GetPackObjectsCache()))
	}, testserver.WithDisablePraefect())
	ctx := testhelper.Context(t)

	ctx, err := txinfo.InjectTransaction(ctx, 1234, "primary", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	client := newMuxedSmartHTTPClient(t, ctx, addr, cfg.Auth.Token, func() backchannel.Server {
		srv := grpc.NewServer()
		gitalypb.RegisterRefTransactionServer(srv, refTransactionServer)
		return srv
	})

	t.Run("update", func(t *testing.T) {
		stream, err := client.PostReceivePack(ctx)
		require.NoError(t, err)

		repo, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])

		request := &gitalypb.PostReceivePackRequest{Repository: repo, GlId: "key-1234", GlRepository: "some_repo"}
		response := doPush(t, stream, request, newTestPush(t, cfg, nil).body)

		expectedResponse := "0049\x01000eunpack ok\n0019ok refs/heads/master\n0019ok refs/heads/branch\n00000000"
		require.Equal(t, expectedResponse, string(response), "Expected response to be %q, got %q", expectedResponse, response)
		require.Equal(t, 5, refTransactionServer.called)
	})

	t.Run("delete", func(t *testing.T) {
		refTransactionServer.called = 0

		stream, err := client.PostReceivePack(ctx)
		require.NoError(t, err)

		repo, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0])

		// Create a new branch which we're about to delete. We also pack references because
		// this used to generate two transactions: one for the packed-refs file and one for
		// the loose ref. We only expect a single transaction though, given that the
		// packed-refs transaction should get filtered out.
		gittest.Exec(t, cfg, "-C", repoPath, "branch", "delete-me")
		gittest.Exec(t, cfg, "-C", repoPath, "pack-refs", "--all")
		branchOID := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "refs/heads/delete-me"))

		uploadPackData := &bytes.Buffer{}
		gittest.WritePktlineString(t, uploadPackData, fmt.Sprintf("%s %s refs/heads/delete-me\x00 %s", branchOID, git.ZeroOID.String(), uploadPackCapabilities))
		gittest.WritePktlineFlush(t, uploadPackData)

		request := &gitalypb.PostReceivePackRequest{Repository: repo, GlId: "key-1234", GlRepository: "some_repo"}
		response := doPush(t, stream, request, uploadPackData)

		expectedResponse := "0033\x01000eunpack ok\n001cok refs/heads/delete-me\n00000000"
		require.Equal(t, expectedResponse, string(response), "Expected response to be %q, got %q", expectedResponse, response)
		require.Equal(t, 3, refTransactionServer.called)
	})
}

func TestPostReceive_allRejected(t *testing.T) {
	t.Parallel()

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
	)
	addr := testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterSmartHTTPServiceServer(srv, NewServer(
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetTxManager(),
			deps.GetDiskCache(),
		))
		gitalypb.RegisterHookServiceServer(srv, hook.NewServer(deps.GetHookManager(), deps.GetGitCmdFactory(), deps.GetPackObjectsCache()))
	}, testserver.WithDisablePraefect(), testserver.WithHookManager(hookManager))

	ctx, err := txinfo.InjectTransaction(testhelper.Context(t), 1234, "primary", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	client := newMuxedSmartHTTPClient(t, ctx, addr, cfg.Auth.Token, func() backchannel.Server {
		srv := grpc.NewServer()
		gitalypb.RegisterRefTransactionServer(srv, refTransactionServer)
		return srv
	})

	stream, err := client.PostReceivePack(ctx)
	require.NoError(t, err)

	repo, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])

	request := &gitalypb.PostReceivePackRequest{Repository: repo, GlId: "key-1234", GlRepository: "some_repo"}
	doPush(t, stream, request, newTestPush(t, cfg, nil).body)

	require.Equal(t, 1, refTransactionServer.called)
}
