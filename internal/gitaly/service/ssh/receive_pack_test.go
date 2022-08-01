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

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestReceivePack_validation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	repo, _ := gittest.CreateRepository(ctx, t, cfg)

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
			expectedErr: func() error {
				if testhelper.IsPraefectEnabled() {
					return helper.ErrInvalidArgumentf("repo scoped: invalid Repository")
				}

				return helper.ErrInvalidArgumentf("GetPath: relative path missing from storage_name:\"default\"")
			}(),
		},
		{
			desc: "missing repository",
			request: &gitalypb.SSHReceivePackRequest{
				Repository: nil,
				GlId:       "user-123",
			},
			expectedErr: func() error {
				if testhelper.IsPraefectEnabled() {
					return helper.ErrInvalidArgumentf("repo scoped: empty Repository")
				}

				return helper.ErrInvalidArgumentf("repository is empty")
			}(),
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
			expectedErr: helper.ErrInvalidArgumentf("empty GlId"),
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
			expectedErr: helper.ErrInvalidArgumentf("non-empty data in first request"),
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

	gitCmdFactory, hookOutputFile := gittest.CaptureHookEnv(t, cfg)
	testcfg.BuildGitalySSH(t, cfg)

	cfg.SocketPath = runSSHServer(t, cfg, testserver.WithGitCommandFactory(gitCmdFactory))

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	glRepository := "project-456"
	glProjectPath := "project/path"

	// We're explicitly injecting feature flags here because if we didn't, then Praefect would
	// do so for us and inject them all with their default value. As a result, we'd see
	// different flag values depending on whether this test runs with Gitaly or with Praefect
	// when deserializing the HooksPayload. By setting all flags to `true` explicitly, we both
	// verify that gitaly-ssh picks up feature flags correctly and fix the test to behave the
	// same with and without Praefect.
	for _, featureFlag := range featureflag.DefinedFlags() {
		ctx = featureflag.ContextWithFeatureFlag(ctx, featureFlag, true)
	}

	lHead, rHead, err := testCloneAndPush(ctx, t, cfg, cfg.SocketPath, repo, repoPath, pushParams{
		storageName:   cfg.Storages[0].Name,
		glID:          "123",
		glUsername:    "user",
		glRepository:  glRepository,
		glProjectPath: glProjectPath,
	})
	require.NoError(t, err)
	require.Equal(t, lHead, rHead, "local and remote head not equal. push failed")

	envData := testhelper.MustReadFile(t, hookOutputFile)
	payload, err := git.HooksPayloadFromEnv(strings.Split(string(envData), "\n"))
	require.NoError(t, err)

	// Compare the repository up front so that we can use require.Equal for
	// the remaining values.
	testhelper.ProtoEqual(t, &gitalypb.Repository{
		StorageName:   cfg.Storages[0].Name,
		RelativePath:  gittest.GetReplicaPath(ctx, t, cfg, repo),
		GlProjectPath: glProjectPath,
		GlRepository:  glRepository,
	}, payload.Repo)
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

	require.Equal(t, git.HooksPayload{
		RuntimeDir:          cfg.RuntimeDir,
		InternalSocket:      cfg.InternalSocketPath(),
		InternalSocketToken: cfg.Auth.Token,
		UserDetails: &git.UserDetails{
			UserID:   "123",
			Username: "user",
			Protocol: "ssh",
		},
		RequestedHooks: git.ReceivePackHooks,
	}, payload)
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
			expectedErr:       helper.ErrInternalf("cmd wait: exit status 128"),
			expectedErrorCode: 128,
			expectedStderr:    "fatal: protocol error: expected old/new/ref, got 'garbage'\n",
		},
		{
			desc: "command without flush",
			writeRequest: func(t *testing.T, stdin io.Writer) {
				gittest.WritePktlinef(t, stdin, "%[1]s %[1]s refs/heads/main", gittest.DefaultObjectHash.ZeroOID)
			},
			expectedErr:       helper.ErrCanceledf("user canceled the push"),
			expectedErrorCode: 128,
			expectedStderr:    "fatal: the remote end hung up unexpectedly\n",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, _ := gittest.CreateRepository(ctx, t, cfg)

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

	protocolDetectingFactory := gittest.NewProtocolDetectingCommandFactory(ctx, t, cfg)

	cfg.SocketPath = runSSHServer(t, cfg, testserver.WithGitCommandFactory(protocolDetectingFactory))

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	lHead, rHead, err := testCloneAndPush(ctx, t, cfg, cfg.SocketPath, repo, repoPath, pushParams{
		storageName:  testhelper.DefaultStorageName,
		glRepository: "project-123",
		glID:         "1",
		gitProtocol:  git.ProtocolV2,
	})
	require.NoError(t, err)
	require.Equal(t, lHead, rHead)

	envData := protocolDetectingFactory.ReadProtocol(t)
	require.Contains(t, envData, fmt.Sprintf("GIT_PROTOCOL=%s\n", git.ProtocolV2))
}

func TestReceivePack_failure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runSSHServer(t, cfg)

	testcfg.BuildGitalySSH(t, cfg)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	t.Run("clone with invalid storage name", func(t *testing.T) {
		_, _, err := testCloneAndPush(ctx, t, cfg, cfg.SocketPath, repo, repoPath, pushParams{
			storageName: "foobar",
			glID:        "1",
		})
		require.Error(t, err)

		if testhelper.IsPraefectEnabled() {
			require.Contains(t, err.Error(), helper.ErrInvalidArgumentf("repo scoped: invalid Repository").Error())
		} else {
			require.Contains(t, err.Error(), helper.ErrInvalidArgumentf("GetStorageByName: no such storage: \\\"foobar\\\"\\n").Error())
		}
	})

	t.Run("clone with invalid GlId", func(t *testing.T) {
		_, _, err := testCloneAndPush(ctx, t, cfg, cfg.SocketPath, repo, repoPath, pushParams{storageName: cfg.Storages[0].Name, glID: ""})
		require.Error(t, err)
		require.Contains(t, err.Error(), helper.ErrInvalidArgumentf("empty GlId").Error())
	})
}

func TestReceivePack_hookFailure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg, git.WithHooksPath(testhelper.TempDir(t)))

	testcfg.BuildGitalySSH(t, cfg)

	cfg.SocketPath = runSSHServer(t, cfg, testserver.WithGitCommandFactory(gitCmdFactory))

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	hookContent := []byte("#!/bin/sh\nexit 1")
	require.NoError(t, os.WriteFile(filepath.Join(gitCmdFactory.HooksPath(ctx), "pre-receive"), hookContent, 0o755))

	_, _, err := testCloneAndPush(ctx, t, cfg, cfg.SocketPath, repo, repoPath, pushParams{storageName: cfg.Storages[0].Name, glID: "1"})
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

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	cloneDetails, cleanup := setupSSHClone(t, cfg, repo, repoPath)
	defer cleanup()

	hookContent := []byte("#!/bin/sh\necho 'this is wrong' >&2;exit 1")
	gittest.WriteCustomHook(t, cloneDetails.RemoteRepoPath, "pre-receive", hookContent)

	cmd := sshPushCommand(ctx, t, cfg, cloneDetails, cfg.SocketPath,
		pushParams{
			storageName:  cfg.Storages[0].Name,
			glID:         "1",
			glRepository: repo.GlRepository,
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

	repoProto, _ := gittest.CreateRepository(ctx, t, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)
	txManager := transaction.NewManager(cfg, backchannel.NewRegistry())

	client := newSSHClient(t, cfg.SocketPath)

	stream, err := client.SSHReceivePack(ctx)
	require.NoError(t, err)

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
	require.NoError(t, pool.Link(ctx, repo))

	commitID := gittest.WriteCommit(t, cfg, pool.FullPath(), gittest.WithBranch(t.Name()))

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

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg)
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
			expectedVotes: 3,
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
			expectedVotes: 3,
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
			expectedVotes: 3,
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
			expectedVotes: 3,
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
			expectedVotes: 5,
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
			expectedVotes: 1,
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
			expectedVotes: 3,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			txManager.Reset()

			var request bytes.Buffer
			for i, command := range tc.commands {
				// Only the first pktline contains capabilities.
				if i == 0 {
					gittest.WritePktlineString(t, &request, fmt.Sprintf("%s %s %s\000 %s",
						command.oldOID, command.newOID, command.ref,
						"report-status side-band-64k agent=git/2.12.0"))
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

	protocolDetectingFactory := gittest.NewProtocolDetectingCommandFactory(ctx, t, cfg)
	cfg.SocketPath = runSSHServer(t, cfg, testserver.WithGitCommandFactory(protocolDetectingFactory))

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	tempGitlabShellDir := testhelper.TempDir(t)

	cfg.GitlabShell.Dir = tempGitlabShellDir

	cloneDetails, cleanup := setupSSHClone(t, cfg, repo, repoPath)
	defer cleanup()

	serverURL, cleanup := gitlab.NewTestServer(t, gitlab.TestServerOptions{
		User:                        "",
		Password:                    "",
		SecretToken:                 secretToken,
		GLID:                        glID,
		GLRepository:                glRepository,
		Changes:                     fmt.Sprintf("%s %s refs/heads/master\n", string(cloneDetails.OldHead), string(cloneDetails.NewHead)),
		PostReceiveCounterDecreased: true,
		Protocol:                    "ssh",
	})
	defer cleanup()

	gitlab.WriteShellSecretFile(t, tempGitlabShellDir, secretToken)

	cfg.Gitlab.URL = serverURL
	cfg.Gitlab.SecretFile = filepath.Join(tempGitlabShellDir, ".gitlab_shell_secret")

	gittest.WriteCheckNewObjectExistsHook(t, cloneDetails.RemoteRepoPath)

	lHead, rHead, err := sshPush(ctx, t, cfg, cloneDetails, cfg.SocketPath, pushParams{
		storageName:  cfg.Storages[0].Name,
		glID:         glID,
		glRepository: glRepository,
		gitProtocol:  git.ProtocolV2,
	})
	require.NoError(t, err)
	require.Equal(t, lHead, rHead, "local and remote head not equal. push failed")

	envData := protocolDetectingFactory.ReadProtocol(t)
	require.Contains(t, envData, fmt.Sprintf("GIT_PROTOCOL=%s\n", git.ProtocolV2))
}

// SSHCloneDetails encapsulates values relevant for a test clone
type SSHCloneDetails struct {
	LocalRepoPath, RemoteRepoPath, TempRepo string
	OldHead                                 []byte
	NewHead                                 []byte
}

// setupSSHClone sets up a test clone
func setupSSHClone(t *testing.T, cfg config.Cfg, remoteRepo *gitalypb.Repository, remoteRepoPath string) (SSHCloneDetails, func()) {
	_, localRepoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0])

	oldHead := text.ChompBytes(gittest.Exec(t, cfg, "-C", localRepoPath, "rev-parse", "HEAD"))
	newHead := gittest.WriteCommit(t, cfg, localRepoPath,
		gittest.WithMessage(fmt.Sprintf("Testing ReceivePack RPC around %d", time.Now().Unix())),
		gittest.WithTreeEntries(gittest.TreeEntry{
			Path:    "foo.txt",
			Mode:    "100644",
			Content: "foo bar",
		}),
		gittest.WithBranch("master"),
		gittest.WithParents(git.ObjectID(oldHead)),
	)

	return SSHCloneDetails{
			OldHead:        []byte(oldHead),
			NewHead:        []byte(newHead.String()),
			LocalRepoPath:  localRepoPath,
			RemoteRepoPath: remoteRepoPath,
			TempRepo:       remoteRepo.GetRelativePath(),
		}, func() {
			require.NoError(t, os.RemoveAll(remoteRepoPath))
			require.NoError(t, os.RemoveAll(localRepoPath))
		}
}

func sshPushCommand(ctx context.Context, t *testing.T, cfg config.Cfg, cloneDetails SSHCloneDetails, serverSocketPath string, params pushParams) *exec.Cmd {
	pbTempRepo := &gitalypb.Repository{
		StorageName:   params.storageName,
		RelativePath:  cloneDetails.TempRepo,
		GlProjectPath: params.glProjectPath,
		GlRepository:  params.glRepository,
	}
	payload, err := protojson.Marshal(&gitalypb.SSHReceivePackRequest{
		Repository:       pbTempRepo,
		GlRepository:     params.glRepository,
		GlId:             params.glID,
		GlUsername:       params.glUsername,
		GitConfigOptions: params.gitConfigOptions,
		GitProtocol:      params.gitProtocol,
	})
	require.NoError(t, err)

	var flagsWithValues []string
	for flag, value := range featureflag.FromContext(ctx) {
		flagsWithValues = append(flagsWithValues, flag.FormatWithValue(value))
	}

	cmd := gittest.NewCommand(t, cfg, "-C", cloneDetails.LocalRepoPath, "push", "-v", "git@localhost:test/test.git", "master")
	cmd.Env = []string{
		fmt.Sprintf("GITALY_PAYLOAD=%s", payload),
		fmt.Sprintf("GITALY_ADDRESS=%s", serverSocketPath),
		fmt.Sprintf("GITALY_FEATUREFLAGS=%s", strings.Join(flagsWithValues, ",")),
		fmt.Sprintf("GIT_SSH_COMMAND=%s receive-pack", cfg.BinaryPath("gitaly-ssh")),
	}

	return cmd
}

func sshPush(ctx context.Context, t *testing.T, cfg config.Cfg, cloneDetails SSHCloneDetails, serverSocketPath string, params pushParams) (string, string, error) {
	cmd := sshPushCommand(ctx, t, cfg, cloneDetails, serverSocketPath, params)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("error pushing: %v: %q", err, out)
	}

	if !cmd.ProcessState.Success() {
		return "", "", fmt.Errorf("failed to run `git push`: %q", out)
	}

	localHead := bytes.TrimSpace(gittest.Exec(t, cfg, "-C", cloneDetails.LocalRepoPath, "rev-parse", "master"))
	remoteHead := bytes.TrimSpace(gittest.Exec(t, cfg, "-C", cloneDetails.RemoteRepoPath, "rev-parse", "master"))

	return string(localHead), string(remoteHead), nil
}

func testCloneAndPush(ctx context.Context, t *testing.T, cfg config.Cfg, serverSocketPath string, remoteRepo *gitalypb.Repository, remoteRepoPath string, params pushParams) (string, string, error) {
	cloneDetails, cleanup := setupSSHClone(t, cfg, remoteRepo, remoteRepoPath)
	defer cleanup()

	return sshPush(ctx, t, cfg, cloneDetails, serverSocketPath, params)
}

func drainPostReceivePackResponse(stream gitalypb.SSHService_SSHReceivePackClient) error {
	var err error
	for err == nil {
		_, err = stream.Recv()
	}
	return err
}

type pushParams struct {
	storageName      string
	glID             string
	glUsername       string
	glRepository     string
	glProjectPath    string
	gitConfigOptions []string
	gitProtocol      string
}
