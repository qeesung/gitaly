package operations

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUserCreateBranch_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, client := setupOperationsService(t, ctx)

	type setupData struct {
		branchName     string
		startPoint     string
		expectedBranch *gitalypb.Branch
	}

	testCases := []struct {
		desc  string
		setup func(git.ObjectID, *gitalypb.GitCommit) setupData
	}{
		{
			desc: "valid branch",
			setup: func(startPoint git.ObjectID, startPointCommit *gitalypb.GitCommit) setupData {
				return setupData{
					branchName: "new-branch",
					startPoint: startPoint.String(),
					expectedBranch: &gitalypb.Branch{
						Name:         []byte("new-branch"),
						TargetCommit: startPointCommit,
					},
				}
			},
		},
		// On input like heads/foo and refs/heads/foo we don't
		// DWYM and map it to refs/heads/foo and
		// refs/heads/foo, respectively. Instead we always
		// prepend refs/heads/*, so you get
		// refs/heads/heads/foo and refs/heads/refs/heads/foo
		{
			desc: "valid branch",
			setup: func(startPoint git.ObjectID, startPointCommit *gitalypb.GitCommit) setupData {
				return setupData{
					branchName: "heads/new-branch",
					startPoint: startPoint.String(),
					expectedBranch: &gitalypb.Branch{
						Name:         []byte("heads/new-branch"),
						TargetCommit: startPointCommit,
					},
				}
			},
		},
		{
			desc: "valid branch",
			setup: func(startPoint git.ObjectID, startPointCommit *gitalypb.GitCommit) setupData {
				return setupData{
					branchName: "refs/heads/new-branch",
					startPoint: startPoint.String(),
					expectedBranch: &gitalypb.Branch{
						Name:         []byte("refs/heads/new-branch"),
						TargetCommit: startPointCommit,
					},
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

			startPoint := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
				gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
			))

			localRepo := localrepo.NewTestRepo(t, cfg, repo)
			startPointCommit, err := localRepo.ReadCommit(ctx, startPoint.Revision())
			require.NoError(t, err)

			setup := tc.setup(startPoint, startPointCommit)

			branchName := setup.branchName
			request := &gitalypb.UserCreateBranchRequest{
				Repository: repo,
				BranchName: []byte(branchName),
				StartPoint: []byte(setup.startPoint),
				User:       gittest.TestUser,
			}

			response, err := client.UserCreateBranch(ctx, request)
			require.NoError(t, err)
			require.Equal(t, setup.expectedBranch, response.Branch)

			branches := gittest.Exec(t, cfg, "-C", repoPath, "for-each-ref", "--", "refs/heads/"+branchName)
			require.Contains(t, string(branches), "refs/heads/"+branchName)
		})
	}
}

func TestUserCreateBranch_transactions(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	startPoint := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
	))

	transactionServer := &testTransactionServer{}

	cfg.ListenAddr = "127.0.0.1:0" // runs gitaly on the TCP address
	addr := testserver.RunGitalyServer(t, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterOperationServiceServer(srv, NewServer(deps))
		gitalypb.RegisterHookServiceServer(srv, hook.NewServer(deps))
		// Praefect proxy execution disabled as praefect runs only on the UNIX socket, but
		// the test requires a TCP listening address.
	}, testserver.WithDisablePraefect())

	testcases := []struct {
		desc    string
		address string
	}{
		{
			desc:    "TCP address",
			address: addr,
		},
		{
			desc:    "Unix socket",
			address: "unix://" + cfg.InternalSocketPath(),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			defer gittest.Exec(t, cfg, "-C", repoPath, "branch", "-D", "new-branch")

			ctx, err := txinfo.InjectTransaction(ctx, 1, "node", true)
			require.NoError(t, err)
			ctx = metadata.IncomingToOutgoing(ctx)

			client := newMuxedOperationClient(t, ctx, tc.address, cfg.Auth.Token,
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

			request := &gitalypb.UserCreateBranchRequest{
				Repository: repo,
				BranchName: []byte("new-branch"),
				StartPoint: []byte(startPoint),
				User:       gittest.TestUser,
			}

			transactionServer.called = 0
			_, err = client.UserCreateBranch(ctx, request)
			require.NoError(t, err)
			require.Equal(t, 5, transactionServer.called)
		})
	}
}

func TestUserCreateBranch_hook(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, client := setupOperationsService(t, ctx)
	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	startPoint := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
	))

	branchName := "new-branch"
	request := &gitalypb.UserCreateBranchRequest{
		Repository: repo,
		BranchName: []byte(branchName),
		StartPoint: []byte(startPoint),
		User:       gittest.TestUser,
	}

	for _, hookName := range GitlabHooks {
		t.Run(hookName, func(t *testing.T) {
			defer gittest.Exec(t, cfg, "-C", repoPath, "branch", "-D", branchName)

			hookOutputTempPath := gittest.WriteEnvToCustomHook(t, repoPath, hookName)

			_, err := client.UserCreateBranch(ctx, request)
			require.NoError(t, err)

			output := string(testhelper.MustReadFile(t, hookOutputTempPath))
			require.Contains(t, output, "GL_USERNAME="+gittest.TestUser.GlUsername)
		})
	}
}

func TestUserCreateBranch_startPoint(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, client := setupOperationsService(t, ctx)

	// TODO: https://gitlab.com/gitlab-org/gitaly/-/issues/3331
	// The `startPoint` parameter automagically resolves branch names, so
	// heads/master => refs/heads/master. While the `branchName` parameter
	// prepends `heads/master`, so heads/master would become refs/heads/heads/master.
	// The issue tracks this inconsistency.
	//
	// We validate this behaviour by creating refs/heads + startPoint, but notice
	// that no collisions are noticied when running `TestUserCreateBranch`, because
	// it automagically resolves/adds `refs/heads` as required.
	testCases := []struct {
		desc       string
		branchName string
		startPoint string
	}{
		{
			desc:       "the startPoint parameter resolves heads/master => refs/heads/master",
			branchName: "topic",
			startPoint: "heads/master",
		},
		{
			desc:       "the StartPoint parameter resolves refs/heads/master => refs/heads/master",
			branchName: "topic2",
			startPoint: "refs/heads/master",
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

			commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
				gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
			), gittest.WithBranch("master"))

			localrepo := localrepo.NewTestRepo(t, cfg, repo)
			commit, err := localrepo.ReadCommit(ctx, commitID.Revision())
			require.NoError(t, err)

			gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
				gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
			), gittest.WithBranch(tc.startPoint))

			request := &gitalypb.UserCreateBranchRequest{
				Repository: repo,
				BranchName: []byte(tc.branchName),
				StartPoint: []byte(tc.startPoint),
				User:       gittest.TestUser,
			}

			response, err := client.UserCreateBranch(ctx, request)
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.UserCreateBranchResponse{
				Branch: &gitalypb.Branch{
					Name:         []byte(tc.branchName),
					TargetCommit: commit,
				},
			}, response)
		})
	}
}

func TestUserCreateBranch_hookFailure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, client := setupOperationsService(t, ctx)
	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
	))

	request := &gitalypb.UserCreateBranchRequest{
		Repository: repo,
		BranchName: []byte("new-branch"),
		StartPoint: []byte(commitID),
		User:       gittest.TestUser,
	}

	hookContent := []byte("#!/bin/sh\necho GL_ID=$GL_ID\nexit 1")

	expectedObject := "GL_ID=" + gittest.TestUser.GlId

	for _, hookName := range gitlabPreHooks {
		gittest.WriteCustomHook(t, repoPath, hookName, hookContent)

		_, err := client.UserCreateBranch(ctx, request)

		testhelper.RequireGrpcError(t, structerr.NewPermissionDenied("creation denied by custom hooks").WithDetail(
			&gitalypb.UserCreateBranchError{
				Error: &gitalypb.UserCreateBranchError_CustomHook{
					CustomHook: &gitalypb.CustomHookError{
						HookType: gitalypb.CustomHookError_HOOK_TYPE_PRERECEIVE,
						Stdout:   []byte(expectedObject + "\n"),
					},
				},
			},
		), err)

	}
}

func TestUserCreateBranch_failure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, client := setupOperationsService(t, ctx)
	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
	), gittest.WithBranch("master"))
	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/improve/awesome", commitID.String())

	testCases := []struct {
		desc       string
		repo       *gitalypb.Repository
		branchName string
		startPoint string
		user       *gitalypb.User
		err        error
	}{
		{
			desc:       "repository not provided",
			repo:       nil,
			branchName: "shiny-new-branch",
			startPoint: "",
			user:       gittest.TestUser,
			err:        structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc:       "empty start_point",
			repo:       repo,
			branchName: "shiny-new-branch",
			startPoint: "",
			user:       gittest.TestUser,
			err:        status.Error(codes.InvalidArgument, "empty start point"),
		},
		{
			desc:       "empty user",
			repo:       repo,
			branchName: "shiny-new-branch",
			startPoint: "master",
			user:       nil,
			err:        status.Error(codes.InvalidArgument, "empty user"),
		},
		{
			desc:       "non-existing starting point",
			repo:       repo,
			branchName: "new-branch",
			startPoint: "i-dont-exist",
			user:       gittest.TestUser,
			err:        status.Errorf(codes.FailedPrecondition, "revspec '%s' not found", "i-dont-exist"),
		},
		{
			desc:       "branch exists",
			repo:       repo,
			branchName: "master",
			startPoint: "master",
			user:       gittest.TestUser,
			err: testhelper.WithInterceptedMetadata(
				structerr.NewFailedPrecondition("reference update: reference already exists"),
				"reference", "refs/heads/master",
			),
		},
		{
			desc:       "conflicting with refs/heads/improve/awesome",
			repo:       repo,
			branchName: "improve",
			startPoint: "master",
			user:       gittest.TestUser,
			err: testhelper.WithInterceptedMetadataItems(
				structerr.NewFailedPrecondition("reference update: file directory conflict"),
				structerr.MetadataItem{Key: "conflicting_reference", Value: "refs/heads/improve"},
				structerr.MetadataItem{Key: "existing_reference", Value: "refs/heads/improve/awesome"},
			),
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			if t.Name() == "TestUserCreateBranch_failure/conflicting_with_refs/heads/improve/awesome" {
				testhelper.SkipWithReftable(t, "referenceExistsConflictRegex doesn't match output when using reftables")
			}

			t.Parallel()

			request := &gitalypb.UserCreateBranchRequest{
				Repository: tc.repo,
				BranchName: []byte(tc.branchName),
				StartPoint: []byte(tc.startPoint),
				User:       tc.user,
			}

			response, err := client.UserCreateBranch(ctx, request)
			testhelper.RequireGrpcError(t, tc.err, err)
			require.Empty(t, response)
		})
	}
}
