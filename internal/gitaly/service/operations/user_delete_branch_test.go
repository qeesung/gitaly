package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
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
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb/testproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUserDeleteBranch(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, client := setupOperationsService(t, ctx)

	type setupResponse struct {
		request          *gitalypb.UserDeleteBranchRequest
		expectedResponse *gitalypb.UserDeleteBranchResponse
		repoPath         string
		expectedErr      error
		expectedRefs     []string
	}

	testCases := []struct {
		desc  string
		setup func() setupResponse
	}{
		{
			desc: "simple successful deletion without ExpectedOldOID",
			setup: func() setupResponse {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				firstCommit := gittest.WriteCommit(t, cfg, repoPath)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"), gittest.WithParents(firstCommit))

				gittest.Exec(t, cfg, "-C", repoPath, "branch", "to-attempt-to-delete-soon-branch", "master")

				return setupResponse{
					request: &gitalypb.UserDeleteBranchRequest{
						User:       gittest.TestUser,
						Repository: repoProto,
						BranchName: []byte("to-attempt-to-delete-soon-branch"),
					},
					repoPath:         repoPath,
					expectedResponse: &gitalypb.UserDeleteBranchResponse{},
					expectedRefs:     []string{"master"},
				}
			},
		},
		{
			desc: "simple successful deletion with ExpectedOldOID",
			setup: func() setupResponse {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				firstCommit := gittest.WriteCommit(t, cfg, repoPath)
				headCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"), gittest.WithParents(firstCommit))

				gittest.Exec(t, cfg, "-C", repoPath, "branch", "to-attempt-to-delete-soon-branch", "master")

				return setupResponse{
					request: &gitalypb.UserDeleteBranchRequest{
						User:           gittest.TestUser,
						Repository:     repoProto,
						BranchName:     []byte("to-attempt-to-delete-soon-branch"),
						ExpectedOldOid: headCommit.String(),
					},
					repoPath:         repoPath,
					expectedResponse: &gitalypb.UserDeleteBranchResponse{},
					expectedRefs:     []string{"master"},
				}
			},
		},
		{
			desc: "partially prefixed successful deletion",
			setup: func() setupResponse {
				branchName := "heads/to-attempt-to-delete-soon-branch"

				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				firstCommit := gittest.WriteCommit(t, cfg, repoPath)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"), gittest.WithParents(firstCommit))

				gittest.Exec(t, cfg, "-C", repoPath, "branch", branchName, "master")

				return setupResponse{
					request: &gitalypb.UserDeleteBranchRequest{
						User:       gittest.TestUser,
						Repository: repoProto,
						BranchName: []byte(branchName),
					},
					repoPath:         repoPath,
					expectedResponse: &gitalypb.UserDeleteBranchResponse{},
					expectedRefs:     []string{"master"},
				}
			},
		},
		{
			desc: "branch with refs/heads/ prefix",
			setup: func() setupResponse {
				branchName := "refs/heads/branch"

				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				firstCommit := gittest.WriteCommit(t, cfg, repoPath)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"), gittest.WithParents(firstCommit))

				gittest.Exec(t, cfg, "-C", repoPath, "branch", branchName, "master")

				return setupResponse{
					request: &gitalypb.UserDeleteBranchRequest{
						User:       gittest.TestUser,
						Repository: repoProto,
						BranchName: []byte(branchName),
					},
					repoPath:         repoPath,
					expectedResponse: &gitalypb.UserDeleteBranchResponse{},
					expectedRefs:     []string{"master"},
				}
			},
		},
		{
			desc: "invalid ExpectedOldOID",
			setup: func() setupResponse {
				branchName := "random-branch"

				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				firstCommit := gittest.WriteCommit(t, cfg, repoPath)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"), gittest.WithParents(firstCommit))

				gittest.Exec(t, cfg, "-C", repoPath, "branch", branchName, "master")

				return setupResponse{
					request: &gitalypb.UserDeleteBranchRequest{
						User:           gittest.TestUser,
						Repository:     repoProto,
						BranchName:     []byte(branchName),
						ExpectedOldOid: "foobar",
					},
					repoPath: repoPath,
					expectedErr: testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument(fmt.Sprintf("invalid expected old object ID: invalid object ID: \"foobar\", expected length %v, got 6", gittest.DefaultObjectHash.EncodedLen())),
						"old_object_id", "foobar"),
					expectedRefs: []string{"master", branchName},
				}
			},
		},
		{
			desc: "valid ExpectedOldOID but not present in repo",
			setup: func() setupResponse {
				branchName := "random-branch"

				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				firstCommit := gittest.WriteCommit(t, cfg, repoPath)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"), gittest.WithParents(firstCommit))

				gittest.Exec(t, cfg, "-C", repoPath, "branch", branchName, "master")

				return setupResponse{
					request: &gitalypb.UserDeleteBranchRequest{
						User:           gittest.TestUser,
						Repository:     repoProto,
						BranchName:     []byte(branchName),
						ExpectedOldOid: gittest.DefaultObjectHash.ZeroOID.String(),
					},
					repoPath: repoPath,
					expectedErr: testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument("cannot resolve expected old object ID: reference not found"),
						"old_object_id", gittest.DefaultObjectHash.ZeroOID),
					expectedRefs: []string{"master", branchName},
				}
			},
		},
		{
			desc: "valid but incorrect ExpectedOldOID",
			setup: func() setupResponse {
				branchName := "random-branch"

				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				firstCommit := gittest.WriteCommit(t, cfg, repoPath)
				secondCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"), gittest.WithParents(firstCommit))

				gittest.Exec(t, cfg, "-C", repoPath, "branch", branchName, "master")

				return setupResponse{
					request: &gitalypb.UserDeleteBranchRequest{
						User:           gittest.TestUser,
						Repository:     repoProto,
						BranchName:     []byte(branchName),
						ExpectedOldOid: firstCommit.String(),
					},
					repoPath: repoPath,
					expectedErr: structerr.NewFailedPrecondition("reference update failed: reference update: reference does not point to expected object").
						WithDetail(&testproto.ErrorMetadata{
							Key:   []byte("actual_object_id"),
							Value: []byte(secondCommit),
						}).
						WithDetail(&testproto.ErrorMetadata{
							Key:   []byte("expected_object_id"),
							Value: []byte(firstCommit),
						}).
						WithDetail(&testproto.ErrorMetadata{
							Key:   []byte("reference"),
							Value: []byte("refs/heads/" + branchName),
						}).
						WithDetail(&gitalypb.UserDeleteBranchError{
							Error: &gitalypb.UserDeleteBranchError_ReferenceUpdate{
								ReferenceUpdate: &gitalypb.ReferenceUpdateError{
									ReferenceName: []byte("refs/heads/" + branchName),
									OldOid:        firstCommit.String(),
									NewOid:        gittest.DefaultObjectHash.ZeroOID.String(),
								},
							},
						}),
					expectedRefs: []string{"master", branchName},
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			data := tc.setup()

			response, err := client.UserDeleteBranch(ctx, data.request)
			testhelper.RequireGrpcError(t, data.expectedErr, err)
			testhelper.ProtoEqual(t, data.expectedResponse, response)

			refs := text.ChompBytes(gittest.Exec(t, cfg, "-C", data.repoPath, "for-each-ref", "--format=%(refname:short)", "--", "refs/heads/"))
			require.ElementsMatch(t, strings.Split(refs, "\n"), data.expectedRefs)
		})
	}
}

func TestUserDeleteBranch_allowed(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc             string
		allowed          func(context.Context, gitlab.AllowedParams) (bool, string, error)
		expectedErr      func(commitID git.ObjectID) error
		expectedResponse *gitalypb.UserDeleteBranchResponse
	}{
		{
			desc: "allowed",
			allowed: func(context.Context, gitlab.AllowedParams) (bool, string, error) {
				return true, "", nil
			},
			expectedResponse: &gitalypb.UserDeleteBranchResponse{},
		},
		{
			desc: "not allowed",
			allowed: func(context.Context, gitlab.AllowedParams) (bool, string, error) {
				return false, "something something", nil
			},
			expectedErr: func(commitID git.ObjectID) error {
				return structerr.NewPermissionDenied("deletion denied by access checks: running pre-receive hooks: GitLab: something something").WithDetail(
					&gitalypb.UserDeleteBranchError{
						Error: &gitalypb.UserDeleteBranchError_AccessCheck{
							AccessCheck: &gitalypb.AccessCheckError{
								Protocol:     "web",
								UserId:       "user-123",
								ErrorMessage: "something something",
								Changes: []byte(fmt.Sprintf(
									"%s %s refs/heads/branch\n", commitID, gittest.DefaultObjectHash.ZeroOID,
								)),
							},
						},
					},
				)
			},
		},
		{
			desc: "error",
			allowed: func(context.Context, gitlab.AllowedParams) (bool, string, error) {
				return false, "something something", errors.New("something else")
			},
			expectedErr: func(commitID git.ObjectID) error {
				return structerr.NewPermissionDenied("deletion denied by access checks: running pre-receive hooks: GitLab: something else").WithDetail(
					&gitalypb.UserDeleteBranchError{
						Error: &gitalypb.UserDeleteBranchError_AccessCheck{
							AccessCheck: &gitalypb.AccessCheckError{
								Protocol:     "web",
								UserId:       "user-123",
								ErrorMessage: "something else",
								Changes: []byte(fmt.Sprintf(
									"%s %s refs/heads/branch\n", commitID, gittest.DefaultObjectHash.ZeroOID,
								)),
							},
						},
					},
				)
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, cfg, client := setupOperationsService(t, ctx, testserver.WithGitLabClient(
				gitlab.NewMockClient(t, tc.allowed, gitlab.MockPreReceive, gitlab.MockPostReceive),
			))

			repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
			commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))

			var expectedErr error
			if tc.expectedErr != nil {
				expectedErr = tc.expectedErr(commitID)
			}

			response, err := client.UserDeleteBranch(ctx, &gitalypb.UserDeleteBranchRequest{
				Repository: repo,
				BranchName: []byte("branch"),
				User:       gittest.TestUser,
			})
			testhelper.RequireGrpcError(t, expectedErr, err)
			testhelper.ProtoEqual(t, tc.expectedResponse, response)
		})
	}
}

func TestUserDeleteBranch_concurrentUpdate(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("concurrent-update"))

	// Create a git-update-ref(1) process that's locking the "concurrent-update" branch. We do
	// not commit the update yet though to keep the reference locked to simulate concurrent
	// writes to the same reference.
	updater, err := updateref.New(ctx, localrepo.NewTestRepo(t, cfg, repo))
	require.NoError(t, err)
	defer testhelper.MustClose(t, updater)

	require.NoError(t, updater.Start())
	require.NoError(t, updater.Delete("refs/heads/concurrent-update"))
	require.NoError(t, updater.Prepare())

	response, err := client.UserDeleteBranch(ctx, &gitalypb.UserDeleteBranchRequest{
		Repository: repo,
		BranchName: []byte("concurrent-update"),
		User:       gittest.TestUser,
	})

	value := []byte("refs/heads/concurrent-update")
	// For reftable there is only table level locking and hence no
	// reference value is provided.
	if gittest.DefaultReferenceBackend == git.ReferenceBackendReftables {
		value = nil
	}

	testhelper.RequireGrpcError(t, structerr.NewFailedPrecondition("reference update failed: reference update: reference is already locked").
		WithDetail(&testproto.ErrorMetadata{
			Key:   []byte("reference"),
			Value: value,
		}).
		WithDetail(&gitalypb.UserDeleteBranchError{
			Error: &gitalypb.UserDeleteBranchError_ReferenceUpdate{
				ReferenceUpdate: &gitalypb.ReferenceUpdateError{
					OldOid:        commitID.String(),
					NewOid:        gittest.DefaultObjectHash.ZeroOID.String(),
					ReferenceName: []byte("refs/heads/concurrent-update"),
				},
			},
		}), err)
	require.Nil(t, response)
}

func TestUserDeleteBranch_hooks(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
	branchNameInput := "to-be-deleted-soon-branch"

	request := &gitalypb.UserDeleteBranchRequest{
		Repository: repo,
		BranchName: []byte(branchNameInput),
		User:       gittest.TestUser,
	}

	for _, hookName := range GitlabHooks {
		t.Run(hookName, func(t *testing.T) {
			gittest.Exec(t, cfg, "-C", repoPath, "branch", branchNameInput)

			hookOutputTempPath := gittest.WriteEnvToCustomHook(t, repoPath, hookName)

			_, err := client.UserDeleteBranch(ctx, request)
			require.NoError(t, err)

			output := testhelper.MustReadFile(t, hookOutputTempPath)
			require.Contains(t, string(output), "GL_USERNAME="+gittest.TestUser.GlUsername)
		})
	}
}

func TestUserDeleteBranch_transaction(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	parent := gittest.WriteCommit(t, cfg, repoPath)
	child := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(parent))

	// This creates a new branch "delete-me" which exists both in the packed-refs file and as a
	// loose reference. Git will create two reference transactions for this: one transaction to
	// delete the packed-refs reference, and one to delete the loose ref. But given that we want
	// to be independent of how well-packed refs are, we expect to get a single transactional
	// vote, only.
	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/delete-me", parent.String())
	gittest.Exec(t, cfg, "-C", repoPath, "pack-refs", "--all")
	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/delete-me", child.String())

	transactionServer := &testTransactionServer{}

	testserver.RunGitalyServer(t, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterOperationServiceServer(srv, NewServer(deps))
	})

	ctx, err := txinfo.InjectTransaction(ctx, 1, "node", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	client := newMuxedOperationClient(t, ctx, fmt.Sprintf("unix://"+cfg.InternalSocketPath()), cfg.Auth.Token,
		backchannel.NewClientHandshaker(
			testhelper.SharedLogger(t),
			func() backchannel.Server {
				srv := grpc.NewServer()
				gitalypb.RegisterRefTransactionServer(srv, transactionServer)
				return srv
			},
			backchannel.DefaultConfiguration(),
		),
	)

	_, err = client.UserDeleteBranch(ctx, &gitalypb.UserDeleteBranchRequest{
		Repository: repo,
		BranchName: []byte("delete-me"),
		User:       gittest.TestUser,
	})
	require.NoError(t, err)
	require.Equal(t, 5, transactionServer.called)
}

func TestUserDeleteBranch_invalidArgument(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	ctx, cfg, client := setupOperationsService(t, ctx)
	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	testCases := []struct {
		desc     string
		request  *gitalypb.UserDeleteBranchRequest
		response *gitalypb.UserDeleteBranchResponse
		err      error
	}{
		{
			desc: "empty user",
			request: &gitalypb.UserDeleteBranchRequest{
				Repository: repo,
				BranchName: []byte("does-matter-the-name-if-user-is-empty"),
			},
			response: nil,
			err:      status.Error(codes.InvalidArgument, "bad request: empty user"),
		},
		{
			desc: "empty branch name",
			request: &gitalypb.UserDeleteBranchRequest{
				Repository: repo,
				User:       gittest.TestUser,
			},
			response: nil,
			err:      status.Error(codes.InvalidArgument, "bad request: empty branch name"),
		},
		{
			desc: "non-existent branch name",
			request: &gitalypb.UserDeleteBranchRequest{
				Repository: repo,
				User:       gittest.TestUser,
				BranchName: []byte("i-do-not-exist"),
			},
			response: nil,
			err:      status.Errorf(codes.FailedPrecondition, "branch not found: %q", "i-do-not-exist"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			response, err := client.UserDeleteBranch(ctx, testCase.request)
			testhelper.RequireGrpcError(t, testCase.err, err)
			testhelper.ProtoEqual(t, testCase.response, response)
		})
	}
}

func TestUserDeleteBranch_hookFailure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	branchNameInput := "to-be-deleted-soon-branch"
	gittest.Exec(t, cfg, "-C", repoPath, "branch", branchNameInput)

	request := &gitalypb.UserDeleteBranchRequest{
		Repository: repo,
		BranchName: []byte(branchNameInput),
		User:       gittest.TestUser,
	}

	hookContent := []byte("#!/bin/sh\necho GL_ID=$GL_ID\nexit 1")

	for _, tc := range []struct {
		hookName string
		hookType gitalypb.CustomHookError_HookType
	}{
		{
			hookName: "pre-receive",
			hookType: gitalypb.CustomHookError_HOOK_TYPE_PRERECEIVE,
		},
		{
			hookName: "update",
			hookType: gitalypb.CustomHookError_HOOK_TYPE_UPDATE,
		},
	} {
		t.Run(tc.hookName, func(t *testing.T) {
			gittest.WriteCustomHook(t, repoPath, tc.hookName, hookContent)

			response, err := client.UserDeleteBranch(ctx, request)
			testhelper.RequireGrpcError(t, structerr.NewPermissionDenied("deletion denied by custom hooks: running %s hooks: %s\n", tc.hookName, "GL_ID=user-123").WithDetail(
				&gitalypb.UserDeleteBranchError{
					Error: &gitalypb.UserDeleteBranchError_CustomHook{
						CustomHook: &gitalypb.CustomHookError{
							HookType: tc.hookType,
							Stdout:   []byte("GL_ID=user-123\n"),
						},
					},
				},
			), err)

			require.Nil(t, response)

			branches := gittest.Exec(t, cfg, "-C", repoPath, "for-each-ref", "--", "refs/heads/"+branchNameInput)
			require.Contains(t, string(branches), branchNameInput, "branch name does not exist in branches list")
		})
	}
}

func TestBranchHookOutput(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	testCases := []struct {
		desc                string
		hookContent         string
		expectedErrorRegexp string
		expectedStderr      string
		expectedStdout      string
	}{
		{
			desc:                "empty stdout and empty stderr",
			hookContent:         "#!/bin/sh\nexit 1",
			expectedErrorRegexp: `executing custom hooks: error executing .+: exit status 1`,
		},
		{
			desc:                "empty stdout and some stderr",
			hookContent:         "#!/bin/sh\necho stderr >&2\nexit 1",
			expectedErrorRegexp: "stderr\n",
			expectedStderr:      "stderr\n",
		},
		{
			desc:                "some stdout and empty stderr",
			hookContent:         "#!/bin/sh\necho stdout\nexit 1",
			expectedErrorRegexp: "stdout\n",
			expectedStdout:      "stdout\n",
		},
		{
			desc:                "some stdout and some stderr",
			hookContent:         "#!/bin/sh\necho stdout\necho stderr >&2\nexit 1",
			expectedErrorRegexp: "stderr\n",
			expectedStderr:      "stderr\n",
			expectedStdout:      "stdout\n",
		},
		{
			desc:                "whitespace stdout and some stderr",
			hookContent:         "#!/bin/sh\necho '   '\necho stderr >&2\nexit 1",
			expectedErrorRegexp: "stderr\n",
			expectedStderr:      "stderr\n",
			expectedStdout:      "   \n",
		},
		{
			desc:                "some stdout and whitespace stderr",
			hookContent:         "#!/bin/sh\necho stdout\necho '   ' >&2\nexit 1",
			expectedErrorRegexp: "stdout\n",
			expectedStderr:      "   \n",
			expectedStdout:      "stdout\n",
		},
	}

	for _, hookTestCase := range []struct {
		hookName string
		hookType gitalypb.CustomHookError_HookType
	}{
		{
			hookName: "pre-receive",
			hookType: gitalypb.CustomHookError_HOOK_TYPE_PRERECEIVE,
		},
		{
			hookName: "update",
			hookType: gitalypb.CustomHookError_HOOK_TYPE_UPDATE,
		},
	} {
		for _, testCase := range testCases {
			t.Run(hookTestCase.hookName+"/"+testCase.desc, func(t *testing.T) {
				branchNameInput := "some-branch"
				createRequest := &gitalypb.UserCreateBranchRequest{
					Repository: repo,
					BranchName: []byte(branchNameInput),
					StartPoint: []byte(git.DefaultBranch),
					User:       gittest.TestUser,
				}
				deleteRequest := &gitalypb.UserDeleteBranchRequest{
					Repository: repo,
					BranchName: []byte(branchNameInput),
					User:       gittest.TestUser,
				}

				gittest.WriteCustomHook(t, repoPath, hookTestCase.hookName, []byte(testCase.hookContent))

				_, err := client.UserCreateBranch(ctx, createRequest)

				testhelper.RequireGrpcError(t, structerr.NewPermissionDenied("creation denied by custom hooks").WithDetail(
					&gitalypb.UserCreateBranchError{
						Error: &gitalypb.UserCreateBranchError_CustomHook{
							CustomHook: &gitalypb.CustomHookError{
								HookType: hookTestCase.hookType,
								Stdout:   []byte(testCase.expectedStdout),
								Stderr:   []byte(testCase.expectedStderr),
							},
						},
					},
				), err)

				gittest.Exec(t, cfg, "-C", repoPath, "branch", branchNameInput)
				defer gittest.Exec(t, cfg, "-C", repoPath, "branch", "-d", branchNameInput)

				deleteResponse, err := client.UserDeleteBranch(ctx, deleteRequest)

				actualStatus, ok := status.FromError(err)
				require.True(t, ok)

				statusWithoutMessage := actualStatus.Proto()
				statusWithoutMessage.Message = "OVERRIDDEN"

				// Assert the message separately as it references the hook path which may change and fail the equality check.
				require.Regexp(t, fmt.Sprintf(`^rpc error: code = PermissionDenied desc = deletion denied by custom hooks: running %s hooks: %s$`, hookTestCase.hookName, testCase.expectedErrorRegexp), err)
				testhelper.RequireGrpcError(t, structerr.NewPermissionDenied(statusWithoutMessage.Message).WithDetail(
					&gitalypb.UserDeleteBranchError{
						Error: &gitalypb.UserDeleteBranchError_CustomHook{
							CustomHook: &gitalypb.CustomHookError{
								HookType: hookTestCase.hookType,
								Stdout:   []byte(testCase.expectedStdout),
								Stderr:   []byte(testCase.expectedStderr),
							},
						},
					},
				), status.ErrorProto(statusWithoutMessage))
				require.Nil(t, deleteResponse)
			})
		}
	}
}
