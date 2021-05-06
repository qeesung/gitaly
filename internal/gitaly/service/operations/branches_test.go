package operations

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/internal/bootstrap/starter"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/metadata"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testTransactionServer struct {
	gitalypb.UnimplementedRefTransactionServer
	called int
}

func (s *testTransactionServer) VoteTransaction(ctx context.Context, in *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
	s.called++
	return &gitalypb.VoteTransactionResponse{
		State: gitalypb.VoteTransactionResponse_COMMIT,
	}, nil
}

func TestSuccessfulCreateBranchRequest(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	startPoint := "c7fbe50c7c7419d9701eebe64b1fdacc3df5b9dd"
	startPointCommit, err := repo.ReadCommit(ctx, git.Revision(startPoint))
	require.NoError(t, err)

	testCases := []struct {
		desc           string
		branchName     string
		startPoint     string
		expectedBranch *gitalypb.Branch
	}{
		{
			desc:       "valid branch",
			branchName: "new-branch",
			startPoint: startPoint,
			expectedBranch: &gitalypb.Branch{
				Name:         []byte("new-branch"),
				TargetCommit: startPointCommit,
			},
		},
		// On input like heads/foo and refs/heads/foo we don't
		// DWYM and map it to refs/heads/foo and
		// refs/heads/foo, respectively. Instead we always
		// prepend refs/heads/*, so you get
		// refs/heads/heads/foo and refs/heads/refs/heads/foo
		{
			desc:       "valid branch",
			branchName: "heads/new-branch",
			startPoint: startPoint,
			expectedBranch: &gitalypb.Branch{
				Name:         []byte("heads/new-branch"),
				TargetCommit: startPointCommit,
			},
		},
		{
			desc:       "valid branch",
			branchName: "refs/heads/new-branch",
			startPoint: startPoint,
			expectedBranch: &gitalypb.Branch{
				Name:         []byte("refs/heads/new-branch"),
				TargetCommit: startPointCommit,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			branchName := testCase.branchName
			request := &gitalypb.UserCreateBranchRequest{
				Repository: repoProto,
				BranchName: []byte(branchName),
				StartPoint: []byte(testCase.startPoint),
				User:       testhelper.TestUser,
			}

			response, err := client.UserCreateBranch(ctx, request)
			if testCase.expectedBranch != nil {
				defer testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "branch", "-D", branchName)
			}

			require.NoError(t, err)
			require.Equal(t, testCase.expectedBranch, response.Branch)
			require.Empty(t, response.PreReceiveError)

			branches := testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "for-each-ref", "--", "refs/heads/"+branchName)
			require.Contains(t, string(branches), "refs/heads/"+branchName)
		})
	}
}

func TestUserCreateBranchWithTransaction(t *testing.T) {
	cfg, repo, repoPath := testcfg.BuildWithRepo(t)

	transactionServer := &testTransactionServer{}

	cfg.ListenAddr = "127.0.0.1:0" // runs gitaly on the TCP address
	addr := testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterOperationServiceServer(srv, NewServer(deps.GetCfg(), nil, deps.GetHookManager(), deps.GetLocator(), deps.GetConnsPool(), deps.GetGitCmdFactory()))
		gitalypb.RegisterHookServiceServer(srv, hook.NewServer(deps.GetCfg(), deps.GetHookManager(), deps.GetGitCmdFactory()))
		// Praefect proxy execution disabled as praefect runs only on the UNIX socket, but
		// the test requires a TCP listening address.
	}, testserver.WithDisablePraefect())

	addrConfig, err := starter.ParseEndpoint(addr)
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(addrConfig.Addr)
	require.NoError(t, err)

	testcases := []struct {
		desc    string
		address string
		server  metadata.PraefectServer
	}{
		{
			desc:    "explicit TCP address",
			address: addr,
			server: metadata.PraefectServer{
				ListenAddr: addr,
				Token:      cfg.Auth.Token,
			},
		},
		{
			desc:    "catch-all TCP address",
			address: addr,
			server: metadata.PraefectServer{
				ListenAddr: "tcp://0.0.0.0:" + port,
				Token:      cfg.Auth.Token,
			},
		},
		{
			desc:    "Unix socket",
			address: "unix://" + cfg.GitalyInternalSocketPath(),
			server: metadata.PraefectServer{
				SocketPath: "unix://" + cfg.GitalyInternalSocketPath(),
				Token:      cfg.Auth.Token,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			defer testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "branch", "-D", "new-branch")

			ctx, cancel := testhelper.Context()
			defer cancel()

			client := newMuxedOperationClient(t, ctx, tc.address, cfg.Auth.Token,
				backchannel.NewClientHandshaker(
					testhelper.DiscardTestEntry(t),
					func() backchannel.Server {
						srv := grpc.NewServer()
						gitalypb.RegisterRefTransactionServer(srv, transactionServer)
						return srv
					},
				),
			)

			ctx, err := tc.server.Inject(ctx)
			require.NoError(t, err)
			ctx, err = metadata.InjectTransaction(ctx, 1, "node", true)
			require.NoError(t, err)
			ctx = helper.IncomingToOutgoing(ctx)

			request := &gitalypb.UserCreateBranchRequest{
				Repository: repo,
				BranchName: []byte("new-branch"),
				StartPoint: []byte("c7fbe50c7c7419d9701eebe64b1fdacc3df5b9dd"),
				User:       testhelper.TestUser,
			}

			transactionServer.called = 0
			response, err := client.UserCreateBranch(ctx, request)
			require.NoError(t, err)
			require.Empty(t, response.PreReceiveError)
			require.Equal(t, 1, transactionServer.called)
		})
	}
}

func TestSuccessfulGitHooksForUserCreateBranchRequest(t *testing.T) {
	testhelper.NewFeatureSets([]featureflag.FeatureFlag{
		featureflag.ReferenceTransactions,
	}).Run(t, testSuccessfulGitHooksForUserCreateBranchRequest)
}

func testSuccessfulGitHooksForUserCreateBranchRequest(t *testing.T, ctx context.Context) {
	ctx, _, repo, repoPath, client := setupOperationsService(t, ctx)

	branchName := "new-branch"
	request := &gitalypb.UserCreateBranchRequest{
		Repository: repo,
		BranchName: []byte(branchName),
		StartPoint: []byte("c7fbe50c7c7419d9701eebe64b1fdacc3df5b9dd"),
		User:       testhelper.TestUser,
	}

	for _, hookName := range GitlabHooks {
		t.Run(hookName, func(t *testing.T) {
			defer testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "branch", "-D", branchName)

			hookOutputTempPath := gittest.WriteEnvToCustomHook(t, repoPath, hookName)

			response, err := client.UserCreateBranch(ctx, request)
			require.NoError(t, err)
			require.Empty(t, response.PreReceiveError)

			output := string(testhelper.MustReadFile(t, hookOutputTempPath))
			require.Contains(t, output, "GL_USERNAME="+testhelper.TestUser.GlUsername)
		})
	}
}

func TestSuccessfulCreateBranchRequestWithStartPointRefPrefix(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	testCases := []struct {
		desc             string
		branchName       string
		startPoint       string
		startPointCommit string
		user             *gitalypb.User
	}{
		// Similar to prefixing branchName in
		// TestSuccessfulCreateBranchRequest() above:
		// Unfortunately (and inconsistently), the StartPoint
		// reference does have DWYM semantics. See
		// https://gitlab.com/gitlab-org/gitaly/-/issues/3331
		{
			desc:             "the StartPoint parameter does DWYM references (boo!)",
			branchName:       "topic",
			startPoint:       "heads/master",
			startPointCommit: "9a944d90955aaf45f6d0c88f30e27f8d2c41cec0", // TODO: see below
			user:             testhelper.TestUser,
		},
		{
			desc:             "the StartPoint parameter does DWYM references (boo!) 2",
			branchName:       "topic2",
			startPoint:       "refs/heads/master",
			startPointCommit: "c642fe9b8b9f28f9225d7ea953fe14e74748d53b", // TODO: see below
			user:             testhelper.TestUser,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "update-ref", "refs/heads/"+testCase.startPoint,
				testCase.startPointCommit,
				git.ZeroOID.String(),
			)
			request := &gitalypb.UserCreateBranchRequest{
				Repository: repoProto,
				BranchName: []byte(testCase.branchName),
				StartPoint: []byte(testCase.startPoint),
				User:       testCase.user,
			}

			// BEGIN TODO: Uncomment if StartPoint started behaving sensibly
			// like BranchName. See
			// https://gitlab.com/gitlab-org/gitaly/-/issues/3331
			//
			//targetCommitOK, err := repo.ReadCommit(ctx, testCase.startPointCommit)
			// END TODO
			targetCommitOK, err := repo.ReadCommit(ctx, "1e292f8fedd741b75372e19097c76d327140c312")
			require.NoError(t, err)

			response, err := client.UserCreateBranch(ctx, request)
			require.NoError(t, err)
			responseOk := &gitalypb.UserCreateBranchResponse{
				Branch: &gitalypb.Branch{
					Name:         []byte(testCase.branchName),
					TargetCommit: targetCommitOK,
				},
			}
			require.Equal(t, responseOk, response)
			branches := testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "for-each-ref", "--", "refs/heads/"+testCase.branchName)
			require.Contains(t, string(branches), "refs/heads/"+testCase.branchName)
		})
	}
}

func TestFailedUserCreateBranchDueToHooks(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	ctx, _, repo, repoPath, client := setupOperationsService(t, ctx)

	request := &gitalypb.UserCreateBranchRequest{
		Repository: repo,
		BranchName: []byte("new-branch"),
		StartPoint: []byte("c7fbe50c7c7419d9701eebe64b1fdacc3df5b9dd"),
		User:       testhelper.TestUser,
	}
	// Write a hook that will fail with the environment as the error message
	// so we can check that string for our env variables.
	hookContent := []byte("#!/bin/sh\nprintenv | paste -sd ' ' -\nexit 1")

	for _, hookName := range gitlabPreHooks {
		gittest.WriteCustomHook(t, repoPath, hookName, hookContent)

		response, err := client.UserCreateBranch(ctx, request)
		require.Nil(t, err)
		require.Contains(t, response.PreReceiveError, "GL_USERNAME="+testhelper.TestUser.GlUsername)
	}
}

func TestFailedUserCreateBranchRequest(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	ctx, _, repo, _, client := setupOperationsService(t, ctx)

	testCases := []struct {
		desc       string
		branchName string
		startPoint string
		user       *gitalypb.User
		err        error
	}{
		{
			desc:       "empty start_point",
			branchName: "shiny-new-branch",
			startPoint: "",
			user:       testhelper.TestUser,
			err:        status.Error(codes.InvalidArgument, "empty start point"),
		},
		{
			desc:       "empty user",
			branchName: "shiny-new-branch",
			startPoint: "master",
			user:       nil,
			err:        status.Error(codes.InvalidArgument, "empty user"),
		},
		{
			desc:       "non-existing starting point",
			branchName: "new-branch",
			startPoint: "i-dont-exist",
			user:       testhelper.TestUser,
			err:        status.Errorf(codes.FailedPrecondition, "revspec '%s' not found", "i-dont-exist"),
		},

		{
			desc:       "branch exists",
			branchName: "master",
			startPoint: "master",
			user:       testhelper.TestUser,
			err:        status.Errorf(codes.FailedPrecondition, "Could not update %s. Please refresh and try again.", "master"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			request := &gitalypb.UserCreateBranchRequest{
				Repository: repo,
				BranchName: []byte(testCase.branchName),
				StartPoint: []byte(testCase.startPoint),
				User:       testCase.user,
			}

			response, err := client.UserCreateBranch(ctx, request)
			require.Equal(t, testCase.err, err)
			require.Empty(t, response)
		})
	}
}

func TestSuccessfulUserDeleteBranchRequest(t *testing.T) {
	testhelper.NewFeatureSets([]featureflag.FeatureFlag{
		featureflag.ReferenceTransactions,
	}).Run(t, testSuccessfulUserDeleteBranchRequest)
}

func testSuccessfulUserDeleteBranchRequest(t *testing.T, ctx context.Context) {
	ctx, _, repo, repoPath, client := setupOperationsService(t, ctx)

	testCases := []struct {
		desc            string
		branchNameInput string
		branchCommit    string
		user            *gitalypb.User
		response        *gitalypb.UserDeleteBranchResponse
		err             error
	}{
		{
			desc:            "simple successful deletion",
			branchNameInput: "to-attempt-to-delete-soon-branch",
			branchCommit:    "c7fbe50c7c7419d9701eebe64b1fdacc3df5b9dd",
			user:            testhelper.TestUser,
			response:        &gitalypb.UserDeleteBranchResponse{},
		},
		{
			desc:            "partially prefixed successful deletion",
			branchNameInput: "heads/to-attempt-to-delete-soon-branch",
			branchCommit:    "9a944d90955aaf45f6d0c88f30e27f8d2c41cec0",
			user:            testhelper.TestUser,
			response:        &gitalypb.UserDeleteBranchResponse{},
		},
		{
			desc:            "branch with refs/heads/ prefix",
			branchNameInput: "refs/heads/branch",
			branchCommit:    "9a944d90955aaf45f6d0c88f30e27f8d2c41cec0",
			user:            testhelper.TestUser,
			response:        &gitalypb.UserDeleteBranchResponse{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "branch", testCase.branchNameInput, testCase.branchCommit)

			response, err := client.UserDeleteBranch(ctx, &gitalypb.UserDeleteBranchRequest{
				Repository: repo,
				BranchName: []byte(testCase.branchNameInput),
				User:       testCase.user,
			})
			require.NoError(t, err)
			testhelper.ProtoEqual(t, testCase.response, response)

			refs := testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "for-each-ref", "--", "refs/heads/"+testCase.branchNameInput)
			require.NotContains(t, string(refs), testCase.branchCommit, "branch deleted from refs")
		})
	}
}

func TestSuccessfulGitHooksForUserDeleteBranchRequest(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	ctx, _, repo, repoPath, client := setupOperationsService(t, ctx)

	branchNameInput := "to-be-deleted-soon-branch"

	request := &gitalypb.UserDeleteBranchRequest{
		Repository: repo,
		BranchName: []byte(branchNameInput),
		User:       testhelper.TestUser,
	}

	for _, hookName := range GitlabHooks {
		t.Run(hookName, func(t *testing.T) {
			testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "branch", branchNameInput)

			hookOutputTempPath := gittest.WriteEnvToCustomHook(t, repoPath, hookName)

			_, err := client.UserDeleteBranch(ctx, request)
			require.NoError(t, err)

			output := testhelper.MustReadFile(t, hookOutputTempPath)
			require.Contains(t, string(output), "GL_USERNAME="+testhelper.TestUser.GlUsername)
		})
	}
}

func TestUserDeleteBranch_transaction(t *testing.T) {
	cfg, repo, repoPath := testcfg.BuildWithRepo(t)

	// This creates a new branch "delete-me" which exists both in the packed-refs file and as a
	// loose reference. Git will create two reference transactions for this: one transaction to
	// delete the packed-refs reference, and one to delete the loose ref. But given that we want
	// to be independent of how well-packed refs are, we expect to get a single transactional
	// vote, only.
	testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "update-ref", "refs/heads/delete-me", "master~")
	testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "pack-refs", "--all")
	testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "update-ref", "refs/heads/delete-me", "master")

	transactionServer := &testTransactionServer{}

	testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterOperationServiceServer(srv, NewServer(deps.GetCfg(), nil, deps.GetHookManager(), deps.GetLocator(), deps.GetConnsPool(), deps.GetGitCmdFactory()))
	})

	praefect := metadata.PraefectServer{
		SocketPath: fmt.Sprintf("unix://" + cfg.GitalyInternalSocketPath()),
		Token:      cfg.Auth.Token,
	}

	ctx, cancel := testhelper.Context()
	defer cancel()

	ctx, err := praefect.Inject(ctx)
	require.NoError(t, err)
	ctx, err = metadata.InjectTransaction(ctx, 1, "node", true)
	require.NoError(t, err)
	ctx = helper.IncomingToOutgoing(ctx)

	client := newMuxedOperationClient(t, ctx, fmt.Sprintf("unix://"+cfg.GitalyInternalSocketPath()), cfg.Auth.Token,
		backchannel.NewClientHandshaker(
			testhelper.DiscardTestEntry(t),
			func() backchannel.Server {
				srv := grpc.NewServer()
				gitalypb.RegisterRefTransactionServer(srv, transactionServer)
				return srv
			},
		),
	)

	_, err = client.UserDeleteBranch(ctx, &gitalypb.UserDeleteBranchRequest{
		Repository: repo,
		BranchName: []byte("delete-me"),
		User:       testhelper.TestUser,
	})
	require.NoError(t, err)
	require.Equal(t, 1, transactionServer.called)
}

func TestFailedUserDeleteBranchDueToValidation(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	ctx, _, repo, _, client := setupOperationsService(t, ctx)

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
			err:      status.Error(codes.InvalidArgument, "Bad Request (empty user)"),
		},
		{
			desc: "empty branch name",
			request: &gitalypb.UserDeleteBranchRequest{
				Repository: repo,
				User:       testhelper.TestUser,
			},
			response: nil,
			err:      status.Error(codes.InvalidArgument, "Bad Request (empty branch name)"),
		},
		{
			desc: "non-existent branch name",
			request: &gitalypb.UserDeleteBranchRequest{
				Repository: repo,
				User:       testhelper.TestUser,
				BranchName: []byte("i-do-not-exist"),
			},
			response: nil,
			err:      status.Errorf(codes.FailedPrecondition, "branch not found: %s", "i-do-not-exist"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			response, err := client.UserDeleteBranch(ctx, testCase.request)
			require.Equal(t, testCase.err, err)
			testhelper.ProtoEqual(t, testCase.response, response)
		})
	}
}

func TestFailedUserDeleteBranchDueToHooks(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	ctx, _, repo, repoPath, client := setupOperationsService(t, ctx)

	branchNameInput := "to-be-deleted-soon-branch"
	testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "branch", branchNameInput)

	request := &gitalypb.UserDeleteBranchRequest{
		Repository: repo,
		BranchName: []byte(branchNameInput),
		User:       testhelper.TestUser,
	}

	hookContent := []byte("#!/bin/sh\necho GL_ID=$GL_ID\nexit 1")

	for _, hookName := range gitlabPreHooks {
		t.Run(hookName, func(t *testing.T) {
			gittest.WriteCustomHook(t, repoPath, hookName, hookContent)

			response, err := client.UserDeleteBranch(ctx, request)
			require.NoError(t, err)
			require.Contains(t, response.PreReceiveError, "GL_ID="+testhelper.TestUser.GlId)

			branches := testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "for-each-ref", "--", "refs/heads/"+branchNameInput)
			require.Contains(t, string(branches), branchNameInput, "branch name does not exist in branches list")
		})
	}
}

func TestBranchHookOutput(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	ctx, _, repo, repoPath, client := setupOperationsService(t, ctx)

	testCases := []struct {
		desc        string
		hookContent string
		output      string
	}{
		{
			desc:        "empty stdout and empty stderr",
			hookContent: "#!/bin/sh\nexit 1",
			output:      "",
		},
		{
			desc:        "empty stdout and some stderr",
			hookContent: "#!/bin/sh\necho stderr >&2\nexit 1",
			output:      "stderr\n",
		},
		{
			desc:        "some stdout and empty stderr",
			hookContent: "#!/bin/sh\necho stdout\nexit 1",
			output:      "stdout\n",
		},
		{
			desc:        "some stdout and some stderr",
			hookContent: "#!/bin/sh\necho stdout\necho stderr >&2\nexit 1",
			output:      "stderr\n",
		},
		{
			desc:        "whitespace stdout and some stderr",
			hookContent: "#!/bin/sh\necho '   '\necho stderr >&2\nexit 1",
			output:      "stderr\n",
		},
		{
			desc:        "some stdout and whitespace stderr",
			hookContent: "#!/bin/sh\necho stdout\necho '   ' >&2\nexit 1",
			output:      "stdout\n",
		},
	}

	for _, hookName := range gitlabPreHooks {
		for _, testCase := range testCases {
			t.Run(hookName+"/"+testCase.desc, func(t *testing.T) {
				branchNameInput := "some-branch"
				createRequest := &gitalypb.UserCreateBranchRequest{
					Repository: repo,
					BranchName: []byte(branchNameInput),
					StartPoint: []byte("master"),
					User:       testhelper.TestUser,
				}
				deleteRequest := &gitalypb.UserDeleteBranchRequest{
					Repository: repo,
					BranchName: []byte(branchNameInput),
					User:       testhelper.TestUser,
				}

				gittest.WriteCustomHook(t, repoPath, hookName, []byte(testCase.hookContent))

				createResponse, err := client.UserCreateBranch(ctx, createRequest)
				require.NoError(t, err)
				require.Equal(t, testCase.output, createResponse.PreReceiveError)

				testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "branch", branchNameInput)
				defer testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "branch", "-d", branchNameInput)

				deleteResponse, err := client.UserDeleteBranch(ctx, deleteRequest)
				require.NoError(t, err)
				require.Equal(t, testCase.output, deleteResponse.PreReceiveError)
			})
		}
	}
}
