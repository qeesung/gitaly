//go:build !gitaly_test_sha256

package diff

import (
	"io"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"google.golang.org/grpc/codes"
)

func TestRawDiff_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repoProto, repoPath, client := setupDiffService(ctx, t)
	testcfg.BuildGitalyGit2Go(t, cfg)

	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	locator := config.NewLocator(cfg)
	git2goExecutor := git2go.NewExecutor(cfg, gitCmdFactory, locator)

	leftCommit := "57290e673a4c87f51294f5216672cbc58d485d25"
	rightCommit := "e395f646b1499e8e0279445fc99a0596a65fab7e"

	stream, err := client.RawDiff(ctx, &gitalypb.RawDiffRequest{
		Repository:    repoProto,
		LeftCommitId:  leftCommit,
		RightCommitId: rightCommit,
	})
	require.NoError(t, err)

	rawDiff, err := io.ReadAll(streamio.NewReader(func() ([]byte, error) {
		response, err := stream.Recv()
		return response.GetData(), err
	}))
	require.NoError(t, err)

	signature := git2go.Signature{
		Name:  "Scrooge McDuck",
		Email: "scrooge@mcduck.com",
		When:  time.Unix(12345, 0),
	}

	// Now that we have read the patch in we verify that it indeed round-trips to the same tree
	// as the right commit is referring to by reapplying the diff on top of the left commit.
	patchedCommitID, err := git2goExecutor.Apply(ctx, gittest.RewrittenRepository(ctx, t, cfg, repoProto), git2go.ApplyParams{
		Repository:   repoPath,
		Committer:    signature,
		ParentCommit: leftCommit,
		Patches: git2go.NewSlicePatchIterator([]git2go.Patch{{
			Author:  signature,
			Message: "Applying received raw diff",
			Diff:    rawDiff,
		}}),
	})
	require.NoError(t, err)

	// Peel both right commit and patched commit to their trees and assert that they refer to
	// the same one.
	require.Equal(t,
		gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", rightCommit+"^{tree}"),
		gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", patchedCommitID.String()+"^{tree}"),
	)
}

func TestRawDiff_inputValidation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupDiffService(ctx, t)

	testCases := []struct {
		desc    string
		request *gitalypb.RawDiffRequest
		code    codes.Code
	}{
		{
			desc: "empty left commit",
			request: &gitalypb.RawDiffRequest{
				Repository:    repo,
				LeftCommitId:  "",
				RightCommitId: "e395f646b1499e8e0279445fc99a0596a65fab7e",
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty right commit",
			request: &gitalypb.RawDiffRequest{
				Repository:    repo,
				RightCommitId: "",
				LeftCommitId:  "e395f646b1499e8e0279445fc99a0596a65fab7e",
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty repo",
			request: &gitalypb.RawDiffRequest{
				Repository:    nil,
				RightCommitId: "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab",
				LeftCommitId:  "e395f646b1499e8e0279445fc99a0596a65fab7e",
			},
			code: codes.InvalidArgument,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			c, _ := client.RawDiff(ctx, testCase.request)
			testhelper.RequireGrpcCode(t, drainRawDiffResponse(c), testCase.code)
		})
	}
}

func TestRawPatch_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repoProto, repoPath, client := setupDiffService(ctx, t)
	testcfg.BuildGitalyGit2Go(t, cfg)

	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	locator := config.NewLocator(cfg)
	git2goExecutor := git2go.NewExecutor(cfg, gitCmdFactory, locator)

	rightCommit := "e395f646b1499e8e0279445fc99a0596a65fab7e"
	leftCommit := "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"

	stream, err := client.RawPatch(ctx, &gitalypb.RawPatchRequest{
		Repository:    repoProto,
		LeftCommitId:  leftCommit,
		RightCommitId: rightCommit,
	})
	require.NoError(t, err)

	rawPatch, err := io.ReadAll(streamio.NewReader(func() ([]byte, error) {
		response, err := stream.Recv()
		return response.GetData(), err
	}))
	require.NoError(t, err)

	signature := git2go.Signature{
		Name:  "Scrooge McDuck",
		Email: "scrooge@mcduck.com",
		When:  time.Unix(12345, 0),
	}

	// Now that we have read the patch in we verify that it indeed round-trips to the same tree
	// as the right commit is referring to by reapplying the diff on top of the left commit.
	patchedCommitID, err := git2goExecutor.Apply(ctx, gittest.RewrittenRepository(ctx, t, cfg, repoProto), git2go.ApplyParams{
		Repository:   repoPath,
		Committer:    signature,
		ParentCommit: leftCommit,
		Patches: git2go.NewSlicePatchIterator([]git2go.Patch{{
			Author:  signature,
			Message: "Applying received raw patch",
			Diff:    rawPatch,
		}}),
	})
	require.NoError(t, err)

	// Peel both right commit and patched commit to their trees and assert that they refer to
	// the same one.
	require.Equal(t,
		gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", rightCommit+"^{tree}"),
		gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", patchedCommitID.String()+"^{tree}"),
	)
}

func TestRawPatch_inputValidation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupDiffService(ctx, t)

	testCases := []struct {
		desc    string
		request *gitalypb.RawPatchRequest
		code    codes.Code
	}{
		{
			desc: "empty left commit",
			request: &gitalypb.RawPatchRequest{
				Repository:    repo,
				LeftCommitId:  "",
				RightCommitId: "e395f646b1499e8e0279445fc99a0596a65fab7e",
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty right commit",
			request: &gitalypb.RawPatchRequest{
				Repository:    repo,
				RightCommitId: "",
				LeftCommitId:  "e395f646b1499e8e0279445fc99a0596a65fab7e",
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty repo",
			request: &gitalypb.RawPatchRequest{
				Repository:    nil,
				RightCommitId: "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab",
				LeftCommitId:  "e395f646b1499e8e0279445fc99a0596a65fab7e",
			},
			code: codes.InvalidArgument,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			c, _ := client.RawPatch(ctx, testCase.request)
			testhelper.RequireGrpcCode(t, drainRawPatchResponse(c), testCase.code)
		})
	}
}

func TestRawPatch_gitlabSignature(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupDiffService(ctx, t)

	rightCommit := "e395f646b1499e8e0279445fc99a0596a65fab7e"
	leftCommit := "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"
	rpcRequest := &gitalypb.RawPatchRequest{Repository: repo, RightCommitId: rightCommit, LeftCommitId: leftCommit}

	c, err := client.RawPatch(ctx, rpcRequest)
	require.NoError(t, err)

	reader := streamio.NewReader(func() ([]byte, error) {
		response, err := c.Recv()
		return response.GetData(), err
	})

	patch, err := io.ReadAll(reader)
	require.NoError(t, err)

	require.Regexp(t, regexp.MustCompile(`\n-- \nGitLab\s+$`), string(patch))
}

func drainRawDiffResponse(c gitalypb.DiffService_RawDiffClient) error {
	var err error
	for err == nil {
		_, err = c.Recv()
	}
	return err
}

func drainRawPatchResponse(c gitalypb.DiffService_RawPatchClient) error {
	var err error
	for err == nil {
		_, err = c.Recv()
	}
	return err
}
