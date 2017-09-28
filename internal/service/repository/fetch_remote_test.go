package repository

import (
	"io/ioutil"
	"os"
	"path"
	"testing"

	"google.golang.org/grpc/codes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func copyRepoWithNewRemote(t *testing.T, repo *pb.Repository, remote string) *pb.Repository {
	repoPath, err := helper.GetRepoPath(repo)
	require.NoError(t, err)

	cloneRepo := &pb.Repository{StorageName: repo.GetStorageName(), RelativePath: "fetch-remote-clone.git"}

	clonePath := path.Join(testhelper.GitlabTestStoragePath(), "fetch-remote-clone.git")
	t.Logf("clonePath: %q", clonePath)
	os.RemoveAll(clonePath)

	testhelper.MustRunCommand(t, nil, "git", "clone", "--bare", repoPath, clonePath)

	testhelper.MustRunCommand(t, nil, "git", "-C", clonePath, "remote", "add", remote, repoPath)

	return cloneRepo
}

func TestFetchRemoteArgsBuilder(t *testing.T) {
	tests := []struct {
		desc string
		req  *pb.FetchRemoteRequest
		args []string
		envs []string
		code codes.Code
	}{
		{
			desc: "invalid storage",
			req:  &pb.FetchRemoteRequest{Repository: &pb.Repository{StorageName: "foobar"}},
			code: codes.NotFound,
		},
		{
			desc: "no params",
			req:  &pb.FetchRemoteRequest{Repository: testRepo, Remote: "upstream"},
			args: []string{"fetch-remote", testhelper.GitlabTestStoragePath(), testRepo.GetRelativePath(), "upstream"},
			code: codes.OK,
		},
		{
			desc: "force",
			req:  &pb.FetchRemoteRequest{Repository: testRepo, Remote: "upstream", Force: true},
			args: []string{"fetch-remote", testhelper.GitlabTestStoragePath(), testRepo.GetRelativePath(), "upstream", "--force"},
			code: codes.OK,
		},
		{
			desc: "no-tags",
			req:  &pb.FetchRemoteRequest{Repository: testRepo, Remote: "upstream", NoTags: true},
			args: []string{"fetch-remote", testhelper.GitlabTestStoragePath(), testRepo.GetRelativePath(), "upstream", "--no-tags"},
			code: codes.OK,
		},
		{
			desc: "timeout",
			req:  &pb.FetchRemoteRequest{Repository: testRepo, Remote: "upstream", Timeout: 1337},
			args: []string{"fetch-remote", testhelper.GitlabTestStoragePath(), testRepo.GetRelativePath(), "upstream", "1337"},
			code: codes.OK,
		},
		{
			desc: "force & no-tags",
			req:  &pb.FetchRemoteRequest{Repository: testRepo, Remote: "upstream", NoTags: true, Force: true},
			args: []string{"fetch-remote", testhelper.GitlabTestStoragePath(), testRepo.GetRelativePath(), "upstream", "--force", "--no-tags"},
			code: codes.OK,
		},
		{
			desc: "timeout, force & no-tags",
			req:  &pb.FetchRemoteRequest{Repository: testRepo, Remote: "upstream", NoTags: true, Force: true, Timeout: 1337},
			args: []string{"fetch-remote", testhelper.GitlabTestStoragePath(), testRepo.GetRelativePath(), "upstream", "1337", "--force", "--no-tags"},
			code: codes.OK,
		},
		{
			desc: "ssh-keys",
			req:  &pb.FetchRemoteRequest{Repository: testRepo, Remote: "upstream", SshKey: "foo", KnownHosts: "bar"},
			args: []string{"fetch-remote", testhelper.GitlabTestStoragePath(), testRepo.GetRelativePath(), "upstream"},
			envs: []string{"GITLAB_SHELL_SSH_KEY=foo", "GITLAB_SHELL_KNOWN_HOSTS=bar"},
			code: codes.OK,
		},
	}

	for _, tc := range tests {
		t.Logf("testing %q", tc.desc)
		args, envs, err := fetchRemoteArgBuilder(tc.req)
		if tc.code == codes.OK {
			assert.NoError(t, err)
		} else {
			testhelper.AssertGrpcError(t, err, tc.code, "")
		}
		assert.EqualValues(t, tc.args, args)
		assert.EqualValues(t, tc.envs, envs)
	}
}

// NOTE: Only tests that `gitlab-shell` is being called, not what it does.
func TestFetchRemoteSuccess(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	dir, err := ioutil.TempDir("", "gitlab-shell.")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(path.Join(dir, "bin"), 0755))
	defer func(dir string) {
		os.RemoveAll(dir)
	}(dir)
	testhelper.MustRunCommand(t, nil, "go", "build", "-o", path.Join(dir, "bin", "gitlab-projects"), "gitlab.com/gitlab-org/gitaly/internal/testhelper/gitlab-projects")

	// We need to know about gitlab-shell...
	defer func(oldPath string) {
		config.Config.GitlabShell.Dir = oldPath
	}(config.Config.GitlabShell.Dir)
	config.Config.GitlabShell.Dir = dir

	server := runRepoServer(t)
	defer server.Stop()

	client, _ := newRepositoryClient(t)

	cloneRepo := copyRepoWithNewRemote(t, testRepo, "my-remote")
	defer func(r *pb.Repository) {
		path, err := helper.GetRepoPath(r)
		if err != nil {
			panic(err)
		}
		os.RemoveAll(path)
	}(cloneRepo)

	resp, err := client.FetchRemote(ctx, &pb.FetchRemoteRequest{
		Repository: cloneRepo,
		Remote:     "my-remote",
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestFetchRemoteFailure(t *testing.T) {
	server := runRepoServer(t)
	defer server.Stop()

	client, _ := newRepositoryClient(t)

	tests := []struct {
		desc string
		req  *pb.FetchRemoteRequest
		code codes.Code
		err  string
	}{
		{
			desc: "invalid storage",
			req:  &pb.FetchRemoteRequest{Repository: &pb.Repository{StorageName: "invalid", RelativePath: "foobar.git"}},
			code: codes.NotFound,
			err:  "Storage not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, cancel := testhelper.Context()
			defer cancel()

			resp, err := client.FetchRemote(ctx, tc.req)
			testhelper.AssertGrpcError(t, err, tc.code, tc.err)
			assert.Error(t, err)
			assert.Nil(t, resp)
		})
	}
}
