//go:build !gitaly_test_sha256

package operations

import (
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var rebaseBranchName = "many_files"

func TestUserRebaseConfirmable_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)

	pushOptions := []string{"ci.skip", "test=value"}
	cfg.Gitlab.URL = setupAndStartGitlabServer(t, gittest.GlID, "project-1", cfg, pushOptions...)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	repoCopyProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	branchOID := gittest.ResolveRevision(t, cfg, repoPath, rebaseBranchName)

	rebaseStream, err := client.UserRebaseConfirmable(ctx)
	require.NoError(t, err)

	preReceiveHookOutputPath := gittest.WriteEnvToCustomHook(t, repoPath, "pre-receive")
	postReceiveHookOutputPath := gittest.WriteEnvToCustomHook(t, repoPath, "post-receive")

	headerRequest := buildHeaderRequest(repoProto, gittest.TestUser, "1", rebaseBranchName, branchOID, repoCopyProto, "master")
	headerRequest.GetHeader().GitPushOptions = pushOptions
	require.NoError(t, rebaseStream.Send(headerRequest), "send header")

	firstResponse, err := rebaseStream.Recv()
	require.NoError(t, err, "receive first response")

	_, err = repo.ReadCommit(ctx, git.Revision(firstResponse.GetRebaseSha()))
	require.Equal(t, localrepo.ErrObjectNotFound, err, "commit should not exist in the normal repo given that it is quarantined")

	applyRequest := buildApplyRequest(true)
	require.NoError(t, rebaseStream.Send(applyRequest), "apply rebase")

	secondResponse, err := rebaseStream.Recv()
	require.NoError(t, err, "receive second response")

	_, err = rebaseStream.Recv()
	require.Equal(t, io.EOF, err)

	_, err = repo.ReadCommit(ctx, git.Revision(firstResponse.GetRebaseSha()))
	require.NoError(t, err)

	newBranchCommit := gittest.ResolveRevision(t, cfg, repoPath, rebaseBranchName)

	require.NotEqual(t, newBranchCommit, branchOID)
	require.Equal(t, newBranchCommit.String(), firstResponse.GetRebaseSha())

	require.True(t, secondResponse.GetRebaseApplied(), "the second rebase is applied")

	for _, outputPath := range []string{preReceiveHookOutputPath, postReceiveHookOutputPath} {
		output := string(testhelper.MustReadFile(t, outputPath))
		require.Contains(t, output, "GIT_PUSH_OPTION_COUNT=2")
		require.Contains(t, output, "GIT_PUSH_OPTION_0=ci.skip")
		require.Contains(t, output, "GIT_PUSH_OPTION_1=test=value")
	}
}

func TestUserRebaseConfirmable_skipEmptyCommits(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)

	// This is the base commit from which both "theirs" and "ours" branch from".
	baseCommit := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "README", Content: "a\nb\nc\nd\ne\nf\n"},
		),
	)

	// "theirs" changes the first line of the file to contain a "1".
	theirs := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(baseCommit),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "README", Content: "1\nb\nc\nd\ne\nf\n"},
		),
		gittest.WithMessage("theirs"),
		gittest.WithBranch("theirs"),
	)

	// We create two commits on "ours": the first one does the same changes as "theirs", but
	// with a different commit ID. It is expected to become empty. And the second commit is an
	// independent change which modifies the last line.
	oursBecomingEmpty := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(baseCommit),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "README", Content: "1\nb\nc\nd\ne\nf\n"},
		),
		gittest.WithMessage("ours but same change as theirs"),
	)
	ours := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(oursBecomingEmpty),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "README", Content: "1\nb\nc\nd\ne\n6\n"},
		),
		gittest.WithMessage("ours with additional changes"),
		gittest.WithBranch("ours"),
	)

	stream, err := client.UserRebaseConfirmable(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&gitalypb.UserRebaseConfirmableRequest{
		UserRebaseConfirmableRequestPayload: &gitalypb.UserRebaseConfirmableRequest_Header_{
			Header: &gitalypb.UserRebaseConfirmableRequest_Header{
				Repository: repoProto,
				User:       gittest.TestUser,
				RebaseId:   "something",
				// Note: the way UserRebaseConfirmable handles BranchSha and
				// RemoteBranch is really weird. What we're doing is to rebase
				// BranchSha on top of RemoteBranch, even though documentation of
				// the protobuf says that we're doing it the other way round. I
				// don't dare changing this now though, so I simply abide and write
				// the test with those weird semantics.
				Branch:           []byte("ours"),
				BranchSha:        ours.String(),
				RemoteRepository: repoProto,
				RemoteBranch:     []byte("theirs"),
				Timestamp:        &timestamppb.Timestamp{Seconds: 123456},
			},
		},
	}))

	response, err := stream.Recv()
	require.NoError(t, err)
	require.NoError(t, stream.Send(buildApplyRequest(true)))

	rebaseOID := git.ObjectID(response.GetRebaseSha())

	response, err = stream.Recv()
	require.NoError(t, err)
	require.True(t, response.GetRebaseApplied())

	rebaseCommit, err := localrepo.NewTestRepo(t, cfg, repoProto).ReadCommit(ctx, rebaseOID.Revision())
	require.NoError(t, err)
	testhelper.ProtoEqual(t, &gitalypb.GitCommit{
		Subject:   []byte("ours with additional changes"),
		Body:      []byte("ours with additional changes"),
		BodySize:  28,
		Id:        "ef7f98be1f753f1a9fa895d999a855611d691629",
		ParentIds: []string{theirs.String()},
		TreeId:    "b68aeb18813d7f2e180f2cc0bccc128511438b29",
		Author:    gittest.DefaultCommitAuthor,
		Committer: &gitalypb.CommitAuthor{
			Name:     gittest.TestUser.Name,
			Email:    gittest.TestUser.Email,
			Date:     &timestamppb.Timestamp{Seconds: 123456},
			Timezone: []byte("+0000"),
		},
	}, rebaseCommit)
}

func TestUserRebaseConfirmable_transaction(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	txManager := transaction.NewTrackingManager()

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(
		t, ctx,
		// Praefect would intercept our call and inject its own transaction.
		testserver.WithDisablePraefect(),
		testserver.WithTransactionManager(txManager),
	)
	cfg.Gitlab.URL = setupAndStartGitlabServer(t, gittest.GlID, "project-1", cfg)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	for _, tc := range []struct {
		desc                 string
		withTransaction      bool
		primary              bool
		expectedVotes        int
		expectPreReceiveHook bool
	}{
		{
			desc:                 "non-transactional does not vote but executes hook",
			expectedVotes:        0,
			expectPreReceiveHook: true,
		},
		{
			desc:                 "primary votes and executes hook",
			withTransaction:      true,
			primary:              true,
			expectedVotes:        2,
			expectPreReceiveHook: true,
		},
		{
			desc:                 "secondary votes but does not execute hook",
			withTransaction:      true,
			primary:              false,
			expectedVotes:        2,
			expectPreReceiveHook: false,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			preReceiveHookOutputPath := gittest.WriteEnvToCustomHook(t, repoPath, "pre-receive")

			txManager.Reset()

			ctx := ctx
			if tc.withTransaction {
				ctx = metadata.OutgoingToIncoming(ctx)

				var err error
				ctx, err = txinfo.InjectTransaction(ctx, 1, "node", tc.primary)
				require.NoError(t, err)
				ctx = metadata.IncomingToOutgoing(ctx)
			}

			branchCommitID, err := repo.ResolveRevision(ctx, git.Revision(rebaseBranchName))
			require.NoError(t, err)

			rebaseStream, err := client.UserRebaseConfirmable(ctx)
			require.NoError(t, err)

			headerRequest := buildHeaderRequest(repoProto, gittest.TestUser, "1", rebaseBranchName, branchCommitID, repoProto, "master")
			require.NoError(t, rebaseStream.Send(headerRequest))
			_, err = rebaseStream.Recv()
			require.NoError(t, err)

			require.NoError(t, rebaseStream.Send(buildApplyRequest(true)), "apply rebase")
			secondResponse, err := rebaseStream.Recv()
			require.NoError(t, err)
			require.True(t, secondResponse.GetRebaseApplied(), "the second rebase is applied")

			response, err := rebaseStream.Recv()
			require.Nil(t, response)
			require.Equal(t, io.EOF, err)

			require.Equal(t, tc.expectedVotes, len(txManager.Votes()))
			if tc.expectPreReceiveHook {
				require.FileExists(t, preReceiveHookOutputPath)
			} else {
				require.NoFileExists(t, preReceiveHookOutputPath)
			}
		})
	}
}

func TestUserRebaseConfirmable_stableCommitIDs(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)
	cfg.Gitlab.URL = setupAndStartGitlabServer(t, gittest.GlID, "project-1", cfg)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	rebaseStream, err := client.UserRebaseConfirmable(ctx)
	require.NoError(t, err)

	committerDate := &timestamppb.Timestamp{Seconds: 100000000}
	parentSha := gittest.ResolveRevision(t, cfg, repoPath, "master")

	require.NoError(t, rebaseStream.Send(&gitalypb.UserRebaseConfirmableRequest{
		UserRebaseConfirmableRequestPayload: &gitalypb.UserRebaseConfirmableRequest_Header_{
			Header: &gitalypb.UserRebaseConfirmableRequest_Header{
				Repository:       repoProto,
				User:             gittest.TestUser,
				RebaseId:         "1",
				Branch:           []byte(rebaseBranchName),
				BranchSha:        gittest.ResolveRevision(t, cfg, repoPath, rebaseBranchName).String(),
				RemoteRepository: repoProto,
				RemoteBranch:     []byte("master"),
				Timestamp:        committerDate,
			},
		},
	}), "send header")

	response, err := rebaseStream.Recv()
	require.NoError(t, err, "receive first response")
	require.Equal(t, "c52b98024db0d3af0ccb20ed2a3a93a21cfbba87", response.GetRebaseSha())

	applyRequest := buildApplyRequest(true)
	require.NoError(t, rebaseStream.Send(applyRequest), "apply rebase")

	response, err = rebaseStream.Recv()
	require.NoError(t, err, "receive second response")
	require.True(t, response.GetRebaseApplied())

	_, err = rebaseStream.Recv()
	require.Equal(t, io.EOF, err)

	commit, err := repo.ReadCommit(ctx, git.Revision(rebaseBranchName))
	require.NoError(t, err, "look up git commit")
	testhelper.ProtoEqual(t, &gitalypb.GitCommit{
		Subject:   []byte("Add a directory with many files to allow testing of default 1,000 entry limit"),
		Body:      []byte("Add a directory with many files to allow testing of default 1,000 entry limit\n\nFor performance reasons, GitLab will add a file viewer limit and only show\nthe first 1,000 entries in a directory. Having this directory with many\nempty files in the test project will make the test easy.\n"),
		BodySize:  283,
		Id:        "c52b98024db0d3af0ccb20ed2a3a93a21cfbba87",
		ParentIds: []string{parentSha.String()},
		TreeId:    "d0305132f880aa0ab4102e56a09cf1343ba34893",
		Author: &gitalypb.CommitAuthor{
			Name:  []byte("Drew Blessing"),
			Email: []byte("drew@gitlab.com"),
			// Nanoseconds get ignored because commit timestamps aren't that granular.
			Date:     &timestamppb.Timestamp{Seconds: 1510610637},
			Timezone: []byte("-0600"),
		},
		Committer: &gitalypb.CommitAuthor{
			Name:  gittest.TestUser.Name,
			Email: gittest.TestUser.Email,
			// Nanoseconds get ignored because commit timestamps aren't that granular.
			Date:     committerDate,
			Timezone: []byte("+0000"),
		},
	}, commit)
}

func TestUserRebaseConfirmable_inputValidation(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, repo, repoPath, client := setupOperationsService(t, ctx)

	repoCopy, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])

	branchCommitID := gittest.ResolveRevision(t, cfg, repoPath, rebaseBranchName)

	testCases := []struct {
		desc string
		req  *gitalypb.UserRebaseConfirmableRequest
	}{
		{
			desc: "empty Repository",
			req:  buildHeaderRequest(nil, gittest.TestUser, "1", rebaseBranchName, branchCommitID, repoCopy, "master"),
		},
		{
			desc: "empty User",
			req:  buildHeaderRequest(repo, nil, "1", rebaseBranchName, branchCommitID, repoCopy, "master"),
		},
		{
			desc: "empty Branch",
			req:  buildHeaderRequest(repo, gittest.TestUser, "1", "", branchCommitID, repoCopy, "master"),
		},
		{
			desc: "empty BranchSha",
			req:  buildHeaderRequest(repo, gittest.TestUser, "1", rebaseBranchName, "", repoCopy, "master"),
		},
		{
			desc: "empty RemoteRepository",
			req:  buildHeaderRequest(repo, gittest.TestUser, "1", rebaseBranchName, branchCommitID, nil, "master"),
		},
		{
			desc: "empty RemoteBranch",
			req:  buildHeaderRequest(repo, gittest.TestUser, "1", rebaseBranchName, branchCommitID, repoCopy, ""),
		},
		{
			desc: "invalid branch name",
			req:  buildHeaderRequest(repo, gittest.TestUser, "1", rebaseBranchName, branchCommitID, repoCopy, "+dev:master"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			rebaseStream, err := client.UserRebaseConfirmable(ctx)
			require.NoError(t, err)

			require.NoError(t, rebaseStream.Send(tc.req), "send request header")

			firstResponse, err := rebaseStream.Recv()
			testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
			require.Contains(t, err.Error(), tc.desc)
			require.Empty(t, firstResponse.GetRebaseSha(), "rebase sha on first response")
		})
	}
}

func TestUserRebaseConfirmable_abortViaClose(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, _, _, client := setupOperationsService(t, ctx)

	testCases := []struct {
		req       *gitalypb.UserRebaseConfirmableRequest
		closeSend bool
		desc      string
		code      codes.Code
	}{
		{req: &gitalypb.UserRebaseConfirmableRequest{}, desc: "empty request, don't close", code: codes.FailedPrecondition},
		{req: &gitalypb.UserRebaseConfirmableRequest{}, closeSend: true, desc: "empty request and close", code: codes.FailedPrecondition},
		{closeSend: true, desc: "no request just close", code: codes.Internal},
	}

	for i, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			createRepoOpts := gittest.CreateRepositoryConfig{Seed: gittest.SeedGitLabTest}
			testRepo, testRepoPath := gittest.CreateRepository(ctx, t, cfg, createRepoOpts)
			testRepoCopy, _ := gittest.CreateRepository(ctx, t, cfg, createRepoOpts)

			branchCommitID := gittest.ResolveRevision(t, cfg, testRepoPath, rebaseBranchName)

			headerRequest := buildHeaderRequest(testRepo, gittest.TestUser, fmt.Sprintf("%v", i), rebaseBranchName, branchCommitID, testRepoCopy, "master")

			rebaseStream, err := client.UserRebaseConfirmable(ctx)
			require.NoError(t, err)

			require.NoError(t, rebaseStream.Send(headerRequest), "send first request")

			firstResponse, err := rebaseStream.Recv()
			require.NoError(t, err, "receive first response")
			require.NotEmpty(t, firstResponse.GetRebaseSha(), "rebase sha on first response")

			if tc.req != nil {
				require.NoError(t, rebaseStream.Send(tc.req), "send second request")
			}

			if tc.closeSend {
				require.NoError(t, rebaseStream.CloseSend(), "close request stream from client")
			}

			secondResponse, err := rebaseRecvTimeout(rebaseStream, 1*time.Second)
			if err == errRecvTimeout {
				t.Fatal(err)
			}

			require.False(t, secondResponse.GetRebaseApplied(), "rebase should not have been applied")
			require.Error(t, err)
			testhelper.RequireGrpcCode(t, err, tc.code)

			newBranchCommitID := gittest.ResolveRevision(t, cfg, testRepoPath, rebaseBranchName)
			require.Equal(t, newBranchCommitID, branchCommitID, "branch should not change when the rebase is aborted")
		})
	}
}

func TestUserRebaseConfirmable_abortViaApply(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	testRepoCopy, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	branchCommitID := gittest.ResolveRevision(t, cfg, repoPath, rebaseBranchName)

	rebaseStream, err := client.UserRebaseConfirmable(ctx)
	require.NoError(t, err)

	headerRequest := buildHeaderRequest(repoProto, gittest.TestUser, "1", rebaseBranchName, branchCommitID, testRepoCopy, "master")
	require.NoError(t, rebaseStream.Send(headerRequest), "send header")

	firstResponse, err := rebaseStream.Recv()
	require.NoError(t, err, "receive first response")

	_, err = repo.ReadCommit(ctx, git.Revision(firstResponse.GetRebaseSha()))
	require.Equal(t, localrepo.ErrObjectNotFound, err, "commit should not exist in the normal repo given that it is quarantined")

	applyRequest := buildApplyRequest(false)
	require.NoError(t, rebaseStream.Send(applyRequest), "apply rebase")

	secondResponse, err := rebaseStream.Recv()
	require.Error(t, err, "second response should have error")
	testhelper.RequireGrpcCode(t, err, codes.FailedPrecondition)
	require.False(t, secondResponse.GetRebaseApplied(), "the second rebase is not applied")

	_, err = repo.ReadCommit(ctx, git.Revision(firstResponse.GetRebaseSha()))
	require.Equal(t, localrepo.ErrObjectNotFound, err, "commit should have been discarded")

	newBranchCommitID := gittest.ResolveRevision(t, cfg, repoPath, rebaseBranchName)
	require.Equal(t, branchCommitID, newBranchCommitID, "branch should not change when the rebase is not applied")
	require.NotEqual(t, newBranchCommitID, firstResponse.GetRebaseSha(), "branch should not be the sha returned when the rebase is not applied")
}

func TestUserRebaseConfirmable_preReceiveError(t *testing.T) {
	t.Parallel()

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, testhelper.Context(t))
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	repoCopyProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	branchCommitID := gittest.ResolveRevision(t, cfg, repoPath, rebaseBranchName)

	hookContent := []byte("#!/bin/sh\necho 'failure'\nexit 1")

	for i, hookName := range GitlabPreHooks {
		t.Run(hookName, func(t *testing.T) {
			gittest.WriteCustomHook(t, repoPath, hookName, hookContent)

			rebaseStream, err := client.UserRebaseConfirmable(ctx)
			require.NoError(t, err)

			headerRequest := buildHeaderRequest(repoProto, gittest.TestUser, fmt.Sprintf("%v", i), rebaseBranchName, branchCommitID, repoCopyProto, "master")
			require.NoError(t, rebaseStream.Send(headerRequest), "send header")

			firstResponse, err := rebaseStream.Recv()
			require.NoError(t, err, "receive first response")

			_, err = repo.ReadCommit(ctx, git.Revision(firstResponse.GetRebaseSha()))
			require.Equal(t, localrepo.ErrObjectNotFound, err, "commit should not exist in the normal repo given that it is quarantined")

			applyRequest := buildApplyRequest(true)
			require.NoError(t, rebaseStream.Send(applyRequest), "apply rebase")

			secondResponse, err := rebaseStream.Recv()
			require.Nil(t, secondResponse)

			testhelper.RequireGrpcError(t, errWithDetails(t,
				helper.ErrPermissionDeniedf(`access check: "running %s hooks: failure\n"`, hookName),
				&gitalypb.UserRebaseConfirmableError{
					Error: &gitalypb.UserRebaseConfirmableError_AccessCheck{
						AccessCheck: &gitalypb.AccessCheckError{
							ErrorMessage: "failure\n",
						},
					},
				},
			), err)

			_, err = repo.ReadCommit(ctx, git.Revision(firstResponse.GetRebaseSha()))
			if hookName == "pre-receive" {
				require.Equal(t, localrepo.ErrObjectNotFound, err, "commit should have been discarded")
			} else {
				require.NoError(t, err)
			}

			newBranchCommitID := gittest.ResolveRevision(t, cfg, repoPath, rebaseBranchName)
			require.Equal(t, branchCommitID, newBranchCommitID, "branch should not change when the rebase fails due to PreReceiveError")
			require.NotEqual(t, newBranchCommitID, firstResponse.GetRebaseSha(), "branch should not be the sha returned when the rebase fails due to PreReceiveError")
		})
	}
}

func TestUserRebaseConfirmable_gitError(t *testing.T) {
	t.Parallel()

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, testhelper.Context(t))

	repoCopyProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	targetBranch := "rebase-encoding-failure-trigger"
	targetBranchCommitID := gittest.ResolveRevision(t, cfg, repoPath, targetBranch)
	sourceBranchCommitID := gittest.ResolveRevision(t, cfg, repoPath, "master")

	rebaseStream, err := client.UserRebaseConfirmable(ctx)
	require.NoError(t, err)

	headerRequest := buildHeaderRequest(repoProto, gittest.TestUser, "1", targetBranch, targetBranchCommitID, repoCopyProto, "master")

	require.NoError(t, rebaseStream.Send(headerRequest), "send header")

	response, err := rebaseStream.Recv()
	require.Nil(t, response)
	testhelper.RequireGrpcError(t, errWithDetails(t,
		helper.ErrFailedPrecondition(errors.New(`rebasing commits: rebase: commit "eb8f5fb9523b868cef583e09d4bf70b99d2dd404": there are conflicting files`)),
		&gitalypb.UserRebaseConfirmableError{
			Error: &gitalypb.UserRebaseConfirmableError_RebaseConflict{
				RebaseConflict: &gitalypb.MergeConflictError{
					ConflictingFiles: [][]byte{
						[]byte("README.md"),
					},
					ConflictingCommitIds: []string{
						sourceBranchCommitID.String(),
						targetBranchCommitID.String(),
					},
				},
			},
		},
	), err)

	newBranchCommitID := gittest.ResolveRevision(t, cfg, repoPath, targetBranch)
	require.Equal(t, targetBranchCommitID, newBranchCommitID, "branch should not change when the rebase fails due to GitError")
}

func TestUserRebaseConfirmable_deletedFileInLocalRepo(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	localRepoProto, localRepoPath := gittest.CreateRepository(ctx, t, cfg)
	localRepo := localrepo.NewTestRepo(t, cfg, localRepoProto)

	remoteRepoProto, remoteRepoPath := gittest.CreateRepository(ctx, t, cfg)

	// Write the root commit into both repositories as common history.
	var rootCommitID git.ObjectID
	for _, path := range []string{localRepoPath, remoteRepoPath} {
		rootCommitID = gittest.WriteCommit(t, cfg, path,
			gittest.WithTreeEntries(
				gittest.TreeEntry{Path: "change-me", Mode: "100644", Content: "unchanged contents"},
				gittest.TreeEntry{Path: "delete-me", Mode: "100644", Content: "useless stuff"},
			),
		)
	}

	// Write a commit into the local repository that deletes a single file.
	localCommitID := gittest.WriteCommit(t, cfg, localRepoPath,
		gittest.WithParents(rootCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "change-me", Mode: "100644", Content: "unchanged contents"},
		),
		gittest.WithBranch("local"),
	)

	// And then finally write a commit into the remote repository that changes a different file.
	gittest.WriteCommit(t, cfg, remoteRepoPath,
		gittest.WithParents(rootCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "change-me", Mode: "100644", Content: "modified contents"},
			gittest.TreeEntry{Path: "delete-me", Mode: "100644", Content: "useless stuff"},
		),
		gittest.WithBranch("remote"),
	)

	// Send the first request to tell the server to perform the rebase.
	stream, err := client.UserRebaseConfirmable(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(buildHeaderRequest(
		localRepoProto, gittest.TestUser, "1", "local", localCommitID, remoteRepoProto, "remote",
	)))
	firstResponse, err := stream.Recv()
	require.NoError(t, err)

	_, err = localRepo.ReadCommit(ctx, git.Revision(firstResponse.GetRebaseSha()))
	require.Equal(t, localrepo.ErrObjectNotFound, err, "commit should not exist in the normal repo given that it is quarantined")

	// Send the second request to tell the server to apply the rebase.
	require.NoError(t, stream.Send(buildApplyRequest(true)))
	secondResponse, err := stream.Recv()
	require.NoError(t, err)
	require.True(t, secondResponse.GetRebaseApplied())

	_, err = stream.Recv()
	require.Equal(t, io.EOF, err)

	newBranchCommitID := gittest.ResolveRevision(t, cfg, localRepoPath, "local")

	_, err = localRepo.ReadCommit(ctx, git.Revision(firstResponse.GetRebaseSha()))
	require.NoError(t, err)
	require.NotEqual(t, newBranchCommitID, localCommitID)
	require.Equal(t, newBranchCommitID.String(), firstResponse.GetRebaseSha())
}

func TestUserRebaseConfirmable_deletedFileInRemoteRepo(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	localRepoProto, localRepoPath := gittest.CreateRepository(ctx, t, cfg)
	localRepo := localrepo.NewTestRepo(t, cfg, localRepoProto)

	remoteRepoProto, remoteRepoPath := gittest.CreateRepository(ctx, t, cfg)

	// Write the root commit into both repositories as common history.
	var rootCommitID git.ObjectID
	for _, path := range []string{localRepoPath, remoteRepoPath} {
		rootCommitID = gittest.WriteCommit(t, cfg, path,
			gittest.WithTreeEntries(
				gittest.TreeEntry{Path: "unchanged", Mode: "100644", Content: "unchanged contents"},
				gittest.TreeEntry{Path: "delete-me", Mode: "100644", Content: "useless stuff"},
			),
			gittest.WithBranch("local"),
		)
	}

	// Write a commit into the remote repository that deletes a file.
	remoteCommitID := gittest.WriteCommit(t, cfg, remoteRepoPath,
		gittest.WithParents(rootCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "unchanged", Mode: "100644", Content: "unchanged contents"},
		),
		gittest.WithBranch("remote"),
	)

	_, err := localRepo.ReadCommit(ctx, remoteCommitID.Revision())
	require.Equal(t, localrepo.ErrObjectNotFound, err, "remote commit should not yet exist in local repository")

	// Send the first request to tell the server to perform the rebase.
	rebaseStream, err := client.UserRebaseConfirmable(ctx)
	require.NoError(t, err)
	require.NoError(t, rebaseStream.Send(buildHeaderRequest(
		localRepoProto, gittest.TestUser, "1", "local", rootCommitID, remoteRepoProto, "remote",
	)))
	firstResponse, err := rebaseStream.Recv()
	require.NoError(t, err)

	_, err = localRepo.ReadCommit(ctx, remoteCommitID.Revision())
	require.Equal(t, localrepo.ErrObjectNotFound, err, "commit should not exist in the normal repo given that it is quarantined")

	// Send the second request to tell the server to apply the rebase.
	require.NoError(t, rebaseStream.Send(buildApplyRequest(true)))
	secondResponse, err := rebaseStream.Recv()
	require.NoError(t, err)
	require.True(t, secondResponse.GetRebaseApplied(), "the second rebase is applied")

	_, err = rebaseStream.Recv()
	require.Equal(t, io.EOF, err)

	_, err = localRepo.ReadCommit(ctx, remoteCommitID.Revision())
	require.NoError(t, err)

	rebasedBranchCommitID := gittest.ResolveRevision(t, cfg, localRepoPath, "local")
	require.NotEqual(t, rebasedBranchCommitID, rootCommitID)
	require.Equal(t, rebasedBranchCommitID.String(), firstResponse.GetRebaseSha())
}

func TestUserRebaseConfirmable_failedWithCode(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)

	branchCommitID := gittest.ResolveRevision(t, cfg, repoPath, rebaseBranchName)

	testCases := []struct {
		desc               string
		buildHeaderRequest func() *gitalypb.UserRebaseConfirmableRequest
		expectedCode       codes.Code
	}{
		{
			desc: "non-existing storage",
			buildHeaderRequest: func() *gitalypb.UserRebaseConfirmableRequest {
				repo := proto.Clone(repoProto).(*gitalypb.Repository)
				repo.StorageName = "@this-storage-does-not-exist"

				return buildHeaderRequest(repo, gittest.TestUser, "1", rebaseBranchName, branchCommitID, repo, "master")
			},
			expectedCode: codes.InvalidArgument,
		},
		{
			desc: "missing repository path",
			buildHeaderRequest: func() *gitalypb.UserRebaseConfirmableRequest {
				repo := proto.Clone(repoProto).(*gitalypb.Repository)
				repo.RelativePath = ""

				return buildHeaderRequest(repo, gittest.TestUser, "1", rebaseBranchName, branchCommitID, repo, "master")
			},
			expectedCode: codes.InvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			rebaseStream, err := client.UserRebaseConfirmable(ctx)
			require.NoError(t, err)

			headerRequest := tc.buildHeaderRequest()
			require.NoError(t, rebaseStream.Send(headerRequest), "send header")

			_, err = rebaseStream.Recv()
			testhelper.RequireGrpcCode(t, err, tc.expectedCode)
		})
	}
}

func rebaseRecvTimeout(bidi gitalypb.OperationService_UserRebaseConfirmableClient, timeout time.Duration) (*gitalypb.UserRebaseConfirmableResponse, error) {
	type responseError struct {
		response *gitalypb.UserRebaseConfirmableResponse
		err      error
	}
	responseCh := make(chan responseError, 1)

	go func() {
		resp, err := bidi.Recv()
		responseCh <- responseError{resp, err}
	}()

	select {
	case respErr := <-responseCh:
		return respErr.response, respErr.err
	case <-time.After(timeout):
		return nil, errRecvTimeout
	}
}

func buildHeaderRequest(repo *gitalypb.Repository, user *gitalypb.User, rebaseID string, branchName string, commitID git.ObjectID, remoteRepo *gitalypb.Repository, remoteBranch string) *gitalypb.UserRebaseConfirmableRequest {
	return &gitalypb.UserRebaseConfirmableRequest{
		UserRebaseConfirmableRequestPayload: &gitalypb.UserRebaseConfirmableRequest_Header_{
			Header: &gitalypb.UserRebaseConfirmableRequest_Header{
				Repository:       repo,
				User:             user,
				RebaseId:         rebaseID,
				Branch:           []byte(branchName),
				BranchSha:        commitID.String(),
				RemoteRepository: remoteRepo,
				RemoteBranch:     []byte(remoteBranch),
			},
		},
	}
}

func buildApplyRequest(apply bool) *gitalypb.UserRebaseConfirmableRequest {
	return &gitalypb.UserRebaseConfirmableRequest{
		UserRebaseConfirmableRequestPayload: &gitalypb.UserRebaseConfirmableRequest_Apply{
			Apply: apply,
		},
	}
}
