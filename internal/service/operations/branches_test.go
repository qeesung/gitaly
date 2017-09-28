package operations

import (
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"golang.org/x/net/context"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

func TestSuccessfulUserCreateBranchRequest(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	server := runOperationServiceServer(t)
	defer server.Stop()

	client, conn := newOperationClient(t)
	defer conn.Close()

	startPoint := "c7fbe50c7c7419d9701eebe64b1fdacc3df5b9dd"
	startPointCommit, err := log.GetCommit(ctx, testRepo, startPoint, "")
	require.NoError(t, err)
	user := &pb.User{
		Name:  []byte("Alejandro Rodríguez"),
		Email: []byte("alejandro@gitlab.com"),
		GlId:  "user-1",
	}

	testCases := []struct {
		desc           string
		branchName     string
		startPoint     string
		expectedBranch *pb.Branch
	}{
		{
			desc:       "valid branch",
			branchName: "new-branch",
			startPoint: startPoint,
			expectedBranch: &pb.Branch{
				Name:         []byte("new-branch"),
				TargetCommit: startPointCommit,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			branchName := testCase.branchName
			request := &pb.UserCreateBranchRequest{
				Repository: testRepo,
				BranchName: []byte(branchName),
				StartPoint: []byte(testCase.startPoint),
				User:       user,
			}

			ctx, cancel := testhelper.Context()
			defer cancel()

			response, err := client.UserCreateBranch(ctx, request)
			if testCase.expectedBranch != nil {
				defer exec.Command("git", "-C", testRepoPath, "branch", "-D", branchName).Run()
			}

			require.NoError(t, err)
			require.Equal(t, testCase.expectedBranch, response.Branch)
			require.Empty(t, response.PreReceiveError)

			branches := testhelper.MustRunCommand(t, nil, "git", "-C", testRepoPath, "branch")
			require.Contains(t, string(branches), branchName)
		})
	}
}

func TestSuccessfulGitHooksForUserCreateBranchRequest(t *testing.T) {
	server := runOperationServiceServer(t)
	defer server.Stop()

	client, conn := newOperationClient(t)
	defer conn.Close()

	branchName := "new-branch"
	user := &pb.User{
		Name:  []byte("Alejandro Rodríguez"),
		Email: []byte("alejandro@gitlab.com"),
		GlId:  "user-1",
	}
	request := &pb.UserCreateBranchRequest{
		Repository: testRepo,
		BranchName: []byte(branchName),
		StartPoint: []byte("c7fbe50c7c7419d9701eebe64b1fdacc3df5b9dd"),
		User:       user,
	}

	for _, hookName := range []string{"pre-receive", "update", "post-receive"} {
		t.Run(hookName, func(t *testing.T) {
			defer exec.Command("git", "-C", testRepoPath, "branch", "-D", branchName).Run()

			hookPath, hookOutputTempPath := writeEnvToHook(t, hookName)
			defer os.Remove(hookPath)

			ctx, cancel := testhelper.Context()
			defer cancel()

			response, err := client.UserCreateBranch(ctx, request)
			require.NoError(t, err)
			require.Empty(t, response.PreReceiveError)

			output := string(testhelper.MustReadFile(t, hookOutputTempPath))
			require.Contains(t, output, "GL_ID="+user.GlId)
		})
	}
}

func TestFailedUserCreateBranchDueToHooks(t *testing.T) {
	server := runOperationServiceServer(t)
	defer server.Stop()

	client, conn := newOperationClient(t)
	defer conn.Close()

	user := &pb.User{
		Name:  []byte("Alejandro Rodríguez"),
		Email: []byte("alejandro@gitlab.com"),
		GlId:  "user-1",
	}
	request := &pb.UserCreateBranchRequest{
		Repository: testRepo,
		BranchName: []byte("new-branch"),
		StartPoint: []byte("c7fbe50c7c7419d9701eebe64b1fdacc3df5b9dd"),
		User:       user,
	}
	// Write a hook that will fail with the environment as the error message
	// so we can check that string for our env variables.
	hookContent := []byte("#!/bin/sh\nprintenv | paste -sd ' ' -\nexit 1")

	for _, hookName := range []string{"pre-receive", "update"} {
		hookPath := path.Join(testRepoPath, "hooks", hookName)
		ioutil.WriteFile(hookPath, hookContent, 0755)
		defer os.Remove(hookPath)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		response, err := client.UserCreateBranch(ctx, request)
		require.Nil(t, err)
		require.Contains(t, response.PreReceiveError, "GL_ID="+user.GlId)
		require.Contains(t, response.PreReceiveError, "GL_REPOSITORY="+testRepo.GlRepository)
		require.Contains(t, response.PreReceiveError, "GL_PROTOCOL=web")
		require.Contains(t, response.PreReceiveError, "PWD="+testRepoPath)
	}
}

func TestFailedUserCreateBranchRequest(t *testing.T) {
	server := runOperationServiceServer(t)
	defer server.Stop()

	client, conn := newOperationClient(t)
	defer conn.Close()

	user := &pb.User{
		Name:  []byte("Alejandro Rodríguez"),
		Email: []byte("alejandro@gitlab.com"),
	}
	testCases := []struct {
		desc       string
		branchName string
		startPoint string
		user       *pb.User
		code       codes.Code
	}{
		{
			desc:       "empty start_point",
			branchName: "shiny-new-branch",
			startPoint: "",
			user:       user,
			code:       codes.InvalidArgument,
		},
		{
			desc:       "empty user",
			branchName: "shiny-new-branch",
			startPoint: "master",
			user:       nil,
			code:       codes.InvalidArgument,
		},
		{
			desc:       "non-existing starting point",
			branchName: "new-branch",
			startPoint: "i-dont-exist",
			user:       user,
			code:       codes.FailedPrecondition,
		},

		{
			desc:       "branch exists",
			branchName: "master",
			startPoint: "master",
			user:       user,
			code:       codes.FailedPrecondition,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			request := &pb.UserCreateBranchRequest{
				Repository: testRepo,
				BranchName: []byte(testCase.branchName),
				StartPoint: []byte(testCase.startPoint),
				User:       testCase.user,
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			_, err := client.UserCreateBranch(ctx, request)
			testhelper.AssertGrpcError(t, err, testCase.code, "")
		})
	}
}
