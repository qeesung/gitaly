package repository

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestFetchSourceBranch(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	type setupData struct {
		cfg         config.Cfg
		client      gitalypb.RepositoryServiceClient
		request     *gitalypb.FetchSourceBranchRequest
		verify      func()
		expectedErr error
	}

	for _, tc := range []struct {
		desc             string
		setup            func(t *testing.T) setupData
		expectedResponse *gitalypb.FetchSourceBranchResponse
	}{
		{
			desc: "success",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				commitID := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceRepository: sourceRepoProto,
						SourceBranch:     []byte("master"),
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
					verify: func() {
						actualCommitID := gittest.ResolveRevision(t, cfg, repoPath, "refs/tmp/fetch-source-branch-test^{commit}")
						require.Equal(t, commitID, actualCommitID)
					},
				}
			},
			expectedResponse: &gitalypb.FetchSourceBranchResponse{Result: true},
		},
		{
			desc: "success + same repository",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				commitID := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       sourceRepoProto,
						SourceRepository: sourceRepoProto,
						SourceBranch:     []byte("master"),
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
					verify: func() {
						actualCommitID := gittest.ResolveRevision(t, cfg, sourceRepoPath, "refs/tmp/fetch-source-branch-test^{commit}")
						require.Equal(t, commitID, actualCommitID)
					},
				}
			},
			expectedResponse: &gitalypb.FetchSourceBranchResponse{Result: true},
		},
		{
			desc: "failure due to branch not found",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath)
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceRepository: sourceRepoProto,
						SourceBranch:     []byte("master"),
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
				}
			},
			expectedResponse: &gitalypb.FetchSourceBranchResponse{Result: false},
		},
		{
			desc: "failure due to branch not found (same repo)",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       sourceRepoProto,
						SourceRepository: sourceRepoProto,
						SourceBranch:     []byte("master"),
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
				}
			},
			expectedResponse: &gitalypb.FetchSourceBranchResponse{Result: false},
		},
		{
			desc: "failure due to no repository provided",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						SourceRepository: sourceRepoProto,
						SourceBranch:     []byte("master"),
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
					expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
				}
			},
		},
		{
			desc: "failure due to no source branch",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, _ := gittest.CreateRepository(t, ctx, cfg)
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceRepository: sourceRepoProto,
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
					expectedErr: structerr.NewInvalidArgument("empty revision"),
				}
			},
		},
		{
			desc: "failure due to blanks in source branch",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, _ := gittest.CreateRepository(t, ctx, cfg)
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceBranch:     []byte("   "),
						SourceRepository: sourceRepoProto,
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't contain whitespace"),
				}
			},
		},
		{
			desc: "failure due to source branch starting with -",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, _ := gittest.CreateRepository(t, ctx, cfg)
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceBranch:     []byte("-ref"),
						SourceRepository: sourceRepoProto,
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't start with '-'"),
				}
			},
		},
		{
			desc: "failure due to source branch with :",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, _ := gittest.CreateRepository(t, ctx, cfg)
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceBranch:     []byte("some:ref"),
						SourceRepository: sourceRepoProto,
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't contain ':'"),
				}
			},
		},
		{
			desc: "failure due to source branch with NUL",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, _ := gittest.CreateRepository(t, ctx, cfg)
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceBranch:     []byte("some\x00ref"),
						SourceRepository: sourceRepoProto,
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't contain NUL"),
				}
			},
		},
		{
			desc: "failure due to no target ref",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceBranch:     []byte("master"),
						SourceRepository: sourceRepoProto,
					},
					expectedErr: structerr.NewInvalidArgument("empty revision"),
				}
			},
		},
		{
			desc: "failure due to blanks in target ref",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceBranch:     []byte("master"),
						SourceRepository: sourceRepoProto,
						TargetRef:        []byte("   "),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't contain whitespace"),
				}
			},
		},
		{
			desc: "failure due to target ref starting with -",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceBranch:     []byte("master"),
						SourceRepository: sourceRepoProto,
						TargetRef:        []byte("-ref"),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't start with '-'"),
				}
			},
		},
		{
			desc: "failure due to target ref with :",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceBranch:     []byte("master"),
						SourceRepository: sourceRepoProto,
						TargetRef:        []byte("some:ref"),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't contain ':'"),
				}
			},
		},
		{
			desc: "failure due to target ref with NUL",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepoProto, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceBranch:     []byte("master"),
						SourceRepository: sourceRepoProto,
						TargetRef:        []byte("some\x00ref"),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't contain NUL"),
				}
			},
		},
		{
			desc: "failure during/after fetch doesn't clean out fetched objects",
			setup: func(t *testing.T) setupData {
				cfg := testcfg.Build(t)

				testcfg.BuildGitalyHooks(t, cfg)
				testcfg.BuildGitalySSH(t, cfg)

				// We simulate a failed fetch where we actually fetch but just exit
				// with status 1, this will actually fetch the refs but gitaly will think
				// git failed.
				gitCmdFactory := gittest.NewInterceptingCommandFactory(t, ctx, cfg, func(execEnv git.ExecutionEnvironment) string {
					return fmt.Sprintf(`#!/usr/bin/env bash
						if [[ "$@" =~ "fetch" ]]; then
							%q "$@"
							exit 1
						fi
						exec %q "$@"`, execEnv.BinaryPath, execEnv.BinaryPath)
				})

				client, serverSocketPath := runRepositoryService(t, cfg, testserver.WithGitCommandFactory(gitCmdFactory))
				cfg.SocketPath = serverSocketPath

				sourceRepoProto, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				commitID := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repoProto,
						SourceRepository: sourceRepoProto,
						SourceBranch:     []byte("master"),
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
					verify: func() {
						repo := localrepo.NewTestRepo(t, cfg, repoProto)
						exists, err := repo.HasRevision(ctx, commitID.Revision()+"^{commit}")
						require.NoError(t, err)
						require.False(t, exists, "fetched commit isn't discarded")
					},
				}
			},
			expectedResponse: &gitalypb.FetchSourceBranchResponse{Result: false},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			data := tc.setup(t)

			md := testcfg.GitalyServersMetadataFromCfg(t, data.cfg)
			ctx = testhelper.MergeOutgoingMetadata(ctx, md)

			resp, err := data.client.FetchSourceBranch(ctx, data.request)
			testhelper.RequireGrpcError(t, data.expectedErr, err)

			if data.verify != nil {
				data.verify()
			}
			testhelper.ProtoEqual(t, tc.expectedResponse, resp)
		})
	}
}
