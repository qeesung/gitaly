package operations

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestUserMergeToRef_successful(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(
		t,
		testUserMergeToRefSuccessful,
	)
}

func testUserMergeToRefSuccessful(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	branch := "main"
	firstParentRef := "refs/heads/" + branch
	branchSha := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithReference(firstParentRef),
		gittest.WithMessage("branch commit"),
	).String()
	sourceSha := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch("source-branch"),
		gittest.WithMessage("source branch commit"),
	).String()
	existingTargetRef := []byte("refs/merge-requests/x/written")
	existingTargetRefOid := gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference(string(existingTargetRef)))
	emptyTargetRef := []byte("refs/merge-requests/x/merge")
	mergeCommitMessage := "Merged by Gitaly"

	testCases := []struct {
		desc           string
		user           *gitalypb.User
		branch         []byte
		targetRef      []byte
		emptyRef       bool
		sourceSha      string
		message        string
		firstParentRef []byte
		expectedOldOid string
	}{
		{
			desc:           "empty target ref merge",
			user:           gittest.TestUser,
			targetRef:      emptyTargetRef,
			emptyRef:       true,
			sourceSha:      sourceSha,
			message:        mergeCommitMessage,
			firstParentRef: []byte(firstParentRef),
		},
		{
			desc:           "existing target ref",
			user:           gittest.TestUser,
			targetRef:      existingTargetRef,
			emptyRef:       false,
			sourceSha:      sourceSha,
			message:        mergeCommitMessage,
			firstParentRef: []byte(firstParentRef),
		},
		{
			desc:           "existing target ref with optimistic lock",
			user:           gittest.TestUser,
			targetRef:      existingTargetRef,
			emptyRef:       false,
			sourceSha:      sourceSha,
			message:        mergeCommitMessage,
			firstParentRef: []byte(firstParentRef),
			expectedOldOid: existingTargetRefOid.String(),
		},
		{
			desc:      "branch is specified and firstParentRef is empty",
			user:      gittest.TestUser,
			branch:    []byte(branch),
			targetRef: existingTargetRef,
			emptyRef:  false,
			sourceSha: sourceSha,
			message:   mergeCommitMessage,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			// reset target ref
			gittest.WriteRef(t, cfg, repoPath, git.ReferenceName(existingTargetRef), existingTargetRefOid)

			request := &gitalypb.UserMergeToRefRequest{
				Repository:     repoProto,
				User:           testCase.user,
				Branch:         testCase.branch,
				TargetRef:      testCase.targetRef,
				SourceSha:      testCase.sourceSha,
				Message:        []byte(testCase.message),
				FirstParentRef: testCase.firstParentRef,
				ExpectedOldOid: testCase.expectedOldOid,
			}

			commitBeforeRefMerge, fetchRefBeforeMergeErr := repo.ReadCommit(ctx, git.Revision(testCase.targetRef))
			if testCase.emptyRef {
				require.Error(t, fetchRefBeforeMergeErr, "error when fetching empty ref commit")
			} else {
				require.NoError(t, fetchRefBeforeMergeErr, "no error when fetching existing ref")
			}

			resp, err := client.UserMergeToRef(ctx, request)
			require.NoError(t, err)

			commit, err := repo.ReadCommit(ctx, git.Revision(testCase.targetRef))
			require.NoError(t, err, "look up git commit after call has finished")

			// Asserts commit parent SHAs
			require.Equal(t, []string{branchSha, testCase.sourceSha}, commit.ParentIds, "merge commit parents must be the sha before HEAD and source sha")

			require.True(t, strings.HasPrefix(string(commit.Body), testCase.message), "expected %q to start with %q", commit.Body, testCase.message)

			// Asserts author
			author := commit.Author
			require.Equal(t, gittest.TestUser.Name, author.Name)
			require.Equal(t, gittest.TestUser.Email, author.Email)
			require.Equal(t, gittest.TimezoneOffset, string(author.Timezone))

			require.Equal(t, resp.CommitId, commit.Id)

			// Calling commitBeforeRefMerge.Id in a non-existent
			// commit will raise a null-pointer error.
			if !testCase.emptyRef {
				require.NotEqual(t, commit.Id, commitBeforeRefMerge.Id)
			}
		})
	}
}

func TestUserMergeToRef_conflicts(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(
		t,
		testUserMergeToRefConflicts,
	)
}

func testUserMergeToRefConflicts(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	common := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "a", Mode: "100644", Content: "base"},
	))
	left := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(common), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "a", Mode: "100644", Content: "conflicting"},
	), gittest.WithBranch("branch"))
	right := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(common), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "a", Mode: "100644", Content: "change"},
	))
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	gittest.Exec(t, cfg, "-C", repoPath, "branch", "parent-ref", right.String())

	t.Run("disallow conflicts to be merged", func(t *testing.T) {
		request := &gitalypb.UserMergeToRefRequest{
			Repository:     repoProto,
			User:           gittest.TestUser,
			TargetRef:      []byte("refs/merge-requests/x/written"),
			SourceSha:      left.String(),
			Message:        []byte("message1"),
			FirstParentRef: []byte("refs/heads/parent-ref"),
		}

		_, err := client.UserMergeToRef(ctx, request)
		testhelper.RequireGrpcError(t, structerr.NewFailedPrecondition("Failed to create merge commit for source_sha %s and target_sha %s at refs/merge-requests/x/written", left, right), err)

		hasRevision, err := repo.HasRevision(ctx, "refs/merge-requests/written")
		require.NoError(t, err)
		require.False(t, hasRevision, "branch should not have been created")
	})

	t.Run("failing merge does not update target reference if skipping precursor update-ref", func(t *testing.T) {
		request := &gitalypb.UserMergeToRefRequest{
			Repository:     repoProto,
			User:           gittest.TestUser,
			TargetRef:      []byte("refs/merge-requests/foo"),
			SourceSha:      left.String(),
			Message:        []byte("message1"),
			FirstParentRef: []byte("refs/heads/parent-ref"),
		}

		_, err := client.UserMergeToRef(ctx, request)
		testhelper.RequireGrpcError(t, structerr.NewFailedPrecondition("Failed to create merge commit for source_sha %s and target_sha %s at refs/merge-requests/foo", left, right), err)

		hasRevision, err := repo.HasRevision(ctx, "refs/merge-requests/foo")
		require.NoError(t, err)
		require.False(t, hasRevision, "branch should not have been created")
	})

	t.Run("target reference is ambiguous", func(t *testing.T) {
		gittest.WriteRef(t, cfg, repoPath, "refs/merge-requests/x/written-before", right)

		request := &gitalypb.UserMergeToRefRequest{
			Repository:     repoProto,
			User:           gittest.TestUser,
			TargetRef:      []byte("refs/merge-requests/x/written*"),
			SourceSha:      left.String(),
			Message:        []byte("message1"),
			FirstParentRef: []byte("refs/heads/parent-ref"),
		}

		_, err := client.UserMergeToRef(ctx, request)
		testhelper.RequireGrpcError(t, structerr.NewInvalidArgument(`target reference is ambiguous: reference is ambiguous: conflicts with "refs/merge-requests/x/written-before"`), err)
	})
}

func TestUserMergeToRef_stableMergeID(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(
		t,
		testUserMergeToRefStableMergeID,
	)
}

func testUserMergeToRefStableMergeID(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	common := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "a", Mode: "100644", Content: "1\n2\n3\n4\n5\n6\n7\n8\n"},
	))
	left := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(common), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "a", Mode: "100644", Content: "1\n2\n3\n4\n5\n6\n7\nh\n"},
	), gittest.WithBranch("branch"))
	right := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(common), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "a", Mode: "100644", Content: "a\n2\n3\n4\n5\n6\n7\n8\n"},
	))

	response, err := client.UserMergeToRef(ctx, &gitalypb.UserMergeToRefRequest{
		Repository:     repoProto,
		User:           gittest.TestUser,
		FirstParentRef: []byte("refs/heads/branch"),
		TargetRef:      []byte("refs/merge-requests/x/written"),
		SourceSha:      right.String(),
		Message:        []byte("Merge message"),
		Timestamp:      &timestamppb.Timestamp{Seconds: 12, Nanos: 34},
	})
	require.NoError(t, err)
	require.Equal(t, gittest.ObjectHashDependent(t, map[string]string{
		"sha1":   "4f295f8bb631748c7c2d0eb628d019c7802421e3",
		"sha256": "0af69a0b9550e3943892537d429a385cdc3d3ab309833744c7478a60055882e3",
	}), response.CommitId)

	commit, err := repo.ReadCommit(ctx, git.Revision("refs/merge-requests/x/written"))
	require.NoError(t, err, "look up git commit after call has finished")
	testhelper.ProtoEqual(t, &gitalypb.GitCommit{
		Subject:  []byte("Merge message"),
		Body:     []byte("Merge message"),
		BodySize: 13,
		Id: gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "4f295f8bb631748c7c2d0eb628d019c7802421e3",
			"sha256": "0af69a0b9550e3943892537d429a385cdc3d3ab309833744c7478a60055882e3",
		}),
		ParentIds: []string{
			left.String(),
			right.String(),
		},
		TreeId: gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "7ed20b777cfc00066401a4d4aa1bab50f487f346",
			"sha256": "9ff5a6fc7476b3297e176d8c7dec1c36a7a58dd68e387570229f94cce65d299c",
		}),
		Author: &gitalypb.CommitAuthor{
			Name:  gittest.TestUser.Name,
			Email: gittest.TestUser.Email,
			// Nanoseconds get ignored because commit timestamps aren't that granular.
			Date:     &timestamppb.Timestamp{Seconds: 12},
			Timezone: []byte(gittest.TimezoneOffset),
		},
		Committer: &gitalypb.CommitAuthor{
			Name:  gittest.TestUser.Name,
			Email: gittest.TestUser.Email,
			// Nanoseconds get ignored because commit timestamps aren't that granular.
			Date:     &timestamppb.Timestamp{Seconds: 12},
			Timezone: []byte(gittest.TimezoneOffset),
		},
	}, commit)
}

func TestUserMergeToRef_failure(t *testing.T) {
	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserMergeToRefFailure)
}

func testUserMergeToRefFailure(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

	validBranchName := "main"
	gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(validBranchName),
		gittest.WithMessage("branch commit"),
	)
	validSourceSha := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch("source-branch"),
		gittest.WithMessage("source branch commit"),
	).String()
	validTargetRef := []byte("refs/merge-requests/x/merge")

	testCases := []struct {
		desc           string
		user           *gitalypb.User
		branch         []byte
		targetRef      []byte
		sourceSha      string
		repo           *gitalypb.Repository
		code           codes.Code
		expectedOldOid string
		message        []byte
	}{
		{
			desc:      "empty repository",
			user:      gittest.TestUser,
			branch:    []byte(validBranchName),
			sourceSha: validSourceSha,
			targetRef: validTargetRef,
			code:      codes.InvalidArgument,
		},
		{
			desc:      "empty user",
			repo:      repoProto,
			branch:    []byte(validBranchName),
			sourceSha: validSourceSha,
			targetRef: validTargetRef,
			code:      codes.InvalidArgument,
		},
		{
			desc:      "empty source SHA",
			repo:      repoProto,
			user:      gittest.TestUser,
			branch:    []byte(validBranchName),
			targetRef: validTargetRef,
			code:      codes.InvalidArgument,
		},
		{
			desc:      "non-existing commit",
			repo:      repoProto,
			user:      gittest.TestUser,
			branch:    []byte(validBranchName),
			sourceSha: "f001",
			targetRef: validTargetRef,
			code:      codes.InvalidArgument,
		},
		{
			desc:      "empty branch and first parent ref",
			repo:      repoProto,
			user:      gittest.TestUser,
			sourceSha: validSourceSha,
			targetRef: validTargetRef,
			code:      codes.InvalidArgument,
		},
		{
			desc:      "invalid target ref",
			repo:      repoProto,
			user:      gittest.TestUser,
			branch:    []byte(validBranchName),
			sourceSha: validSourceSha,
			targetRef: []byte("refs/heads/branch"),
			code:      codes.InvalidArgument,
		},
		{
			desc:      "non-existing branch",
			repo:      repoProto,
			user:      gittest.TestUser,
			branch:    []byte("this-isnt-real"),
			sourceSha: validSourceSha,
			targetRef: validTargetRef,
			code:      codes.InvalidArgument,
		},
		{
			desc:           "non-matching expected_object_id",
			repo:           repoProto,
			user:           gittest.TestUser,
			branch:         []byte(validBranchName),
			sourceSha:      validSourceSha,
			targetRef:      validTargetRef,
			code:           codes.FailedPrecondition,
			message:        []byte("some merge commit message"),
			expectedOldOid: validSourceSha, // arbitrary value that differs from current target ref OID
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			request := &gitalypb.UserMergeToRefRequest{
				Repository:     testCase.repo,
				User:           testCase.user,
				Branch:         testCase.branch,
				SourceSha:      testCase.sourceSha,
				TargetRef:      testCase.targetRef,
				Message:        testCase.message,
				ExpectedOldOid: testCase.expectedOldOid,
			}
			_, err := client.UserMergeToRef(ctx, request)
			testhelper.RequireGrpcCode(t, err, testCase.code)
		})
	}
}

func TestUserMergeToRef_ignoreHooksRequest(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(
		t,
		testUserMergeToRefIgnoreHooksRequest,
	)
}

func testUserMergeToRefIgnoreHooksRequest(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	common := gittest.WriteCommit(t, cfg, repoPath)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(common), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "a", Mode: "100644", Content: "a"},
	), gittest.WithBranch("merge-me"))
	commitToMerge := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(common), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "b", Mode: "100644", Content: "b"},
	))

	request := &gitalypb.UserMergeToRefRequest{
		Repository: repo,
		SourceSha:  commitToMerge.String(),
		Branch:     []byte("merge-me"),
		TargetRef:  []byte("refs/merge-requests/x/merge"),
		User:       gittest.TestUser,
		Message:    []byte("Merge"),
	}

	hookContent := []byte("#!/bin/sh\necho 'failure'\nexit 1")

	for _, hookName := range gitlabPreHooks {
		t.Run(hookName, func(t *testing.T) {
			gittest.WriteCustomHook(t, repoPath, hookName, hookContent)

			_, err := client.UserMergeToRef(ctx, request)
			require.NoError(t, err)
		})
	}
}
