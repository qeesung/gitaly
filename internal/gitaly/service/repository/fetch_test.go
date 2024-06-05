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

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				commitID := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceRepository: sourceRepo,
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
			desc: "success with expected_target_old_oid",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				sourceCommit := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch(git.DefaultBranch))

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				firstCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
				secondCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(firstCommit), gittest.WithBranch(git.DefaultBranch))

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:           repo,
						SourceRepository:     sourceRepo,
						SourceBranch:         []byte(git.DefaultBranch),
						TargetRef:            []byte(git.DefaultRef),
						ExpectedTargetOldOid: secondCommit.String(),
					},
					verify: func() {
						actualCommitID := gittest.ResolveRevision(t, cfg, repoPath, git.DefaultRef.String()+"^{commit}")
						require.Equal(t, sourceCommit, actualCommitID)
					},
				}
			},
			expectedResponse: &gitalypb.FetchSourceBranchResponse{Result: true},
		},
		{
			desc: "success + same repository",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				commitID := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       sourceRepo,
						SourceRepository: sourceRepo,
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
			desc: "failure due to incorrect expected_target_old_oid",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				sourceCommit := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch(git.DefaultBranch))

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				firstCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
				concurrentCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(firstCommit), gittest.WithBranch(git.DefaultBranch))

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:           repo,
						SourceRepository:     sourceRepo,
						SourceBranch:         []byte(git.DefaultBranch),
						TargetRef:            []byte(git.DefaultRef),
						ExpectedTargetOldOid: firstCommit.String(),
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewInternal("commit: reference does not point to expected object"),
						structerr.MetadataItem{Key: "actual_object_id", Value: concurrentCommit.String()},
						structerr.MetadataItem{Key: "expected_object_id", Value: sourceCommit.String()},
						structerr.MetadataItem{Key: "reference", Value: "refs/heads/main"},
					),
				}
			},
		},
		{
			desc: "failure due to branch not found",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath)
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceRepository: sourceRepo,
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

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       sourceRepo,
						SourceRepository: sourceRepo,
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

				sourceRepo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						SourceRepository: sourceRepo,
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

				sourceRepo, _ := gittest.CreateRepository(t, ctx, cfg)
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceRepository: sourceRepo,
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

				sourceRepo, _ := gittest.CreateRepository(t, ctx, cfg)
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceBranch:     []byte("   "),
						SourceRepository: sourceRepo,
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

				sourceRepo, _ := gittest.CreateRepository(t, ctx, cfg)
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceBranch:     []byte("-ref"),
						SourceRepository: sourceRepo,
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

				sourceRepo, _ := gittest.CreateRepository(t, ctx, cfg)
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceBranch:     []byte("some:ref"),
						SourceRepository: sourceRepo,
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

				sourceRepo, _ := gittest.CreateRepository(t, ctx, cfg)
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceBranch:     []byte("some\x00ref"),
						SourceRepository: sourceRepo,
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

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceBranch:     []byte("master"),
						SourceRepository: sourceRepo,
					},
					expectedErr: structerr.NewInvalidArgument("empty revision"),
				}
			},
		},
		{
			desc: "failure due to blanks in target ref",
			setup: func(t *testing.T) setupData {
				cfg, client := setupRepositoryService(t)

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceBranch:     []byte("master"),
						SourceRepository: sourceRepo,
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

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceBranch:     []byte("master"),
						SourceRepository: sourceRepo,
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

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceBranch:     []byte("master"),
						SourceRepository: sourceRepo,
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

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceBranch:     []byte("master"),
						SourceRepository: sourceRepo,
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
				// git failed. We match against a config value that is only present during
				// a fetch.
				gitCmdFactory := gittest.NewInterceptingCommandFactory(t, ctx, cfg, func(execEnv git.ExecutionEnvironment) string {
					return fmt.Sprintf(`#!/usr/bin/env bash
						if [[ "$@" =~ "fetch.writeCommitGraph" ]]; then
							%q "$@"
							exit 1
						fi
						exec %q "$@"`, execEnv.BinaryPath, execEnv.BinaryPath)
				})

				client, serverSocketPath := runRepositoryService(t, cfg, testserver.WithGitCommandFactory(gitCmdFactory))
				cfg.SocketPath = serverSocketPath

				sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
				commitID := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					cfg:    cfg,
					client: client,
					request: &gitalypb.FetchSourceBranchRequest{
						Repository:       repo,
						SourceRepository: sourceRepo,
						SourceBranch:     []byte("master"),
						TargetRef:        []byte("refs/tmp/fetch-source-branch-test"),
					},
					verify: func() {
						repo := localrepo.NewTestRepo(t, cfg, repo)
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
