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

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestReceivePack_validation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	client := newSSHClient(t, cfg.SocketPath)

	for _, tc := range []struct {
		desc        string
		request     *gitalypb.SSHReceivePackRequest
		expectedErr error
	}{
		{
			desc: "empty relative path",
			request: &gitalypb.SSHReceivePackRequest{
				Repository: &gitalypb.Repository{
					StorageName:  cfg.Storages[0].Name,
					RelativePath: "",
				},
				GlId: "user-123",
			},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryPathNotSet),
		},
		{
			desc: "missing repository",
			request: &gitalypb.SSHReceivePackRequest{
				Repository: nil,
				GlId:       "user-123",
			},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "missing GlId",
			request: &gitalypb.SSHReceivePackRequest{
				Repository: &gitalypb.Repository{
					StorageName:  cfg.Storages[0].Name,
					RelativePath: repo.GetRelativePath(),
				},
				GlId: "",
			},
			expectedErr: structerr.NewInvalidArgument("empty GlId"),
		},
		{
			desc: "invalid storage name",
			request: &gitalypb.SSHReceivePackRequest{
				Repository: &gitalypb.Repository{
					StorageName:  "doesnotexist",
					RelativePath: repo.GetRelativePath(),
				},
				GlId: "user-123",
			},
			expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
				"%w", storage.NewStorageNotFoundError("doesnotexist"),
			)),
		},
		{
			desc: "stdin on first request",
			request: &gitalypb.SSHReceivePackRequest{
				Repository: &gitalypb.Repository{
					StorageName:  cfg.Storages[0].Name,
					RelativePath: repo.GetRelativePath(),
				},
				GlId:  "user-123",
				Stdin: []byte("Fail"),
			},
			expectedErr: structerr.NewInvalidArgument("non-empty data in first request"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			stream, err := client.SSHReceivePack(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(tc.request))
			require.NoError(t, stream.CloseSend())

			testhelper.RequireGrpcError(t, tc.expectedErr, drainPostReceivePackResponse(stream))
		})
	}
}

func TestReceivePack_success(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.GitlabShell.Dir = "/foo/bar/gitlab-shell"

	testcfg.BuildGitalyHooks(t, cfg)
	testcfg.BuildGitalySSH(t, cfg)

	// We need to intercept the update hook, but proxy the reference-transactions hook.
	logger := testhelper.SharedLogger(t)
	backchannelRegistry := backchannel.NewRegistry()
	transactionRegistry := storagemgr.NewTransactionRegistry()
	hookManager := hook.NewManager(
		cfg,
		config.NewLocator(cfg),
		logger,
		gittest.NewCommandFactory(t, cfg),
		transaction.NewManager(cfg, logger, backchannelRegistry),
		gitlab.NewMockClient(t, gitlab.MockAllowed, gitlab.MockPreReceive, gitlab.MockPostReceive),
		hook.NewTransactionRegistry(transactionRegistry),
		hook.NewProcReceiveRegistry(),
		nil,
	)

	// When the WAL is enabled, the proc-receive hook is executed which also manually invokes the
	// update hook. Here we use a mock hook manager to intercept the update hook and capture the
	// environment variables.
	var capturedHookEnv []string
	var preReceiveCount, postReceiveCount int
	mockHookManager := hook.NewMockManager(t,
		func(_ *testing.T, _ context.Context, _ *gitalypb.Repository, _, _ []string, _ io.Reader, _, _ io.Writer) error {
			preReceiveCount++
			return nil
		},
		func(_ *testing.T, _ context.Context, _ *gitalypb.Repository, _, _ []string, _ io.Reader, _, _ io.Writer) error {
			postReceiveCount++
			return nil
		},
		func(_ *testing.T, _ context.Context, _ *gitalypb.Repository, _, _, _ string, env []string, _, _ io.Writer) error {
			capturedHookEnv = env
			return nil
		},
		func(t *testing.T, ctx context.Context, state hook.ReferenceTransactionState, env []string, stdin io.Reader) error {
			return hookManager.ReferenceTransactionHook(ctx, state, env, stdin)
		},
		hook.NewProcReceiveRegistry(),
	)

	cfg.SocketPath = runSSHServer(t, cfg,
		testserver.WithHookManager(mockHookManager),
		testserver.WithTransactionRegistry(transactionRegistry),
		testserver.WithBackchannelRegistry(backchannelRegistry),
	)

	remoteRepo, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))
	remoteRepo.GlProjectPath = "project/path"
	remoteRepo.GlRepository = "project-456"

	// We're explicitly injecting feature flags here because if we didn't, then Praefect would
	// do so for us and inject them all with their default value. As a result, we'd see
	// different flag values depending on whether this test runs with Gitaly or with Praefect
	// when deserializing the HooksPayload. By setting all flags to `true` explicitly, we both
	// verify that gitaly-ssh picks up feature flags correctly and fix the test to behave the
	// same with and without Praefect.
	for _, featureFlag := range featureflag.DefinedFlags() {
		ctx = featureflag.ContextWithFeatureFlag(ctx, featureFlag, true)
	}

	lHead, rHead, err := setupRepoAndPush(t, ctx, cfg, &gitalypb.SSHReceivePackRequest{
		Repository:   remoteRepo,
		GlId:         "123",
		GlUsername:   "user",
		GlRepository: remoteRepo.GlRepository,
	})
	require.NoError(t, err)
	require.Equal(t, lHead, rHead, "local and remote head not equal. push failed")

	payload, err := git.HooksPayloadFromEnv(capturedHookEnv)
	require.NoError(t, err)

	expectedHooks := git.ReceivePackHooks

	// Compare the repository up front so that we can use require.Equal for
	// the remaining values.
	//
	expectedRepo := gittest.RewrittenRepository(t, ctx, cfg, remoteRepo)
	// The repository should have a relative path.
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

	// Compare the repository up front so that we can use require.Equal for
	// the remaining values.
	testhelper.ProtoEqual(t, expectedRepo, payload.Repo)
	payload.Repo = nil

	// If running tests with Praefect, then the transaction would be set, but we have no way of
	// figuring out their actual contents. So let's just remove it, too.
	payload.Transaction = nil

	var expectedFeatureFlags []git.FeatureFlagWithValue
	for _, feature := range featureflag.DefinedFlags() {
		expectedFeatureFlags = append(expectedFeatureFlags, git.FeatureFlagWithValue{
			Flag: feature, Enabled: true,
		})
	}

	// Compare here without paying attention to the order given that flags aren't sorted and
	// unset the struct member afterwards.
	require.ElementsMatch(t, expectedFeatureFlags, payload.FeatureFlagsWithValue)
	payload.FeatureFlagsWithValue = nil

	var transactionID storage.TransactionID
	if testhelper.IsWALEnabled() {
		transactionID = 4
	}

	require.Equal(t, git.HooksPayload{
		ObjectFormat:        gittest.DefaultObjectHash.Format,
		RuntimeDir:          cfg.RuntimeDir,
		InternalSocket:      cfg.InternalSocketPath(),
		InternalSocketToken: cfg.Auth.Token,
		UserDetails: &git.UserDetails{
			UserID:   "123",
			Username: "user",
			Protocol: "ssh",
		},
		RequestedHooks: expectedHooks,
		TransactionID:  transactionID,
	}, payload)

	require.Equal(t, 1, preReceiveCount)
	require.Equal(t, 1, postReceiveCount)
}

func TestReceivePack_invalidGitconfig(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	testcfg.BuildGitalySSH(t, cfg)
	testcfg.BuildGitalyHooks(t, cfg)

	remoteRepo, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))
	require.NoError(t, os.WriteFile(filepath.Join(remoteRepoPath, "config"), []byte("x x x foobar"), perm.SharedFile))
	remoteRepo.GlProjectPath = "something"

	lHead, rHead, err := setupRepoAndPush(t, ctx, cfg, &gitalypb.SSHReceivePackRequest{
		Repository:   remoteRepo,
		GlId:         "123",
		GlUsername:   "user",
		GlRepository: "something",
	})
	require.Error(t, err)
	require.Equal(t, lHead, rHead, "local and remote head not equal. push failed")
}

func TestReceivePack_client(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	for _, tc := range []struct {
		desc              string
		writeRequest      func(*testing.T, io.Writer)
		expectedErr       error
		expectedErrorCode int32
		expectedStderr    string
	}{
		{
			desc: "no commands",
			writeRequest: func(t *testing.T, stdin io.Writer) {
				gittest.WritePktlineFlush(t, stdin)
			},
		},
		{
			desc: "garbage",
			writeRequest: func(t *testing.T, stdin io.Writer) {
				gittest.WritePktlineString(t, stdin, "garbage")
			},
			expectedErr:       structerr.NewInternal("cmd wait: exit status 128"),
			expectedErrorCode: 128,
			expectedStderr:    "fatal: protocol error: expected old/new/ref, got 'garbage'\n",
		},
		{
			desc: "command without flush",
			writeRequest: func(t *testing.T, stdin io.Writer) {
				gittest.WritePktlinef(t, stdin, "%[1]s %[1]s refs/heads/main", gittest.DefaultObjectHash.ZeroOID)
			},
			expectedErr:       structerr.NewCanceled("user canceled the push"),
			expectedErrorCode: 128,
			expectedStderr:    "fatal: the remote end hung up unexpectedly\n",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

			stream, err := newSSHClient(t, cfg.SocketPath).SSHReceivePack(ctx)
			require.NoError(t, err)

			var observedErrorCode int32
			var stderr bytes.Buffer
			errCh := make(chan error, 1)
			go func() {
				stdout := streamio.NewReader(func() ([]byte, error) {
					msg, err := stream.Recv()
					if errorCode := msg.GetExitStatus().GetValue(); errorCode != 0 {
						require.Zero(t, observedErrorCode, "must not receive multiple messages with non-zero exit code")
						observedErrorCode = errorCode
					}

					// Write stderr so we can verify what git-receive-pack(1)
					// complains about.
					_, writeErr := stderr.Write(msg.GetStderr())
					require.NoError(t, writeErr)

					return msg.GetStdout(), err
				})

				_, err := io.Copy(io.Discard, stdout)
				errCh <- err
			}()

			require.NoError(t, stream.Send(&gitalypb.SSHReceivePackRequest{Repository: repoProto, GlId: "user-123"}))

			stdin := streamio.NewWriter(func(b []byte) error {
				return stream.Send(&gitalypb.SSHReceivePackRequest{Stdin: b})
			})
			tc.writeRequest(t, stdin)
			require.NoError(t, stream.CloseSend())

			testhelper.RequireGrpcError(t, <-errCh, tc.expectedErr)
			require.Equal(t, tc.expectedErrorCode, observedErrorCode)
			require.Equal(t, tc.expectedStderr, stderr.String())
		})
	}
}

func TestReceive_gitProtocol(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)

	testcfg.BuildGitalySSH(t, cfg)
	testcfg.BuildGitalyHooks(t, cfg)
	ctx := testhelper.Context(t)

	protocolDetectingFactory := gittest.NewProtocolDetectingCommandFactory(t, ctx, cfg)

	cfg.SocketPath = runSSHServer(t, cfg, testserver.WithGitCommandFactory(protocolDetectingFactory))

	remoteRepo, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))

	lHead, rHead, err := setupRepoAndPush(t, ctx, cfg, &gitalypb.SSHReceivePackRequest{
		Repository:   remoteRepo,
		GlRepository: "project-123",
		GlId:         "1",
		GitProtocol:  git.ProtocolV2,
	})
	require.NoError(t, err)
	require.Equal(t, lHead, rHead)

	envData := protocolDetectingFactory.ReadProtocol(t)
	require.Contains(t, envData, fmt.Sprintf("GIT_PROTOCOL=%s\n", git.ProtocolV2))
}

func TestReceivePack_hookFailure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg, git.WithHooksPath(testhelper.TempDir(t)))

	testcfg.BuildGitalySSH(t, cfg)

	cfg.SocketPath = runSSHServer(t, cfg, testserver.WithGitCommandFactory(gitCmdFactory))

	remoteRepo, _ := gittest.CreateRepository(t, ctx, cfg)

	hookContent := []byte("#!/bin/sh\nexit 1")
	require.NoError(t, os.WriteFile(filepath.Join(gitCmdFactory.HooksPath(ctx), "pre-receive"), hookContent, perm.SharedExecutable))

	_, _, err := setupRepoAndPush(t, ctx, cfg, &gitalypb.SSHReceivePackRequest{
		Repository: remoteRepo,
		GlId:       "1",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "(pre-receive hook declined)")
}

func TestReceivePack_customHookFailure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	testcfg.BuildGitalySSH(t, cfg)
	testcfg.BuildGitalyHooks(t, cfg)

	remoteRepo, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)

	localRepo := setupRepoWithChange(t, cfg)

	hookContent := []byte("#!/bin/sh\necho 'this is wrong' >&2;exit 1")
	gittest.WriteCustomHook(t, remoteRepoPath, "pre-receive", hookContent)

	cmd := sshPushCommand(t, ctx, cfg, localRepo, &gitalypb.SSHReceivePackRequest{
		Repository: remoteRepo,
		GlId:       "1",
	})

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stderr, err := cmd.StderrPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	c, err := io.Copy(io.Discard, stdout)
	require.NoError(t, err)
	require.Equal(t, c, int64(0))

	slurpErr, err := io.ReadAll(stderr)
	require.NoError(t, err)

	require.Error(t, cmd.Wait())

	require.Contains(t, string(slurpErr), "remote: this is wrong")
	require.Contains(t, string(slurpErr), "(pre-receive hook declined)")
	require.NotContains(t, string(slurpErr), "final transactional vote: transaction was stopped")
}

func TestReceivePack_hidesObjectPoolReferences(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	testcfg.BuildGitalyHooks(t, cfg)

	repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

	client := newSSHClient(t, cfg.SocketPath)

	stream, err := client.SSHReceivePack(ctx)
	require.NoError(t, err)

	_, poolPath := gittest.CreateObjectPool(t, ctx, cfg, repoProto, gittest.CreateObjectPoolConfig{
		LinkRepositoryToObjectPool: true,
	})
	commitID := gittest.WriteCommit(t, cfg, poolPath, gittest.WithBranch(t.Name()))

	// First request
	require.NoError(t, stream.Send(&gitalypb.SSHReceivePackRequest{Repository: repoProto, GlId: "user-123"}))
	require.NoError(t, stream.Send(&gitalypb.SSHReceivePackRequest{Stdin: []byte("0000")}))
	require.NoError(t, stream.CloseSend())

	r := streamio.NewReader(func() ([]byte, error) {
		msg, err := stream.Recv()
		return msg.GetStdout(), err
	})

	var b bytes.Buffer
	_, err = io.Copy(&b, r)
	require.NoError(t, err)
	require.NotContains(t, b.String(), commitID+" .have")
}

func TestReceivePack_transactional(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	txManager := transaction.NewTrackingManager()

	cfg.SocketPath = runSSHServer(t, cfg, testserver.WithTransactionManager(txManager))

	testcfg.BuildGitalyHooks(t, cfg)

	client := newSSHClient(t, cfg.SocketPath)

	ctx, err := txinfo.InjectTransaction(ctx, 1, "node", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)
	parentCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents(parentCommitID))

	type command struct {
		ref    string
		oldOID git.ObjectID
		newOID git.ObjectID
	}

	for _, tc := range []struct {
		desc          string
		writePackfile bool
		commands      []command
		expectedRefs  map[string]git.ObjectID
		expectedVotes int
	}{
		{
			desc:          "noop",
			writePackfile: true,
			commands: []command{
				{
					ref:    "refs/heads/main",
					oldOID: commitID,
					newOID: commitID,
				},
			},
			expectedRefs: map[string]git.ObjectID{
				"refs/heads/main": commitID,
			},
			expectedVotes: 6,
		},
		{
			desc:          "update",
			writePackfile: true,
			commands: []command{
				{
					ref:    "refs/heads/main",
					oldOID: commitID,
					newOID: parentCommitID,
				},
			},
			expectedRefs: map[string]git.ObjectID{
				"refs/heads/main": parentCommitID,
			},
			expectedVotes: 6,
		},
		{
			desc:          "creation",
			writePackfile: true,
			commands: []command{
				{
					ref:    "refs/heads/other",
					oldOID: gittest.DefaultObjectHash.ZeroOID,
					newOID: commitID,
				},
			},
			expectedRefs: map[string]git.ObjectID{
				"refs/heads/other": commitID,
			},
			expectedVotes: 6,
		},
		{
			desc: "deletion",
			commands: []command{
				{
					ref:    "refs/heads/other",
					oldOID: commitID,
					newOID: gittest.DefaultObjectHash.ZeroOID,
				},
			},
			expectedRefs: map[string]git.ObjectID{
				"refs/heads/other": gittest.DefaultObjectHash.ZeroOID,
			},
			expectedVotes: 6,
		},
		{
			desc:          "multiple commands",
			writePackfile: true,
			commands: []command{
				{
					ref:    "refs/heads/a",
					oldOID: gittest.DefaultObjectHash.ZeroOID,
					newOID: commitID,
				},
				{
					ref:    "refs/heads/b",
					oldOID: gittest.DefaultObjectHash.ZeroOID,
					newOID: commitID,
				},
			},
			expectedRefs: map[string]git.ObjectID{
				"refs/heads/a": commitID,
				"refs/heads/b": commitID,
			},
			expectedVotes: 9,
		},
		{
			desc:          "refused recreation of branch",
			writePackfile: true,
			commands: []command{
				{
					ref:    "refs/heads/a",
					oldOID: gittest.DefaultObjectHash.ZeroOID,
					newOID: parentCommitID,
				},
			},
			expectedRefs: map[string]git.ObjectID{
				"refs/heads/a": commitID,
			},
			expectedVotes: 3,
		},
		{
			desc:          "refused recreation and successful delete",
			writePackfile: true,
			commands: []command{
				{
					ref:    "refs/heads/a",
					oldOID: gittest.DefaultObjectHash.ZeroOID,
					newOID: parentCommitID,
				},
				{
					ref:    "refs/heads/b",
					oldOID: commitID,
					newOID: gittest.DefaultObjectHash.ZeroOID,
				},
			},
			expectedRefs: map[string]git.ObjectID{
				"refs/heads/a": commitID,
			},
			expectedVotes: 7,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			txManager.Reset()

			var request bytes.Buffer
			for i, command := range tc.commands {
				// Only the first pktline contains capabilities.
				if i == 0 {
					gittest.WritePktlinef(t, &request, "%s %s %s\000 report-status side-band-64k object-format=%s agent=git/2.12.0",
						command.oldOID, command.newOID, command.ref, gittest.DefaultObjectHash.Format,
					)
				} else {
					gittest.WritePktlineString(t, &request, fmt.Sprintf("%s %s %s",
						command.oldOID, command.newOID, command.ref))
				}
			}
			gittest.WritePktlineFlush(t, &request)

			if tc.writePackfile {
				// We're lazy and simply send over all objects to simplify test
				// setup.
				pack := gittest.Exec(t, cfg, "-C", repoPath, "pack-objects", "--stdout", "--revs", "--thin", "--delta-base-offset", "-q")
				request.Write(pack)
			}

			stream, err := client.SSHReceivePack(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(&gitalypb.SSHReceivePackRequest{
				Repository: repoProto, GlId: "user-123",
			}))
			require.NoError(t, stream.Send(&gitalypb.SSHReceivePackRequest{
				Stdin: request.Bytes(),
			}))
			require.NoError(t, stream.CloseSend())
			require.Equal(t, io.EOF, drainPostReceivePackResponse(stream))

			for expectedRef, expectedOID := range tc.expectedRefs {
				actualOID, err := repo.ResolveRevision(ctx, git.Revision(expectedRef))

				if expectedOID == gittest.DefaultObjectHash.ZeroOID {
					require.Equal(t, git.ErrReferenceNotFound, err)
				} else {
					require.NoError(t, err)
					require.Equal(t, expectedOID, actualOID)
				}
			}
			require.Equal(t, tc.expectedVotes, len(txManager.Votes()))
		})
	}
}

func TestReceivePack_objectExistsHook(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)

	testcfg.BuildGitalyHooks(t, cfg)
	testcfg.BuildGitalySSH(t, cfg)

	const (
		secretToken  = "secret token"
		glRepository = "some_repo"
		glID         = "key-123"
	)
	ctx := testhelper.Context(t)

	protocolDetectingFactory := gittest.NewProtocolDetectingCommandFactory(t, ctx, cfg)
	cfg.SocketPath = runSSHServer(t, cfg, testserver.WithGitCommandFactory(protocolDetectingFactory))

	remoteRepo, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)

	tempGitlabShellDir := testhelper.TempDir(t)

	cfg.GitlabShell.Dir = tempGitlabShellDir

	localRepo := setupRepoWithChange(t, cfg)

	serverURL, cleanup := gitlab.NewTestServer(t, gitlab.TestServerOptions{
		User:                        "",
		Password:                    "",
		SecretToken:                 secretToken,
		GLID:                        glID,
		GLRepository:                glRepository,
		Changes:                     fmt.Sprintf("%s %s refs/heads/master\n", localRepo.oldHead, localRepo.newHead),
		PostReceiveCounterDecreased: true,
		Protocol:                    "ssh",
	})
	defer cleanup()

	gitlab.WriteShellSecretFile(t, tempGitlabShellDir, secretToken)

	cfg.Gitlab.URL = serverURL
	cfg.Gitlab.SecretFile = filepath.Join(tempGitlabShellDir, ".gitlab_shell_secret")

	gittest.WriteCheckNewObjectExistsHook(t, remoteRepoPath)

	lHead, rHead, err := sshPush(t, ctx, cfg, localRepo, &gitalypb.SSHReceivePackRequest{
		Repository:   remoteRepo,
		GlId:         glID,
		GlRepository: glRepository,
		GitProtocol:  git.ProtocolV2,
	})
	require.NoError(t, err)
	require.Equal(t, lHead, rHead, "local and remote head not equal. push failed")

	envData := protocolDetectingFactory.ReadProtocol(t)
	require.Contains(t, envData, fmt.Sprintf("GIT_PROTOCOL=%s\n", git.ProtocolV2))
}

// repoWithChange represents a repository with two commits representing an "old" and "new" state.
type repoWithChange struct {
	path    string
	oldHead git.ObjectID
	newHead git.ObjectID
}

// setupRepoWithChange sets up a new Git repository with an "old" and a "new" commit that represent
// a single change.
func setupRepoWithChange(t *testing.T, cfg config.Cfg) repoWithChange {
	ctx := testhelper.Context(t)

	_, localRepoPath := gittest.CreateRepository(t, ctx, cfg)

	oldHead := gittest.WriteCommit(t, cfg, localRepoPath,
		gittest.WithMessage("old message"),
		gittest.WithTreeEntries(gittest.TreeEntry{
			Path: "foo.txt", Mode: "100644", Content: "old content",
		}),
	)
	newHead := gittest.WriteCommit(t, cfg, localRepoPath,
		gittest.WithMessage("new message"),
		gittest.WithTreeEntries(gittest.TreeEntry{
			Path: "foo.txt", Mode: "100644", Content: "new content",
		}),
		gittest.WithBranch("master"),
		gittest.WithParents(oldHead),
	)

	return repoWithChange{
		path:    localRepoPath,
		oldHead: oldHead,
		newHead: newHead,
	}
}

func sshPushCommand(t *testing.T, ctx context.Context, cfg config.Cfg, repo repoWithChange, request *gitalypb.SSHReceivePackRequest) *exec.Cmd {
	payload, err := protojson.Marshal(request)
	require.NoError(t, err)

	var flagsWithValues []string
	for flag, value := range featureflag.FromContext(ctx) {
		flagsWithValues = append(flagsWithValues, flag.FormatWithValue(value))
	}

	cmd := gittest.NewCommand(t, cfg, "-C", repo.path, "push", "-v", "git@localhost:test/test.git", "master")
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("GITALY_PAYLOAD=%s", payload),
		fmt.Sprintf("GITALY_ADDRESS=%s", cfg.SocketPath),
		fmt.Sprintf("GITALY_FEATUREFLAGS=%s", strings.Join(flagsWithValues, ",")),
		fmt.Sprintf("GIT_SSH_COMMAND=%s receive-pack", cfg.BinaryPath("gitaly-ssh")),
	)

	return cmd
}

func sshPush(t *testing.T, ctx context.Context, cfg config.Cfg, repo repoWithChange, request *gitalypb.SSHReceivePackRequest) (string, string, error) {
	cmd := sshPushCommand(t, ctx, cfg, repo, request)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("error pushing: %w: %q", err, out)
	}

	if !cmd.ProcessState.Success() {
		return "", "", fmt.Errorf("failed to run `git push`: %q", out)
	}

	remoteRepoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, request.Repository))

	localHead := bytes.TrimSpace(gittest.Exec(t, cfg, "-C", repo.path, "rev-parse", "master"))
	remoteHead := bytes.TrimSpace(gittest.Exec(t, cfg, "-C", remoteRepoPath, "rev-parse", "master"))

	return string(localHead), string(remoteHead), nil
}

func setupRepoAndPush(t *testing.T, ctx context.Context, cfg config.Cfg, request *gitalypb.SSHReceivePackRequest) (string, string, error) {
	return sshPush(t, ctx, cfg, setupRepoWithChange(t, cfg), request)
}

func drainPostReceivePackResponse(stream gitalypb.SSHService_SSHReceivePackClient) error {
	var err error
	for err == nil {
		_, err = stream.Recv()
	}
	return err
}
