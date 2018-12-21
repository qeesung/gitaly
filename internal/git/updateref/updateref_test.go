package updateref

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"

	"gitlab.com/gitlab-org/gitaly/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func setup(t *testing.T) (context.Context, *gitalypb.Repository, string, func()) {
	ctx, cancel := testhelper.Context()
	testRepo, testRepoPath, cleanup := testhelper.NewTestRepo(t)
	teardown := func() {
		cancel()
		cleanup()
	}

	return ctx, testRepo, testRepoPath, teardown
}

func TestCreate(t *testing.T) {
	ctx, testRepo, _, teardown := setup(t)
	defer teardown()

	headCommit, err := log.GetCommit(ctx, testRepo, "HEAD")
	require.NoError(t, err)

	updater, err := New(ctx, testRepo)
	require.NoError(t, err)

	ref := "refs/heads/_create"
	sha := headCommit.Id

	require.NoError(t, updater.Create(ref, sha))
	require.NoError(t, updater.Wait())

	// check the ref was created
	commit, logErr := log.GetCommit(ctx, testRepo, ref)
	require.NoError(t, logErr)
	require.NotNil(t, commit, "reference was not created")
	require.Equal(t, commit.Id, sha, "reference was created with the wrong SHA")
}

func TestUpdate(t *testing.T) {
	ctx, testRepo, _, teardown := setup(t)
	defer teardown()

	headCommit, err := log.GetCommit(ctx, testRepo, "HEAD")
	require.NoError(t, err)

	updater, err := New(ctx, testRepo)
	require.NoError(t, err)

	ref := "refs/heads/feature"
	sha := headCommit.Id

	// Sanity check: ensure the ref exists before we start
	commit, logErr := log.GetCommit(ctx, testRepo, ref)
	require.NoError(t, logErr)
	require.NotNil(t, commit, "%s does not exist in the test repository", ref)
	require.NotEqual(t, commit.Id, sha, "%s points to HEAD: %s in the test repository", ref, sha)

	require.NoError(t, updater.Update(ref, sha, ""))
	require.NoError(t, updater.Wait())

	// check the ref was updated
	commit, logErr = log.GetCommit(ctx, testRepo, ref)
	require.NoError(t, logErr)
	require.NotNil(t, commit)
	require.Equal(t, commit.Id, sha, "reference was not updated")

	// try updating the ref with an old sha that doesn't exist
	parentCommit, err := log.GetCommit(ctx, testRepo, "HEAD^")
	require.NoError(t, err)
	require.Error(t, updater.Update(ref, parentCommit.Id, parentCommit.Id))

	// check the ref was not updated
	commit, logErr = log.GetCommit(ctx, testRepo, ref)
	require.NoError(t, logErr)
	require.NotNil(t, commit)
	require.NotEqual(t, commit.Id, parentCommit.Id, "reference was updated when it shouldn't have been")
}

func TestDelete(t *testing.T) {
	ctx, testRepo, _, teardown := setup(t)
	defer teardown()

	updater, err := New(ctx, testRepo)
	require.NoError(t, err)

	ref := "refs/heads/feature"

	require.NoError(t, updater.Delete(ref))
	require.NoError(t, updater.Wait())

	// check the ref was removed
	commit, logErr := log.GetCommit(ctx, testRepo, ref)
	require.NoError(t, logErr)
	require.Nil(t, commit, "reference was not removed")
}

func TestBulkOperation(t *testing.T) {
	ctx, testRepo, testRepoPath, teardown := setup(t)
	defer teardown()

	headCommit, err := log.GetCommit(ctx, testRepo, "HEAD")
	require.NoError(t, err)

	updater, err := New(ctx, testRepo)
	require.NoError(t, err)

	for i := 0; i < 1000; i++ {
		ref := fmt.Sprintf("refs/head/_test_%d", i)
		require.NoError(t, updater.Create(ref, headCommit.Id), "Failed to create ref %d", i)
	}

	require.NoError(t, updater.Wait())

	refs := testhelper.GetRepositoryRefs(t, testRepoPath)
	split := strings.Split(refs, "\n")
	require.True(t, len(split) > 1000, "At least 1000 refs should be present")
}

func TestContextCancelAbortsRefChanges(t *testing.T) {
	ctx, testRepo, _, teardown := setup(t)
	defer teardown()

	headCommit, err := log.GetCommit(ctx, testRepo, "HEAD")
	require.NoError(t, err)

	childCtx, childCancel := context.WithCancel(ctx)
	updater, err := New(childCtx, testRepo)
	require.NoError(t, err)

	ref := "refs/heads/_shouldnotexist"

	require.NoError(t, updater.Create(ref, headCommit.Id))

	// Force the update-ref process to terminate early
	childCancel()
	require.Error(t, updater.Wait())

	// check the ref doesn't exist
	commit, logErr := log.GetCommit(ctx, testRepo, ref)
	require.NoError(t, logErr)
	require.Nil(t, commit, "Reference was created even though the process was aborted")
}
