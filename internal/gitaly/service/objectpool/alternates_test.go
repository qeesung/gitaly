package objectpool

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestDisconnectGitAlternates(t *testing.T) {
	locator := config.NewLocator(config.Config)
	server, serverSocketPath := runObjectPoolServer(t, config.Config, locator)
	defer server.Stop()

	client, conn := newObjectPoolClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	gitCmdFactory := git.NewExecCommandFactory(config.Config)
	pool, err := objectpool.NewObjectPool(config.Config, locator, gitCmdFactory, testRepo.GetStorageName(), testhelper.NewTestObjectPoolName(t))
	require.NoError(t, err)
	defer pool.Remove(ctx)

	require.NoError(t, pool.Create(ctx, testRepo))
	require.NoError(t, pool.Link(ctx, testRepo))
	testhelper.MustRunCommand(t, nil, "git", "-C", testRepoPath, "gc")

	existingObjectID := "55bc176024cfa3baaceb71db584c7e5df900ea65"

	// Corrupt the repository to check that existingObjectID can no longer be found
	altPath, err := locator.InfoAlternatesPath(testRepo)
	require.NoError(t, err, "find info/alternates")
	require.NoError(t, os.RemoveAll(altPath))

	cmd, err := gitCmdFactory.New(ctx, testRepo, nil,
		git.SubCmd{Name: "cat-file", Flags: []git.Option{git.Flag{Name: "-e"}}, Args: []string{existingObjectID}})
	require.NoError(t, err)
	require.Error(t, cmd.Wait(), "expect cat-file to fail because object cannot be found")

	require.NoError(t, pool.Link(ctx, testRepo))
	require.FileExists(t, altPath, "objects/info/alternates should be back")

	// At this point we know that the repository has access to
	// existingObjectID, but only if objects/info/alternates is in place.

	_, err = client.DisconnectGitAlternates(ctx, &gitalypb.DisconnectGitAlternatesRequest{Repository: testRepo})
	require.NoError(t, err, "call DisconnectGitAlternates")

	// Check that the object can still be found, even though
	// objects/info/alternates is gone. This is the purpose of
	// DisconnectGitAlternates.
	testhelper.AssertPathNotExists(t, altPath)
	testhelper.MustRunCommand(t, nil, "git", "-C", testRepoPath, "cat-file", "-e", existingObjectID)
}

func TestDisconnectGitAlternatesNoAlternates(t *testing.T) {
	locator := config.NewLocator(config.Config)
	server, serverSocketPath := runObjectPoolServer(t, config.Config, locator)
	defer server.Stop()

	client, conn := newObjectPoolClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	altPath, err := locator.InfoAlternatesPath(testRepo)
	require.NoError(t, err, "find info/alternates")
	testhelper.AssertPathNotExists(t, altPath)

	_, err = client.DisconnectGitAlternates(ctx, &gitalypb.DisconnectGitAlternatesRequest{Repository: testRepo})
	require.NoError(t, err, "call DisconnectGitAlternates on repository without alternates")

	testhelper.MustRunCommand(t, nil, "git", "-C", testRepoPath, "fsck")
}

func TestDisconnectGitAlternatesUnexpectedAlternates(t *testing.T) {
	locator := config.NewLocator(config.Config)
	server, serverSocketPath := runObjectPoolServer(t, config.Config, locator)
	defer server.Stop()

	client, conn := newObjectPoolClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	testCases := []struct {
		desc       string
		altContent string
	}{
		{desc: "multiple alternates", altContent: "/foo/bar\n/qux/baz\n"},
		{desc: "directory not found", altContent: "/does/not/exist/\n"},
		{desc: "not a directory", altContent: "../HEAD\n"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
			defer cleanupFn()

			altPath, err := locator.InfoAlternatesPath(testRepo)
			require.NoError(t, err, "find info/alternates")

			require.NoError(t, ioutil.WriteFile(altPath, []byte(tc.altContent), 0644))

			_, err = client.DisconnectGitAlternates(ctx, &gitalypb.DisconnectGitAlternatesRequest{Repository: testRepo})
			require.Error(t, err, "call DisconnectGitAlternates on repository with unexpected objects/info/alternates")

			contentAfterRPC, err := ioutil.ReadFile(altPath)
			require.NoError(t, err, "read back objects/info/alternates")
			require.Equal(t, tc.altContent, string(contentAfterRPC), "objects/info/alternates content should not have changed")
		})
	}
}

func TestRemoveAlternatesIfOk(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	locator := config.NewLocator(config.Config)
	altPath, err := locator.InfoAlternatesPath(testRepo)
	require.NoError(t, err, "find info/alternates")
	altContent := "/var/empty\n"
	require.NoError(t, ioutil.WriteFile(altPath, []byte(altContent), 0644), "write alternates file")

	// Intentionally break the repository, so that 'git fsck' will fail later.
	testhelper.MustRunCommand(t, nil, "sh", "-c", fmt.Sprintf("rm %s/objects/pack/*.pack", testRepoPath))

	altBackup := altPath + ".backup"

	srv := server{gitCmdFactory: git.NewExecCommandFactory(config.Config)}
	err = srv.removeAlternatesIfOk(ctx, testRepo, altPath, altBackup)
	require.Error(t, err, "removeAlternatesIfOk should fail")
	require.IsType(t, &fsckError{}, err, "error must be because of fsck")

	// We expect objects/info/alternates to have been restored when
	// removeAlternatesIfOk returned.
	assertAlternates(t, altPath, altContent)

	// We expect the backup alternates file to still exist.
	assertAlternates(t, altBackup, altContent)
}

func assertAlternates(t *testing.T, altPath string, altContent string) {
	actualContent, err := ioutil.ReadFile(altPath)
	require.NoError(t, err, "read %s after fsck failure", altPath)

	require.Equal(t, altContent, string(actualContent), "%s content after fsck failure", altPath)
}
