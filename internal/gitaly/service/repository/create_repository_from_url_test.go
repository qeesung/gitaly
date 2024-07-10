package repository

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

func TestCreateRepositoryFromURL_successful(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, client := setupRepositoryService(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)

	_, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	importedRepo := &gitalypb.Repository{
		RelativePath: "imports/test-repo-imported.git",
		StorageName:  cfg.Storages[0].Name,
	}

	user := "username123"
	password := "password321localhost"
	port := gitServerWithBasicAuth(t, ctx, gitCmdFactory, user, password, repoPath)

	url := fmt.Sprintf("http://%s:%s@localhost:%d/%s", user, password, port, filepath.Base(repoPath))

	req := &gitalypb.CreateRepositoryFromURLRequest{
		Repository: importedRepo,
		Url:        url,
	}

	_, err := client.CreateRepositoryFromURL(ctx, req)
	require.NoError(t, err)

	importedRepoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, importedRepo))

	gittest.Exec(t, cfg, "-C", importedRepoPath, "fsck")

	remotes := gittest.Exec(t, cfg, "-C", importedRepoPath, "remote")
	require.NotContains(t, string(remotes), "origin")

	_, err = os.Lstat(filepath.Join(importedRepoPath, "hooks"))
	require.True(t, os.IsNotExist(err), "hooks directory should not have been created")
}

func TestCreateRepositoryFromURL_successfulWithOptionalParameters(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)

	_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch(git.DefaultBranch))
	gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithReference("refs/merge-requests/1/head"))

	user := "username123"
	password := "password321localhost"
	port := gitServerWithBasicAuth(t, ctx, gitCmdFactory, user, password, remoteRepoPath)

	importedRepo := &gitalypb.Repository{
		RelativePath: "imports/test-repo-imported-mirror.git",
		StorageName:  cfg.Storages[0].Name,
	}

	_, err := client.CreateRepositoryFromURL(ctx, &gitalypb.CreateRepositoryFromURLRequest{
		Repository:              importedRepo,
		Url:                     fmt.Sprintf("http://%s:%s@localhost:%d/%s", user, password, port, filepath.Base(remoteRepoPath)),
		HttpAuthorizationHeader: "GL-Geo EhEhKSUk_385GSLnS7BI:eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJkYXRhIjoie1wic2NvcGVcIjpcInJvb3QvZ2l0bGFiLWNlXCJ9IiwianRpIjoiNmQ4ZDM1NGQtZjUxYS00MDQ5LWExZjctMjUyMjk4YmQwMTI4IiwiaWF0IjoxNjQyMDk1MzY5LCJuYmYiOjE2NDIwOTUzNjQsImV4cCI6MTY0MjA5NTk2OX0.YEpfzg8305dUqkYOiB7_dhbL0FVSaUPgpSpMuKrgNrg",
		Mirror:                  true,
	})
	require.NoError(t, err)

	importedRepoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, importedRepo))
	gittest.Exec(t, cfg, "-C", importedRepoPath, "fsck")
	require.Empty(t, gittest.Exec(t, cfg, "-C", importedRepoPath, "remote"))
	require.Equal(t,
		[]string{"refs/heads/" + git.DefaultBranch, "refs/merge-requests/1/head"},
		strings.Split(text.ChompBytes(gittest.Exec(t, cfg, "-C", importedRepoPath, "for-each-ref", "--format=%(refname)")), "\n"),
	)
	require.NoDirExists(t, filepath.Join(importedRepoPath, "hooks"))
}

func TestCreateRepositoryFromURL_existingTarget(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	testCases := []struct {
		desc        string
		repoPath    string
		isDir       bool
		skipWithWAL string
	}{
		{
			desc:  "target is a directory",
			isDir: true,
		},
		{
			desc:  "target is a file",
			isDir: false,
			skipWithWAL: `
The transaction commit fails as the TransactionManager fails to initialize with the state
directory being a file. This is testing storage details rather than the RPC implementation
testing of this scenario should be left to the relevant package.
`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			testhelper.SkipWithWAL(t, testCase.skipWithWAL)

			cfg, client := setupRepositoryService(t)

			importedRepo := &gitalypb.Repository{
				RelativePath: storage.DeriveReplicaPath(1),
				StorageName:  cfg.Storages[0].Name,
			}
			importedRepoPath := filepath.Join(cfg.Storages[0].Path, importedRepo.GetRelativePath())

			if testCase.isDir {
				require.NoError(t, os.MkdirAll(importedRepoPath, perm.PrivateDir))
			} else {
				require.NoError(t, os.MkdirAll(filepath.Dir(importedRepoPath), perm.PrivateDir))
				require.NoError(t, os.WriteFile(importedRepoPath, nil, perm.PrivateWriteOnceFile))
			}
			t.Cleanup(func() { require.NoError(t, os.RemoveAll(importedRepoPath)) })

			req := &gitalypb.CreateRepositoryFromURLRequest{
				Repository: importedRepo,
				Url:        "https://gitlab.com/gitlab-org/gitlab-test.git",
			}

			_, err := client.CreateRepositoryFromURL(ctx, req)
			testhelper.RequireGrpcCode(t, err, codes.AlreadyExists)
		})
	}
}

func TestCreateRepositoryFromURL_redirect(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, client := setupRepositoryService(t)

	importedRepo := &gitalypb.Repository{
		RelativePath: "imports/test-repo-imported.git",
		StorageName:  cfg.Storages[0].Name,
	}

	httpServerState, redirectingServer := StartRedirectingTestServer()
	defer redirectingServer.Close()

	req := &gitalypb.CreateRepositoryFromURLRequest{
		Repository: importedRepo,
		Url:        redirectingServer.URL,
	}

	_, err := client.CreateRepositoryFromURL(ctx, req)

	require.True(t, httpServerState.serverVisited, "git command should make the initial HTTP request")
	require.False(t, httpServerState.serverVisitedAfterRedirect, "git command should not follow HTTP redirection")

	testhelper.RequireStatusWithErrorMetadataRegexp(t,
		structerr.NewInternal("creating repository: cloning repository: exit status 128"),
		err,
		map[string]string{
			"stderr":           "The requested URL returned error: 301",
			"resolved_address": "",
		},
	)
}

func TestCreateRepositoryFromURL_fsck(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, client := setupRepositoryService(t)

	_, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)

	// We're creating a new commit which has a root tree with duplicate entries. git-mktree(1)
	// allows us to create these trees just fine, but git-fsck(1) complains.
	gittest.WriteCommit(t, cfg, sourceRepoPath,
		gittest.WithParents(),
		gittest.WithBranch("main"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Content: "content", Path: "dup", Mode: "100644"},
			gittest.TreeEntry{Content: "content", Path: "dup", Mode: "100644"},
		),
	)

	targetRepoProto := &gitalypb.Repository{
		RelativePath: gittest.NewRepositoryName(t),
		StorageName:  cfg.Storages[0].Name,
	}

	_, err := client.CreateRepositoryFromURL(ctx, &gitalypb.CreateRepositoryFromURLRequest{
		Repository: targetRepoProto,
		Url:        "file://" + sourceRepoPath,
	})
	testhelper.RequireStatusWithErrorMetadataRegexp(t,
		structerr.NewInternal("creating repository: cloning repository: exit status 128"),
		err,
		map[string]string{
			"stderr":           "duplicateEntries: contains duplicate file entries",
			"resolved_address": "",
		},
	)
}

func TestServer_CloneFromURLCommand(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	s := server{cfg: cfg, gitCmdFactory: gittest.NewCommandFactory(t, cfg)}

	user, password := "example_user", "pass%21%3F%40"

	for _, tc := range []struct {
		desc                         string
		url                          string
		resolvedAddress              string
		token                        string
		expectedAuthHeader           string
		expectedCurloptResolveHeader string
		expectedURL                  string
	}{
		{
			desc:  "user credentials",
			url:   fmt.Sprintf("https://%s:%s@192.0.2.1/secretrepo.git", user, password),
			token: "",
			expectedAuthHeader: fmt.Sprintf(
				"Authorization: Basic %s",
				base64.StdEncoding.EncodeToString([]byte("example_user:pass!?@")),
			),
			expectedURL: "https://192.0.2.1/secretrepo.git",
		},
		{
			desc:  "token",
			url:   "https://192.0.2.1/secretrepo.git",
			token: "some-token",
			expectedAuthHeader: fmt.Sprintf(
				"Authorization: %s", "some-token",
			),
			expectedURL: "https://192.0.2.1/secretrepo.git",
		},
		{
			desc:                         "with resolved address",
			url:                          "https://gitlab.com/secretrepo.git",
			token:                        "some-token",
			expectedAuthHeader:           fmt.Sprintf("Authorization: %s", "some-token"),
			resolvedAddress:              "192.0.1.1",
			expectedURL:                  "https://gitlab.com/secretrepo.git",
			expectedCurloptResolveHeader: "gitlab.com:443:192.0.1.1",
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(testhelper.Context(t))

			cmd, err := s.cloneFromURLCommand(
				ctx,
				tc.url,
				tc.resolvedAddress,
				filepath.Join(testhelper.TempDir(t), "target"),
				tc.token,
				false,
				git.WithDisabledHooks(),
			)
			require.NoError(t, err)

			// Kill the command so that it won't leak outside of the current test
			// context. We know that it will return an error, but we cannot quite tell
			// what kind of error it will be because it might fail either be to the kill
			// signal or because it failed to clone the repository.
			cancel()
			require.Error(t, cmd.Wait())

			args := cmd.Args()
			require.Contains(t, args, "--bare")
			require.Contains(t, args, tc.expectedURL)
			for _, arg := range args {
				require.NotContains(t, arg, user)
				require.NotContains(t, arg, password)
				require.NotContains(t, arg, tc.expectedAuthHeader)
			}

			require.Subset(t, cmd.Env(), []string{
				"GIT_CONFIG_KEY_0=http.extraHeader",
				"GIT_CONFIG_VALUE_0=" + tc.expectedAuthHeader,
			})

			if tc.expectedCurloptResolveHeader != "" {
				require.Subset(t, cmd.Env(), []string{
					"GIT_CONFIG_KEY_1=http.curloptResolve",
					"GIT_CONFIG_VALUE_1=" + tc.expectedCurloptResolveHeader,
				})
			}
		})
	}
}

func TestServer_CloneFromURLCommand_withMirror(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	repositoryFullPath := filepath.Join(testhelper.TempDir(t), "full/path/to/repository")
	url := "https://www.example.com/secretrepo.git"

	cfg := testcfg.Build(t)
	s := server{cfg: cfg, gitCmdFactory: gittest.NewCommandFactory(t, cfg)}
	cmd, err := s.cloneFromURLCommand(ctx, url, "", repositoryFullPath, "", true, git.WithDisabledHooks())
	require.NoError(t, err)

	args := cmd.Args()
	require.Contains(t, args, "--mirror")
	require.NotContains(t, args, "--bare")
	require.Error(t, cmd.Wait())
}

func TestServer_CloneFromURLCommand_validate(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	testCases := []struct {
		desc        string
		req         *gitalypb.CreateRepositoryFromURLRequest
		expectedErr error
	}{
		{
			desc:        "no repository provided",
			req:         &gitalypb.CreateRepositoryFromURLRequest{Repository: nil},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc:        "no URL provided",
			req:         &gitalypb.CreateRepositoryFromURLRequest{Repository: &gitalypb.Repository{StorageName: cfg.Storages[0].Name, RelativePath: "new"}, Url: ""},
			expectedErr: structerr.NewInvalidArgument("empty Url"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.CreateRepositoryFromURL(ctx, tc.req)
			require.Error(t, err)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func gitServerWithBasicAuth(tb testing.TB, ctx context.Context, gitCmdFactory git.CommandFactory, user, pass, repoPath string) int {
	return gittest.HTTPServer(tb, ctx, gitCmdFactory, repoPath, basicAuthMiddleware(tb, user, pass))
}

func basicAuthMiddleware(tb testing.TB, user, pass string) func(http.ResponseWriter, *http.Request, http.Handler) {
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		authUser, authPass, ok := r.BasicAuth()
		require.True(tb, ok, "should contain basic auth")
		require.Equal(tb, user, authUser, "username should match")
		require.Equal(tb, pass, authPass, "password should match")
		next.ServeHTTP(w, r)
	}
}
