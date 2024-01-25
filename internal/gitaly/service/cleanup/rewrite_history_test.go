package cleanup

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRewriteHistory(t *testing.T) {
	t.Parallel()
	gittest.SkipWithSHA256(t)

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)
	cfg.SocketPath = runCleanupServiceServer(t, cfg)

	client, conn := newCleanupServiceClient(t, cfg.SocketPath)
	t.Cleanup(func() { conn.Close() })

	addUnmodifiedRefs := func(repoPath string) []git.Reference {
		unmodifiedCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("unmodified"), gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "unmodified-file", Mode: "100644", Content: "can't touch this"},
		))
		unmodifiedTag := gittest.WriteTag(t, cfg, repoPath, "unmodified-tag", unmodifiedCommit.Revision(), gittest.WriteTagConfig{
			Message: "annotated",
		})
		keepRef := git.ReferenceName("refs/keeparound/" + unmodifiedCommit.String())
		gittest.WriteRef(t, cfg, repoPath, keepRef, unmodifiedCommit)

		return []git.Reference{
			{
				Name:   "refs/heads/unmodified",
				Target: unmodifiedCommit.String(),
			},
			{
				Name:   "refs/tags/unmodified-tag",
				Target: unmodifiedTag.String(),
			},
			{
				Name:   keepRef,
				Target: unmodifiedCommit.String(),
			},
		}
	}

	type setupData struct {
		requests         []*gitalypb.RewriteHistoryRequest
		repoPath         string
		expectedRefs     []git.Reference
		expectedErr      error
		expectedResponse *gitalypb.RewriteHistoryResponse
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "no requests",
			setup: func(t *testing.T) setupData {
				return setupData{
					requests: nil,
					expectedErr: testhelper.GitalyOrPraefect(
						structerr.NewInternal("receiving initial request: EOF"),
						structerr.NewInternal("EOF"),
					),
				}
			},
		},
		{
			desc: "missing repository",
			setup: func(t *testing.T) setupData {
				repoPath := gittest.NewRepositoryName(t)

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: &gitalypb.Repository{
								StorageName:  cfg.Storages[0].Name,
								RelativePath: repoPath,
							},
							Redactions: [][]byte{[]byte("hunter2")},
						},
					},
					expectedErr: testhelper.ToInterceptedMetadata(
						structerr.New("%w", storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, repoPath)),
					),
				}
			},
		},
		{
			desc: "repository in subsequent request",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
							Redactions: [][]byte{[]byte("hunter2")},
						},
						{
							Repository: repo,
							Redactions: [][]byte{[]byte("secretpassword")},
						},
					},
					expectedErr: structerr.NewInvalidArgument("subsequent requests must not contain repository"),
				}
			},
		},
		{
			desc: "all requests empty",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
						},
						{},
					},
					expectedErr: structerr.NewInvalidArgument("no object IDs or text replacements specified"),
				}
			},
		},
		{
			desc: "remove invalid oid",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				unmodifiedRefs := addUnmodifiedRefs(repoPath)

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
						},
						{
							Blobs: []string{"invalid oid"},
						},
					},
					repoPath:     repoPath,
					expectedRefs: unmodifiedRefs,
					expectedErr: testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument("validating object ID: invalid object ID: \"invalid oid\", expected length %v, got 11", gittest.DefaultObjectHash.EncodedLen()),
						"oid", "invalid oid",
					),
				}
			},
		},
		{
			desc: "redaction pattern with newline",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				unmodifiedRefs := addUnmodifiedRefs(repoPath)

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
						},
						{
							Redactions: [][]byte{[]byte("hunter\n2")},
						},
					},
					repoPath:     repoPath,
					expectedRefs: unmodifiedRefs,
					expectedErr:  structerr.NewInvalidArgument("redaction pattern contains newline"),
				}
			},
		},
		{
			desc: "redaction pattern with escaped newline",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				unmodifiedRefs := addUnmodifiedRefs(repoPath)

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
						},
						{
							Redactions: [][]byte{[]byte("hunter\\n2")},
						},
					},
					repoPath:         repoPath,
					expectedRefs:     unmodifiedRefs,
					expectedResponse: &gitalypb.RewriteHistoryResponse{},
				}
			},
		},
		{
			desc: "remove non-existent oid",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				unmodifiedRefs := addUnmodifiedRefs(repoPath)

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
						},
						{
							Blobs: []string{strings.Repeat("a", gittest.DefaultObjectHash.EncodedLen())},
						},
					},
					repoPath:         repoPath,
					expectedRefs:     unmodifiedRefs,
					expectedResponse: &gitalypb.RewriteHistoryResponse{},
				}
			},
		},
		{
			desc: "remove blobs",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				unmodifiedRefs := addUnmodifiedRefs(repoPath)

				blobToRemove := gittest.WriteBlob(t, cfg, repoPath, []byte("big blob"))
				_ = gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "remove-me", Mode: "100644", OID: blobToRemove},
					gittest.TreeEntry{Path: "a-file", Mode: "100644", Content: "foobar"},
				))
				updatedCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a-file", Mode: "100644", Content: "foobar"},
				))

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
						},
						{
							Blobs: []string{blobToRemove.String()},
						},
					},
					repoPath: repoPath,
					expectedRefs: append(unmodifiedRefs, []git.Reference{
						{
							Name:   "refs/heads/main",
							Target: updatedCommit.String(),
						},
					}...),
					expectedResponse: &gitalypb.RewriteHistoryResponse{},
				}
			},
		},
		{
			desc: "redact blobs",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				unmodifiedRefs := addUnmodifiedRefs(repoPath)

				_ = gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "redact-me", Mode: "100644", Content: "my password is hunter2"},
				))
				updatedCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "redact-me", Mode: "100644", Content: "my password is ***REMOVED***"},
				))

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
						},
						{
							Redactions: [][]byte{
								[]byte("hunter2"),
							},
						},
					},
					repoPath: repoPath,
					expectedRefs: append(unmodifiedRefs, []git.Reference{
						{
							Name:   "refs/heads/main",
							Target: updatedCommit.String(),
						},
					}...),
					expectedResponse: &gitalypb.RewriteHistoryResponse{},
				}
			},
		},
		{
			desc: "multiple requests",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				unmodifiedRefs := addUnmodifiedRefs(repoPath)

				blobToRemove := gittest.WriteBlob(t, cfg, repoPath, []byte("big blob"))
				_ = gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "remove-me", Mode: "100644", OID: blobToRemove},
					gittest.TreeEntry{Path: "redact-me", Mode: "100644", Content: "my password is hunter2"},
				))
				updatedCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "redact-me", Mode: "100644", Content: "my password is ***REMOVED***"},
				))

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repoProto,
						},
						{
							Blobs: []string{blobToRemove.String()},
						},
						{
							Redactions: [][]byte{[]byte("hunter2")},
						},
					},
					repoPath: repoPath,
					expectedRefs: append(unmodifiedRefs, []git.Reference{
						{
							Name:   "refs/heads/main",
							Target: updatedCommit.String(),
						},
					}...),
					expectedResponse: &gitalypb.RewriteHistoryResponse{},
				}
			},
		},
		{
			desc: "empty branch deleted",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				unmodifiedRefs := addUnmodifiedRefs(repoPath)

				blobToRemove := gittest.WriteBlob(t, cfg, repoPath, []byte("big blob"))
				_ = gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "remove-me", Mode: "100644", OID: blobToRemove},
				))

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
						},
						{
							Blobs: []string{blobToRemove.String()},
						},
					},
					repoPath:         repoPath,
					expectedRefs:     unmodifiedRefs,
					expectedResponse: &gitalypb.RewriteHistoryResponse{},
				}
			},
		},
		{
			desc: "tag updated",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				unmodifiedRefs := addUnmodifiedRefs(repoPath)

				blobToRemove := gittest.WriteBlob(t, cfg, repoPath, []byte("big blob"))
				commit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "remove-me", Mode: "100644", OID: blobToRemove},
					gittest.TreeEntry{Path: "a-file", Mode: "100644", Content: "foobar"},
				))
				_ = gittest.WriteTag(t, cfg, repoPath, "updated-tag", commit.Revision())

				updatedCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a-file", Mode: "100644", Content: "foobar"},
				))

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
						},
						{
							Blobs: []string{blobToRemove.String()},
						},
					},
					repoPath: repoPath,
					expectedRefs: append(unmodifiedRefs, []git.Reference{
						{
							Name:   "refs/tags/updated-tag",
							Target: updatedCommit.String(),
						},
					}...),
					expectedResponse: &gitalypb.RewriteHistoryResponse{},
				}
			},
		},
		{
			desc: "empty tag deleted",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				unmodifiedRefs := addUnmodifiedRefs(repoPath)

				blobToRemove := gittest.WriteBlob(t, cfg, repoPath, []byte("big blob"))
				commit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a-file", Mode: "100644", OID: blobToRemove},
				))
				_ = gittest.WriteTag(t, cfg, repoPath, "deleted-tag", commit.Revision(), gittest.WriteTagConfig{
					Message: "annotated",
				})

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
						},
						{
							Blobs: []string{blobToRemove.String()},
						},
					},
					repoPath:         repoPath,
					expectedRefs:     unmodifiedRefs,
					expectedResponse: &gitalypb.RewriteHistoryResponse{},
				}
			},
		},
		{
			desc: "remove blob in pool repo",
			setup: func(t *testing.T) setupData {
				testhelper.SkipWithWAL(t, `
Object pools are not yet supported with transaction management.`)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				unmodifiedRefs := addUnmodifiedRefs(repoPath)

				poolBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("pool blob"))
				parentCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "remove-me", Mode: "100644", OID: poolBlob},
						gittest.TreeEntry{Path: "other-pool-file", Mode: "100644", Content: "pool blob to retain"},
					),
					gittest.WithBranch("main"),
				)

				gittest.CreateObjectPool(t, ctx, cfg, repo, gittest.CreateObjectPoolConfig{
					LinkRepositoryToObjectPool: true,
				})

				_ = gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "remove-me", Mode: "100644", OID: poolBlob},
						gittest.TreeEntry{Path: "a-file", Mode: "100644", Content: "local blob"},
					),
					gittest.WithParents(parentCommit),
					gittest.WithBranch("main"),
				)

				updatedParent := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "other-pool-file", Mode: "100644", Content: "pool blob to retain"},
					),
				)
				updatedCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "a-file", Mode: "100644", Content: "local blob"},
					),
					gittest.WithParents(updatedParent),
				)

				return setupData{
					requests: []*gitalypb.RewriteHistoryRequest{
						{
							Repository: repo,
						},
						{
							Blobs: []string{poolBlob.String()},
						},
					},
					repoPath: repoPath,
					expectedRefs: append(unmodifiedRefs, []git.Reference{
						{
							Name:   "refs/heads/main",
							Target: updatedCommit.String(),
						},
					}...),
					expectedResponse: &gitalypb.RewriteHistoryResponse{},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			stream, err := client.RewriteHistory(ctx)
			require.NoError(t, err)

			for _, request := range setup.requests {
				require.NoError(t, stream.Send(request))
			}

			response, err := stream.CloseAndRecv()
			testhelper.RequireGrpcError(t, setup.expectedErr, err)
			testhelper.ProtoEqual(t, setup.expectedResponse, response)

			if setup.repoPath != "" {
				refs := gittest.GetReferences(t, cfg, setup.repoPath)
				require.ElementsMatch(t, refs, setup.expectedRefs)
			}
		})
	}
}

func TestRewriteHistory_SHA256(t *testing.T) {
	t.Parallel()

	if !gittest.ObjectHashIsSHA256() {
		t.Skip("test is not compatible with SHA1")
	}

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)
	cfg.SocketPath = runCleanupServiceServer(t, cfg)

	client, conn := newCleanupServiceClient(t, cfg.SocketPath)
	t.Cleanup(func() { conn.Close() })

	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	stream, err := client.RewriteHistory(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&gitalypb.RewriteHistoryRequest{
		Repository: repo,
		Redactions: [][]byte{[]byte("hunter2")},
	}))

	response, err := stream.CloseAndRecv()
	require.Nil(t, response)
	testhelper.RequireGrpcError(t, structerr.NewInvalidArgument("git-filter-repo does not support repositories using the SHA256 object format"), err)
}
