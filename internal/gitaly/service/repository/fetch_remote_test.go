//go:build !gitaly_test_sha256

package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFetchRemote(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	// Some of the tests require multiple calls to the clients each run struct
	// encompasses the expected data for a single run
	type run struct {
		expectedRefs     map[string]git.ObjectID
		expectedResponse *gitalypb.FetchRemoteResponse
		expectedErr      error
	}

	type setupData struct {
		repoPath string
		request  *gitalypb.FetchRemoteRequest
		runs     []run
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T, cfg config.Cfg) setupData
	}{
		{
			desc: "check tags without tags",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						CheckTagsChanged: true,
					},
					runs: []run{
						{
							expectedRefs:     map[string]git.ObjectID{"refs/heads/main": commitID},
							expectedResponse: &gitalypb.FetchRemoteResponse{},
						},
					},
				}
			},
		},
		{
			desc: "check tags with tags",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))
				tagID := gittest.WriteTag(t, cfg, remoteRepoPath, "testtag", "main")

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						CheckTagsChanged: true,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":   commitID,
								"refs/tags/testtag": tagID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
					},
				}
			},
		},
		{
			desc: "check tags with tags (second pull)",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))
				tagID := gittest.WriteTag(t, cfg, remoteRepoPath, "testtag", "main")

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						CheckTagsChanged: true,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":   commitID,
								"refs/tags/testtag": tagID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":   commitID,
								"refs/tags/testtag": tagID,
							},
							// second time around it shouldn't have changed tags
							expectedResponse: &gitalypb.FetchRemoteResponse{},
						},
					},
				}
			},
		},
		{
			desc: "without checking for changed tags",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))
				tagID := gittest.WriteTag(t, cfg, remoteRepoPath, "testtag", "main")

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						CheckTagsChanged: false,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":   commitID,
								"refs/tags/testtag": tagID,
							},
							// TagsChanged is set to true as we have requested to not check for tags changed
							// in the request so it defaults to true.
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
						// Run a second time to ensure it is consistent
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":   commitID,
								"refs/tags/testtag": tagID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
					},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			cfg, client := setupRepositoryServiceWithoutRepo(t)
			setupData := tc.setup(t, cfg)

			for _, run := range setupData.runs {
				response, err := client.FetchRemote(ctx, setupData.request)
				testhelper.RequireGrpcError(t, run.expectedErr, err)
				testhelper.ProtoEqual(t, run.expectedResponse, response)

				refs := map[string]git.ObjectID{}
				refLines := text.ChompBytes(gittest.Exec(t, cfg, "-C", setupData.repoPath, "for-each-ref", `--format=%(refname) %(objectname)`))
				if refLines != "" {
					for _, line := range strings.Split(refLines, "\n") {
						refname, objectID, found := strings.Cut(line, " ")
						require.True(t, found, "shouldn't have issues parsing the refs")
						refs[refname] = git.ObjectID(objectID)
					}
				}
				require.Equal(t, run.expectedRefs, refs)
			}
		})
	}
}

func TestFetchRemote_sshCommand(t *testing.T) {
	testhelper.SkipWithPraefect(t, "It's not possible to create repositories through the API with the git command overwritten by the script.")

	t.Parallel()

	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)

	repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		Seed:                   gittest.SeedGitLabTest,
	})

	outputPath := filepath.Join(testhelper.TempDir(t), "output")

	// We ain't got a nice way to intercept the SSH call, so we just write a custom git command
	// which simply prints the GIT_SSH_COMMAND environment variable.
	gitCmdFactory := gittest.NewInterceptingCommandFactory(t, ctx, cfg, func(git.ExecutionEnvironment) string {
		return fmt.Sprintf(
			`#!/bin/sh
			for arg in $GIT_SSH_COMMAND
			do
				case "$arg" in
				-oIdentityFile=*)
					path=$(echo "$arg" | cut -d= -f2)
					cat "$path";;
				*)
					echo "$arg";;
				esac
			done >'%s'
			exit 7
		`, outputPath)
	})

	client, _ := runRepositoryService(t, cfg, nil, testserver.WithGitCommandFactory(gitCmdFactory))

	for _, tc := range []struct {
		desc           string
		request        *gitalypb.FetchRemoteRequest
		expectedOutput string
	}{
		{
			desc: "remote parameters without SSH key",
			request: &gitalypb.FetchRemoteRequest{
				Repository: repo,
				RemoteParams: &gitalypb.Remote{
					Url: "https://example.com",
				},
			},
			expectedOutput: "ssh\n",
		},
		{
			desc: "remote parameters with SSH key",
			request: &gitalypb.FetchRemoteRequest{
				Repository: repo,
				RemoteParams: &gitalypb.Remote{
					Url: "https://example.com",
				},
				SshKey: "mykey",
			},
			expectedOutput: "ssh\n-oIdentitiesOnly=yes\nmykey",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.FetchRemote(ctx, tc.request)
			require.Error(t, err)
			require.Contains(t, err.Error(), "fetch remote: exit status 7")

			output := testhelper.MustReadFile(t, outputPath)
			require.Equal(t, tc.expectedOutput, string(output))

			require.NoError(t, os.Remove(outputPath))
		})
	}
}

func TestFetchRemote_withDefaultRefmaps(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, sourceRepoProto, sourceRepoPath, client := setupRepositoryService(t, ctx)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)

	sourceRepo := localrepo.NewTestRepo(t, cfg, sourceRepoProto)

	targetRepoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})
	targetRepo := localrepo.NewTestRepo(t, cfg, targetRepoProto)

	port := gittest.HTTPServer(t, ctx, gitCmdFactory, sourceRepoPath, nil)

	require.NoError(t, sourceRepo.UpdateRef(ctx, "refs/heads/foobar", "refs/heads/master", ""))

	// With no refmap given, FetchRemote should fall back to
	// "refs/heads/*:refs/heads/*" and thus mirror what's in the source
	// repository.
	resp, err := client.FetchRemote(ctx, &gitalypb.FetchRemoteRequest{
		Repository: targetRepoProto,
		RemoteParams: &gitalypb.Remote{
			Url: fmt.Sprintf("http://127.0.0.1:%d/%s", port, filepath.Base(sourceRepoPath)),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	sourceRefs, err := sourceRepo.GetReferences(ctx)
	require.NoError(t, err)
	targetRefs, err := targetRepo.GetReferences(ctx)
	require.NoError(t, err)
	require.Equal(t, sourceRefs, targetRefs)
}

func TestFetchRemote_transaction(t *testing.T) {
	t.Parallel()
	sourceCfg := testcfg.Build(t)

	txManager := transaction.NewTrackingManager()
	client, addr := runRepositoryService(t, sourceCfg, nil, testserver.WithTransactionManager(txManager))
	sourceCfg.SocketPath = addr

	ctx := testhelper.Context(t)
	repo, _ := gittest.CreateRepository(t, ctx, sourceCfg, gittest.CreateRepositoryConfig{
		RelativePath: t.Name(),
		Seed:         gittest.SeedGitLabTest,
	})
	// Reset the manager as creating the repository casts some votes.
	txManager.Reset()

	targetCfg := testcfg.Build(t)
	targetRepoProto, targetRepoPath := gittest.CreateRepository(t, ctx, targetCfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		Seed:                   gittest.SeedGitLabTest,
		RelativePath:           t.Name(),
	})
	targetGitCmdFactory := gittest.NewCommandFactory(t, targetCfg)

	port := gittest.HTTPServer(t, ctx, targetGitCmdFactory, targetRepoPath, nil)

	ctx, err := txinfo.InjectTransaction(ctx, 1, "node", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	require.Equal(t, 0, len(txManager.Votes()))

	_, err = client.FetchRemote(ctx, &gitalypb.FetchRemoteRequest{
		Repository: targetRepoProto,
		RemoteParams: &gitalypb.Remote{
			Url: fmt.Sprintf("http://127.0.0.1:%d/%s", port, repo.GetRelativePath()),
		},
	})
	require.NoError(t, err)

	require.Equal(t, 1, len(txManager.Votes()))
}

func TestFetchRemote_prune(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, _, sourceRepoPath, client := setupRepositoryService(t, ctx)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)

	port := gittest.HTTPServer(t, ctx, gitCmdFactory, sourceRepoPath, nil)

	remoteURL := fmt.Sprintf("http://127.0.0.1:%d/%s", port, filepath.Base(sourceRepoPath))

	for _, tc := range []struct {
		desc        string
		request     *gitalypb.FetchRemoteRequest
		ref         git.ReferenceName
		shouldExist bool
	}{
		{
			desc: "NoPrune=true with explicit Remote should not delete reference",
			request: &gitalypb.FetchRemoteRequest{
				RemoteParams: &gitalypb.Remote{
					Url: remoteURL,
				},
				NoPrune: true,
			},
			ref:         "refs/heads/nonexistent",
			shouldExist: true,
		},
		{
			desc: "NoPrune=false with explicit Remote should delete reference",
			request: &gitalypb.FetchRemoteRequest{
				RemoteParams: &gitalypb.Remote{
					Url: remoteURL,
				},
				NoPrune: false,
			},
			ref:         "refs/heads/nonexistent",
			shouldExist: false,
		},
		{
			desc: "NoPrune=false with explicit Remote should not delete reference outside of refspec",
			request: &gitalypb.FetchRemoteRequest{
				RemoteParams: &gitalypb.Remote{
					Url: remoteURL,
					MirrorRefmaps: []string{
						"refs/heads/*:refs/remotes/my-remote/*",
					},
				},
				NoPrune: false,
			},
			ref:         "refs/heads/nonexistent",
			shouldExist: true,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			targetRepoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				Seed: gittest.SeedGitLabTest,
			})
			targetRepo := localrepo.NewTestRepo(t, cfg, targetRepoProto)

			require.NoError(t, targetRepo.UpdateRef(ctx, tc.ref, "refs/heads/master", ""))

			tc.request.Repository = targetRepoProto
			resp, err := client.FetchRemote(ctx, tc.request)
			require.NoError(t, err)
			require.NotNil(t, resp)

			hasRevision, err := targetRepo.HasRevision(ctx, tc.ref.Revision())
			require.NoError(t, err)
			require.Equal(t, tc.shouldExist, hasRevision)
		})
	}
}

func TestFetchRemote_force(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, sourceRepoProto, sourceRepoPath, client := setupRepositoryService(t, ctx)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)

	sourceRepo := localrepo.NewTestRepo(t, cfg, sourceRepoProto)

	branchOID, err := sourceRepo.ResolveRevision(ctx, "refs/heads/master")
	require.NoError(t, err)

	tagOID, err := sourceRepo.ResolveRevision(ctx, "refs/tags/v1.0.0")
	require.NoError(t, err)

	divergingBranchOID := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("b1"))
	divergingTagOID := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("b2"))

	port := gittest.HTTPServer(t, ctx, gitCmdFactory, sourceRepoPath, nil)

	remoteURL := fmt.Sprintf("http://127.0.0.1:%d/%s", port, filepath.Base(sourceRepoPath))

	for _, tc := range []struct {
		desc         string
		request      *gitalypb.FetchRemoteRequest
		expectedErr  error
		expectedRefs map[git.ReferenceName]git.ObjectID
	}{
		{
			desc: "remote params without force fails with diverging refs",
			request: &gitalypb.FetchRemoteRequest{
				RemoteParams: &gitalypb.Remote{
					Url: remoteURL,
				},
			},
			expectedErr: status.Error(codes.Internal, "fetch remote: exit status 1"),
			expectedRefs: map[git.ReferenceName]git.ObjectID{
				"refs/heads/master": branchOID,
				"refs/tags/v1.0.0":  tagOID,
			},
		},
		{
			desc: "remote params with force updates diverging refs",
			request: &gitalypb.FetchRemoteRequest{
				RemoteParams: &gitalypb.Remote{
					Url: remoteURL,
				},
				Force: true,
			},
			expectedRefs: map[git.ReferenceName]git.ObjectID{
				"refs/heads/master": divergingBranchOID,
				"refs/tags/v1.0.0":  divergingTagOID,
			},
		},
		{
			desc: "remote params with force-refmap fails with divergent tag",
			request: &gitalypb.FetchRemoteRequest{
				RemoteParams: &gitalypb.Remote{
					Url: remoteURL,
					MirrorRefmaps: []string{
						"+refs/heads/master:refs/heads/master",
					},
				},
			},
			// The master branch has been updated to the diverging branch, but the
			// command still fails because we do fetch tags by default, and the tag did
			// diverge.
			expectedErr: status.Error(codes.Internal, "fetch remote: exit status 1"),
			expectedRefs: map[git.ReferenceName]git.ObjectID{
				"refs/heads/master": divergingBranchOID,
				"refs/tags/v1.0.0":  tagOID,
			},
		},
		{
			desc: "remote params with explicit refmap and force updates divergent tag",
			request: &gitalypb.FetchRemoteRequest{
				RemoteParams: &gitalypb.Remote{
					Url: remoteURL,
					MirrorRefmaps: []string{
						"refs/heads/master:refs/heads/master",
					},
				},
				Force: true,
			},
			expectedRefs: map[git.ReferenceName]git.ObjectID{
				"refs/heads/master": divergingBranchOID,
				"refs/tags/v1.0.0":  divergingTagOID,
			},
		},
		{
			desc: "remote params with force-refmap and no tags only updates refspec",
			request: &gitalypb.FetchRemoteRequest{
				RemoteParams: &gitalypb.Remote{
					Url: remoteURL,
					MirrorRefmaps: []string{
						"+refs/heads/master:refs/heads/master",
					},
				},
				NoTags: true,
			},
			expectedRefs: map[git.ReferenceName]git.ObjectID{
				"refs/heads/master": divergingBranchOID,
				"refs/tags/v1.0.0":  tagOID,
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			targetRepoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				Seed: gittest.SeedGitLabTest,
			})

			targetRepo := localrepo.NewTestRepo(t, cfg, targetRepoProto)

			// We're force-updating a branch and a tag in the source repository to point
			// to a diverging object ID in order to verify that the `force` parameter
			// takes effect.
			require.NoError(t, sourceRepo.UpdateRef(ctx, "refs/heads/master", divergingBranchOID, branchOID))
			require.NoError(t, sourceRepo.UpdateRef(ctx, "refs/tags/v1.0.0", divergingTagOID, tagOID))
			defer func() {
				// Restore references after the current testcase again. Moving
				// source repository setup into the testcases is not easily possible
				// because hosting the gitserver requires the repo path, and we need
				// the URL for our testcases.
				require.NoError(t, sourceRepo.UpdateRef(ctx, "refs/heads/master", branchOID, divergingBranchOID))
				require.NoError(t, sourceRepo.UpdateRef(ctx, "refs/tags/v1.0.0", tagOID, divergingTagOID))
			}()

			tc.request.Repository = targetRepoProto
			_, err := client.FetchRemote(ctx, tc.request)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)

			updatedBranchOID, err := targetRepo.ResolveRevision(ctx, "refs/heads/master")
			require.NoError(t, err)
			updatedTagOID, err := targetRepo.ResolveRevision(ctx, "refs/tags/v1.0.0")
			require.NoError(t, err)

			require.Equal(t, map[git.ReferenceName]git.ObjectID{
				"refs/heads/master": updatedBranchOID,
				"refs/tags/v1.0.0":  updatedTagOID,
			}, tc.expectedRefs)
		})
	}
}

func TestFetchRemote_inputValidation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupRepositoryService(t, ctx)

	const remoteName = "test-repo"
	httpSrv, _ := remoteHTTPServer(t, remoteName, httpHost, httpToken)
	defer httpSrv.Close()

	tests := []struct {
		desc   string
		req    *gitalypb.FetchRemoteRequest
		code   codes.Code
		errMsg string
	}{
		{
			desc: "no repository",
			req: &gitalypb.FetchRemoteRequest{
				Repository: nil,
				RemoteParams: &gitalypb.Remote{
					Url: remoteName,
				},
				Timeout: 1000,
			},
			code:   codes.InvalidArgument,
			errMsg: "empty Repository",
		},
		{
			desc: "invalid storage",
			req: &gitalypb.FetchRemoteRequest{
				Repository: &gitalypb.Repository{
					StorageName:  "invalid",
					RelativePath: "foobar.git",
				},
				RemoteParams: &gitalypb.Remote{
					Url: remoteName,
				},
				Timeout: 1000,
			},
			// the error text is shortened to only a single word as requests to gitaly done via praefect returns different error messages
			code:   codes.InvalidArgument,
			errMsg: "invalid",
		},
		{
			desc: "missing remote",
			req: &gitalypb.FetchRemoteRequest{
				Repository: repo,
				Timeout:    1000,
			},
			code:   codes.InvalidArgument,
			errMsg: "missing remote params",
		},
		{
			desc: "invalid remote url",
			req: &gitalypb.FetchRemoteRequest{
				Repository: repo,
				RemoteParams: &gitalypb.Remote{
					Url: "",
				},
				Timeout: 1000,
			},
			code:   codes.InvalidArgument,
			errMsg: `blank or empty remote URL`,
		},
		{
			desc: "not existing repo via http",
			req: &gitalypb.FetchRemoteRequest{
				Repository: repo,
				RemoteParams: &gitalypb.Remote{
					Url:                     httpSrv.URL + "/invalid/repo/path.git",
					HttpAuthorizationHeader: httpToken,
					HttpHost:                httpHost,
					MirrorRefmaps:           []string{"all_refs"},
				},
				Timeout: 1000,
			},
			code:   codes.Internal,
			errMsg: "invalid/repo/path.git/' not found",
		},
		{
			desc: "/dev/null",
			req: &gitalypb.FetchRemoteRequest{
				Repository: repo,
				RemoteParams: &gitalypb.Remote{
					Url: "/dev/null",
				},
				Timeout: 1000,
			},
			code:   codes.Internal,
			errMsg: "'/dev/null' does not appear to be a git repository",
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			resp, err := client.FetchRemote(ctx, tc.req)
			require.Error(t, err)
			require.Nil(t, resp)

			require.Contains(t, err.Error(), tc.errMsg)
			testhelper.RequireGrpcCode(t, err, tc.code)
		})
	}
}

const (
	httpToken = "ABCefg0999182"
	httpHost  = "example.com"
)

func remoteHTTPServer(t *testing.T, repoName, httpHost, httpToken string) (*httptest.Server, string) {
	b := testhelper.MustReadFile(t, "testdata/advertise.txt")

	s := httptest.NewServer(
		// https://github.com/git/git/blob/master/Documentation/technical/http-protocol.txt
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != httpHost {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			if r.URL.String() != fmt.Sprintf("/%s.git/info/refs?service=git-upload-pack", repoName) {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			if httpToken != "" && r.Header.Get("Authorization") != httpToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
			_, err := w.Write(b)
			assert.NoError(t, err)
		}),
	)

	return s, fmt.Sprintf("%s/%s.git", s.URL, repoName)
}

func getRefnames(t *testing.T, cfg config.Cfg, repoPath string) []string {
	result := gittest.Exec(t, cfg, "-C", repoPath, "for-each-ref", "--format", "%(refname:lstrip=2)")
	return strings.Split(text.ChompBytes(result), "\n")
}

func TestFetchRemote_http(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryServiceWithoutRepo(t)

	testCases := []struct {
		description string
		httpToken   string
		remoteURL   string
	}{
		{
			description: "with http token",
			httpToken:   httpToken,
		},
		{
			description: "without http token",
			httpToken:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			forkedRepo, forkedRepoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				Seed: gittest.SeedGitLabTest,
			})

			s, remoteURL := remoteHTTPServer(t, "my-repo", httpHost, tc.httpToken)
			defer s.Close()

			req := &gitalypb.FetchRemoteRequest{
				Repository: forkedRepo,
				RemoteParams: &gitalypb.Remote{
					Url:                     remoteURL,
					HttpAuthorizationHeader: tc.httpToken,
					HttpHost:                httpHost,
					MirrorRefmaps:           []string{"all_refs"},
				},
				Timeout: 1000,
			}
			if tc.remoteURL != "" {
				req.RemoteParams.Url = s.URL + tc.remoteURL
			}

			refs := getRefnames(t, cfg, forkedRepoPath)
			require.True(t, len(refs) > 1, "the advertisement.txt should have deleted all refs except for master")

			_, err := client.FetchRemote(ctx, req)
			require.NoError(t, err)

			refs = getRefnames(t, cfg, forkedRepoPath)

			require.Len(t, refs, 1)
			assert.Equal(t, "master", refs[0])
		})
	}
}

func TestFetchRemote_localPath(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, _, sourceRepoPath, client := setupRepositoryService(t, ctx)

	mirrorRepo, mirrorRepoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	_, err := client.FetchRemote(ctx, &gitalypb.FetchRemoteRequest{
		Repository: mirrorRepo,
		RemoteParams: &gitalypb.Remote{
			Url: sourceRepoPath,
		},
	})
	require.NoError(t, err)

	require.Equal(t, getRefnames(t, cfg, sourceRepoPath), getRefnames(t, cfg, mirrorRepoPath))
}

func TestFetchRemote_httpWithRedirect(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupRepositoryService(t, ctx)

	s := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/info/refs?service=git-upload-pack", r.URL.String())
			http.Redirect(w, r, "/redirect_url", http.StatusSeeOther)
		}),
	)
	defer s.Close()

	req := &gitalypb.FetchRemoteRequest{
		Repository:   repo,
		RemoteParams: &gitalypb.Remote{Url: s.URL},
		Timeout:      1000,
	}

	_, err := client.FetchRemote(ctx, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "The requested URL returned error: 303")
}

func TestFetchRemote_httpWithTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testhelper.Context(t))
	_, repo, _, client := setupRepositoryService(t, ctx)

	s := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/info/refs?service=git-upload-pack", r.URL.String())
			// Block the request forever.
			<-ctx.Done()
		}),
	)
	defer func() {
		// We need to explicitly cancel the context here, or otherwise we'll be stuck
		// closing the server due to the ongoing request.
		cancel()
		s.Close()
	}()

	req := &gitalypb.FetchRemoteRequest{
		Repository:   repo,
		RemoteParams: &gitalypb.Remote{Url: s.URL},
		Timeout:      1,
	}

	_, err := client.FetchRemote(ctx, req)
	require.Error(t, err)

	require.Contains(t, err.Error(), "fetch remote: signal: terminated")
}

func TestFetchRemote_pooledRepository(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	// By default git-fetch(1) will always run with `core.alternateRefsCommand=exit 0 #`, which
	// effectively disables use of alternate refs. We can't just unset this value, so instead we
	// just write a script that knows to execute git-for-each-ref(1) as expected by this config
	// option.
	//
	// Note that we're using a separate command factory here just to ease the setup because we
	// need to recreate the other command factory with the Git configuration specified by the
	// test.
	alternateRefsCommandFactory := gittest.NewCommandFactory(t, testcfg.Build(t))
	exec := testhelper.WriteExecutable(t,
		filepath.Join(testhelper.TempDir(t), "alternate-refs"),
		[]byte(fmt.Sprintf(`#!/bin/sh
			exec %q -C "$1" for-each-ref --format='%%(objectname)'
		`, alternateRefsCommandFactory.GetExecutionEnvironment(ctx).BinaryPath)),
	)

	for _, tc := range []struct {
		desc                     string
		cfg                      config.Cfg
		shouldAnnouncePooledRefs bool
	}{
		{
			desc: "with default configuration",
		},
		{
			desc: "with alternates",
			cfg: config.Cfg{
				Git: config.Git{
					Config: []config.GitConfig{
						{
							Key:   "core.alternateRefsCommand",
							Value: exec,
						},
					},
				},
			},
			shouldAnnouncePooledRefs: true,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := testcfg.Build(t, testcfg.WithBase(tc.cfg))
			gitCmdFactory := gittest.NewCommandFactory(t, cfg)

			client, swocketPath := runRepositoryService(t, cfg, nil, testserver.WithGitCommandFactory(gitCmdFactory))
			cfg.SocketPath = swocketPath

			// Create a repository that emulates an object pool. This object contains a
			// single reference with an object that is neither in the pool member nor in
			// the remote. If alternate refs are used, then Git will announce it to the
			// remote as "have".
			_, poolRepoPath := gittest.CreateRepository(t, ctx, cfg)
			poolCommitID := gittest.WriteCommit(t, cfg, poolRepoPath,
				gittest.WithBranch("pooled"),
				gittest.WithTreeEntries(gittest.TreeEntry{Path: "pool", Mode: "100644", Content: "pool contents"}),
			)

			// Create the pooled repository and link it to its pool. This is the
			// repository we're fetching into.
			pooledRepoProto, pooledRepoPath := gittest.CreateRepository(t, ctx, cfg)
			require.NoError(t, os.WriteFile(filepath.Join(pooledRepoPath, "objects", "info", "alternates"), []byte(filepath.Join(poolRepoPath, "objects")), 0o644))

			// And then finally create a third repository that emulates the remote side
			// we're fetching from. We need to create at least one reference so that Git
			// would actually try to fetch objects.
			_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
			gittest.WriteCommit(t, cfg, remoteRepoPath,
				gittest.WithBranch("remote"),
				gittest.WithTreeEntries(gittest.TreeEntry{Path: "remote", Mode: "100644", Content: "remote contents"}),
			)

			// Set up an HTTP server and intercept the request. This is done so that we
			// can observe the reference negotiation and check whether alternate refs
			// are announced or not.
			var requestBuffer bytes.Buffer
			port := gittest.HTTPServer(t, ctx, gitCmdFactory, remoteRepoPath, func(responseWriter http.ResponseWriter, request *http.Request, handler http.Handler) {
				closer := request.Body
				defer testhelper.MustClose(t, closer)

				request.Body = io.NopCloser(io.TeeReader(request.Body, &requestBuffer))

				handler.ServeHTTP(responseWriter, request)
			})

			// Perform the fetch.
			_, err := client.FetchRemote(ctx, &gitalypb.FetchRemoteRequest{
				Repository: pooledRepoProto,
				RemoteParams: &gitalypb.Remote{
					Url: fmt.Sprintf("http://127.0.0.1:%d/%s", port, filepath.Base(remoteRepoPath)),
				},
			})
			require.NoError(t, err)

			// This should result in the "remote" branch having been fetched into the
			// pooled repository.
			require.Equal(t,
				gittest.ResolveRevision(t, cfg, pooledRepoPath, "refs/heads/remote"),
				gittest.ResolveRevision(t, cfg, remoteRepoPath, "refs/heads/remote"),
			)

			// Verify whether alternate refs have been announced as part of the
			// reference negotiation phase.
			if tc.shouldAnnouncePooledRefs {
				require.Contains(t, requestBuffer.String(), fmt.Sprintf("have %s", poolCommitID))
			} else {
				require.NotContains(t, requestBuffer.String(), fmt.Sprintf("have %s", poolCommitID))
			}
		})
	}
}
