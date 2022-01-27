package operations

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/lstree"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSuccessfulUserUpdateSubmoduleRequest(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	// This reference is created to check that we can correctly commit onto
	// a branch which has a name starting with "refs/heads/".
	currentOID, err := repo.ResolveRevision(ctx, "refs/heads/master")
	require.NoError(t, err)
	require.NoError(t, repo.UpdateRef(ctx, "refs/heads/refs/heads/master", currentOID, git.ZeroOID))

	// If something uses the branch name as an unqualified reference, then
	// git would return the tag instead of the branch. We thus create a tag
	// with a different OID than the current master branch.
	prevOID, err := repo.ResolveRevision(ctx, "refs/heads/master~")
	require.NoError(t, err)
	require.NoError(t, repo.UpdateRef(ctx, "refs/tags/master", prevOID, git.ZeroOID))

	commitMessage := []byte("Update Submodule message")

	testCases := []struct {
		desc      string
		submodule string
		commitSha string
		branch    string
	}{
		{
			desc:      "Update submodule",
			submodule: "gitlab-grack",
			commitSha: "41fa1bc9e0f0630ced6a8a211d60c2af425ecc2d",
			branch:    "master",
		},
		{
			desc:      "Update submodule on weird branch",
			submodule: "gitlab-grack",
			commitSha: "41fa1bc9e0f0630ced6a8a211d60c2af425ecc2d",
			branch:    "refs/heads/master",
		},
		{
			desc:      "Update submodule inside folder",
			submodule: "test_inside_folder/another_folder/six",
			commitSha: "e25eda1fece24ac7a03624ed1320f82396f35bd8",
			branch:    "submodule_inside_folder",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			request := &gitalypb.UserUpdateSubmoduleRequest{
				Repository:    repoProto,
				User:          gittest.TestUser,
				Submodule:     []byte(testCase.submodule),
				CommitSha:     testCase.commitSha,
				Branch:        []byte(testCase.branch),
				CommitMessage: commitMessage,
			}

			response, err := client.UserUpdateSubmodule(ctx, request)
			require.NoError(t, err)
			require.Empty(t, response.GetCommitError())
			require.Empty(t, response.GetPreReceiveError())

			commit, err := repo.ReadCommit(ctx, git.Revision(response.BranchUpdate.CommitId))
			require.NoError(t, err)
			require.Equal(t, gittest.TestUser.Email, commit.Author.Email)
			require.Equal(t, gittest.TimezoneOffset, string(commit.Author.Timezone))
			require.Equal(t, gittest.TestUser.Email, commit.Committer.Email)
			require.Equal(t, commitMessage, commit.Subject)

			entry := gittest.Exec(t, cfg, "-C", repoPath, "ls-tree", "-z", fmt.Sprintf("%s^{tree}:", response.BranchUpdate.CommitId), testCase.submodule)
			parser := lstree.NewParser(bytes.NewReader(entry))
			parsedEntry, err := parser.NextEntry()
			require.NoError(t, err)
			require.Equal(t, testCase.submodule, parsedEntry.Path)
			require.Equal(t, testCase.commitSha, parsedEntry.ObjectID.String())
		})
	}
}

func TestUserUpdateSubmoduleStableID(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, repoProto, _, client := setupOperationsService(t, ctx)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	response, err := client.UserUpdateSubmodule(ctx, &gitalypb.UserUpdateSubmoduleRequest{
		Repository:    repoProto,
		User:          gittest.TestUser,
		Submodule:     []byte("gitlab-grack"),
		CommitSha:     "41fa1bc9e0f0630ced6a8a211d60c2af425ecc2d",
		Branch:        []byte("master"),
		CommitMessage: []byte("Update Submodule message"),
		Timestamp:     &timestamppb.Timestamp{Seconds: 12345},
	})
	require.NoError(t, err)
	require.Empty(t, response.GetCommitError())
	require.Empty(t, response.GetPreReceiveError())

	commit, err := repo.ReadCommit(ctx, git.Revision(response.BranchUpdate.CommitId))
	require.NoError(t, err)
	require.Equal(t, &gitalypb.GitCommit{
		Id: "928a79b1c5bbe64759f540aad8b339d281719118",
		ParentIds: []string{
			"1e292f8fedd741b75372e19097c76d327140c312",
		},
		TreeId:   "569d23230fd644aaeb2fcb239c52ef1fcaa171c3",
		Subject:  []byte("Update Submodule message"),
		Body:     []byte("Update Submodule message"),
		BodySize: 24,
		Author: &gitalypb.CommitAuthor{
			Name:     gittest.TestUser.Name,
			Email:    gittest.TestUser.Email,
			Date:     &timestamppb.Timestamp{Seconds: 12345},
			Timezone: []byte(gittest.TimezoneOffset),
		},
		Committer: &gitalypb.CommitAuthor{
			Name:     gittest.TestUser.Name,
			Email:    gittest.TestUser.Email,
			Date:     &timestamppb.Timestamp{Seconds: 12345},
			Timezone: []byte(gittest.TimezoneOffset),
		},
	}, commit)
}

func TestUserUpdateSubmoduleQuarantine(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	// Set up a hook that parses the new object and then aborts the update. Like this, we can
	// assert that the object does not end up in the main repository.
	outputPath := filepath.Join(testhelper.TempDir(t), "output")
	gittest.WriteCustomHook(t, repoPath, "pre-receive", []byte(fmt.Sprintf(
		`#!/bin/sh
		read oldval newval ref &&
		git rev-parse $newval^{commit} >%s &&
		exit 1
	`, outputPath)))

	response, err := client.UserUpdateSubmodule(ctx, &gitalypb.UserUpdateSubmoduleRequest{
		Repository:    repoProto,
		User:          gittest.TestUser,
		Submodule:     []byte("gitlab-grack"),
		CommitSha:     "41fa1bc9e0f0630ced6a8a211d60c2af425ecc2d",
		Branch:        []byte("master"),
		CommitMessage: []byte("Update Submodule message"),
		Timestamp:     &timestamppb.Timestamp{Seconds: 12345},
	})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.NotEmpty(t, response.GetPreReceiveError())

	hookOutput := testhelper.MustReadFile(t, outputPath)
	oid, err := git.NewObjectIDFromHex(text.ChompBytes(hookOutput))
	require.NoError(t, err)
	exists, err := repo.HasRevision(ctx, oid.Revision()+"^{commit}")
	require.NoError(t, err)

	require.False(t, exists, "quarantined commit should have been discarded")
}

func TestFailedUserUpdateSubmoduleRequestDueToValidations(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, _, repo, _, client := setupOperationsService(t, ctx)

	testCases := []struct {
		desc    string
		request *gitalypb.UserUpdateSubmoduleRequest
		code    codes.Code
	}{
		{
			desc: "empty Repository",
			request: &gitalypb.UserUpdateSubmoduleRequest{
				Repository:    nil,
				User:          gittest.TestUser,
				Submodule:     []byte("six"),
				CommitSha:     "db54006ff1c999fd485af44581dabe9b6c85a701",
				Branch:        []byte("some-branch"),
				CommitMessage: []byte("Update Submodule message"),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty User",
			request: &gitalypb.UserUpdateSubmoduleRequest{
				Repository:    repo,
				User:          nil,
				Submodule:     []byte("six"),
				CommitSha:     "db54006ff1c999fd485af44581dabe9b6c85a701",
				Branch:        []byte("some-branch"),
				CommitMessage: []byte("Update Submodule message"),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty Submodule",
			request: &gitalypb.UserUpdateSubmoduleRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Submodule:     nil,
				CommitSha:     "db54006ff1c999fd485af44581dabe9b6c85a701",
				Branch:        []byte("some-branch"),
				CommitMessage: []byte("Update Submodule message"),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty CommitSha",
			request: &gitalypb.UserUpdateSubmoduleRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Submodule:     []byte("six"),
				CommitSha:     "",
				Branch:        []byte("some-branch"),
				CommitMessage: []byte("Update Submodule message"),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "invalid CommitSha",
			request: &gitalypb.UserUpdateSubmoduleRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Submodule:     []byte("six"),
				CommitSha:     "foobar",
				Branch:        []byte("some-branch"),
				CommitMessage: []byte("Update Submodule message"),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "invalid CommitSha",
			request: &gitalypb.UserUpdateSubmoduleRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Submodule:     []byte("six"),
				CommitSha:     "db54006ff1c999fd485a",
				Branch:        []byte("some-branch"),
				CommitMessage: []byte("Update Submodule message"),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty Branch",
			request: &gitalypb.UserUpdateSubmoduleRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Submodule:     []byte("six"),
				CommitSha:     "db54006ff1c999fd485af44581dabe9b6c85a701",
				Branch:        nil,
				CommitMessage: []byte("Update Submodule message"),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty CommitMessage",
			request: &gitalypb.UserUpdateSubmoduleRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Submodule:     []byte("six"),
				CommitSha:     "db54006ff1c999fd485af44581dabe9b6c85a701",
				Branch:        []byte("some-branch"),
				CommitMessage: nil,
			},
			code: codes.InvalidArgument,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			_, err := client.UserUpdateSubmodule(ctx, testCase.request)
			testhelper.RequireGrpcCode(t, err, testCase.code)
			require.Contains(t, err.Error(), testCase.desc)
		})
	}
}

func TestFailedUserUpdateSubmoduleRequestDueToInvalidBranch(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, _, repo, _, client := setupOperationsService(t, ctx)

	request := &gitalypb.UserUpdateSubmoduleRequest{
		Repository:    repo,
		User:          gittest.TestUser,
		Submodule:     []byte("six"),
		CommitSha:     "db54006ff1c999fd485af44581dabe9b6c85a701",
		Branch:        []byte("non/existent"),
		CommitMessage: []byte("Update Submodule message"),
	}

	_, err := client.UserUpdateSubmodule(ctx, request)
	testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
	require.Contains(t, err.Error(), "Cannot find branch")
}

func TestFailedUserUpdateSubmoduleRequestDueToInvalidSubmodule(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, _, repo, _, client := setupOperationsService(t, ctx)

	request := &gitalypb.UserUpdateSubmoduleRequest{
		Repository:    repo,
		User:          gittest.TestUser,
		Submodule:     []byte("non-existent-submodule"),
		CommitSha:     "db54006ff1c999fd485af44581dabe9b6c85a701",
		Branch:        []byte("master"),
		CommitMessage: []byte("Update Submodule message"),
	}

	response, err := client.UserUpdateSubmodule(ctx, request)
	require.NoError(t, err)
	require.Equal(t, response.CommitError, "Invalid submodule path")
}

func TestFailedUserUpdateSubmoduleRequestDueToSameReference(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, _, repo, _, client := setupOperationsService(t, ctx)

	request := &gitalypb.UserUpdateSubmoduleRequest{
		Repository:    repo,
		User:          gittest.TestUser,
		Submodule:     []byte("six"),
		CommitSha:     "41fa1bc9e0f0630ced6a8a211d60c2af425ecc2d",
		Branch:        []byte("master"),
		CommitMessage: []byte("Update Submodule message"),
	}

	_, err := client.UserUpdateSubmodule(ctx, request)
	require.NoError(t, err)

	response, err := client.UserUpdateSubmodule(ctx, request)
	require.NoError(t, err)
	require.Contains(t, response.CommitError, "is already at")
}

func TestFailedUserUpdateSubmoduleRequestDueToRepositoryEmpty(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, _, _, client := setupOperationsService(t, ctx)

	repo, _ := gittest.InitRepo(t, cfg, cfg.Storages[0])

	request := &gitalypb.UserUpdateSubmoduleRequest{
		Repository:    repo,
		User:          gittest.TestUser,
		Submodule:     []byte("six"),
		CommitSha:     "41fa1bc9e0f0630ced6a8a211d60c2af425ecc2d",
		Branch:        []byte("master"),
		CommitMessage: []byte("Update Submodule message"),
	}

	response, err := client.UserUpdateSubmodule(ctx, request)
	require.NoError(t, err)
	require.Equal(t, response.CommitError, "Repository is empty")
}
