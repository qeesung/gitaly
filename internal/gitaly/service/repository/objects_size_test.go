package repository

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestObjectsSize(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, client := setupRepositoryService(t)

	type setupData struct {
		requests         []*gitalypb.ObjectsSizeRequest
		expectedErr      error
		expectedResponse *gitalypb.ObjectsSizeResponse
	}

	looseObjectsSize := func(t *testing.T, repoPath string, objectIDs ...git.ObjectID) uint64 {
		t.Helper()

		var totalSize uint64
		for _, oid := range objectIDs {
			hex := oid.String()

			stat, err := os.Stat(filepath.Join(repoPath, "objects", hex[0:2], hex[2:]))
			require.NoError(t, err)

			totalSize += uint64(stat.Size())
		}

		return totalSize
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "missing requests",
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
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: &gitalypb.Repository{
								StorageName:  cfg.Storages[0].Name,
								RelativePath: repoPath,
							},
							Revisions: [][]byte{
								[]byte("HEAD"),
							},
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
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("1234"))

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte(blobID),
							},
						},
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte(blobID),
							},
						},
					},
					expectedErr: structerr.NewInvalidArgument("subsequent requests must not contain repository"),
				}
			},
		},
		{
			desc: "invalid revision",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte("-HEAD"),
							},
						},
					},
					expectedErr: testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument("validating revision: revision can't start with '-'"),
						"revision", []byte("-HEAD"),
					),
				}
			},
		},
		{
			desc: "revision with newline character",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte("HEAD\n"),
							},
						},
					},
					expectedErr: testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument("validating revision: revision can't contain whitespace"),
						"revision", []byte("HEAD\n"),
					),
				}
			},
		},
		{
			desc: "missing revision",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte("does/not/exist"),
							},
						},
					},
					expectedErr: testhelper.WithInterceptedMetadata(
						structerr.NewNotFound("revision not found"),
						"revision", "does/not/exist",
					),
				}
			},
		},
		{
			desc: "missing revision in subsequent request",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("1234"))

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte(blobID),
							},
						},
						{
							Revisions: [][]byte{},
						},
					},
					expectedErr: structerr.NewInvalidArgument("no revisions specified"),
				}
			},
		},
		{
			desc: "simple object",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("1234"))

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte(blobID),
							},
						},
					},
					expectedResponse: &gitalypb.ObjectsSizeResponse{
						Size: looseObjectsSize(t, repoPath, blobID),
					},
				}
			},
		},
		{
			desc: "object is only accounted for once",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("1234"))

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte(blobID),
								[]byte(blobID),
							},
						},
					},
					expectedResponse: &gitalypb.ObjectsSizeResponse{
						Size: looseObjectsSize(t, repoPath, blobID),
					},
				}
			},
		},
		{
			desc: "reachable objects are walked",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("1234"))
				treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "blob", Mode: "100644", OID: blobID},
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTree(treeID))

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte(commitID),
							},
						},
					},
					expectedResponse: &gitalypb.ObjectsSizeResponse{
						Size: looseObjectsSize(t, repoPath, blobID, treeID, commitID),
					},
				}
			},
		},
		{
			desc: "commits are walked",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				parentBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("1234"))
				parentTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "blob", Mode: "100644", OID: parentBlob},
				})
				parentCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTree(parentTree))

				childBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("12345678"))
				childTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "blob", Mode: "100644", OID: childBlob},
				})
				childCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTree(childTree), gittest.WithParents(parentCommit))

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte(childCommit),
							},
						},
					},
					expectedResponse: &gitalypb.ObjectsSizeResponse{
						Size: looseObjectsSize(t, repoPath,
							parentCommit, parentBlob, parentTree,
							childCommit, childBlob, childTree,
						),
					},
				}
			},
		},
		{
			desc: "tree can be walked",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("1234"))
				treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "blob", Mode: "100644", OID: blobID},
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTree(treeID))

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte(commitID + "^{tree}"),
							},
						},
					},
					expectedResponse: &gitalypb.ObjectsSizeResponse{
						Size: looseObjectsSize(t, repoPath, blobID, treeID),
					},
				}
			},
		},
		{
			desc: "can use --all pseudo-revision",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte("--all"),
							},
						},
					},
					expectedResponse: &gitalypb.ObjectsSizeResponse{
						Size: looseObjectsSize(t, repoPath, commitID),
					},
				}
			},
		},
		{
			desc: "unique objects in an object deduplication network",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				dedupBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("1234"))
				dedupTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "blob", Mode: "100644", OID: dedupBlob},
				})
				dedupCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTree(dedupTree),
					gittest.WithReference("refs/heads/main"),
				)

				gittest.CreateObjectPool(t, ctx, cfg, repo, gittest.CreateObjectPoolConfig{
					LinkRepositoryToObjectPool: true,
				})

				uniqueBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("98765"))
				uniqueTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "blob", Mode: "100644", OID: uniqueBlob},
				})
				uniqueCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTree(uniqueTree),
					gittest.WithParents(dedupCommit),
					gittest.WithReference("refs/heads/main"),
				)

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte("--all"),
								[]byte("--not"),
								[]byte("--alternate-refs"),
							},
						},
					},
					expectedResponse: &gitalypb.ObjectsSizeResponse{
						Size: looseObjectsSize(t, repoPath, uniqueBlob, uniqueTree, uniqueCommit),
					},
				}
			},
		},
		{
			desc: "deduplicated objects in an object deduplication network",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				dedupBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("1234"))
				dedupTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "blob", Mode: "100644", OID: dedupBlob},
				})
				reference := "refs/heads/main"
				dedupCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTree(dedupTree),
					gittest.WithReference(reference),
				)

				_, poolPath := gittest.CreateObjectPool(t, ctx, cfg, repo, gittest.CreateObjectPoolConfig{
					LinkRepositoryToObjectPool: true,
				})

				uniqueBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("98765"))
				uniqueTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "blob", Mode: "100644", OID: uniqueBlob},
				})
				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTree(uniqueTree),
					gittest.WithParents(dedupCommit),
					gittest.WithReference("refs/heads/main"),
				)

				// As we're only interested in objects in the pool, we expect them to exactly match the disk
				// size in the pool. We use rev-list to figure out the size as the objects are loose without
				// transactions and packed with transactions.
				expectedSize, err := strconv.ParseUint(
					text.ChompBytes(
						gittest.Exec(t, cfg, "-C", poolPath, "rev-list", "--disk-usage", "--objects", reference),
					), 10, 64,
				)
				require.NoError(t, err)

				return setupData{
					requests: []*gitalypb.ObjectsSizeRequest{
						{
							Repository: repo,
							Revisions: [][]byte{
								[]byte("--alternate-refs"),
							},
						},
					},
					expectedResponse: &gitalypb.ObjectsSizeResponse{
						Size: expectedSize,
					},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			stream, err := client.ObjectsSize(ctx)
			require.NoError(t, err)

			for _, request := range setup.requests {
				require.NoError(t, stream.Send(request))
			}

			response, err := stream.CloseAndRecv()
			testhelper.RequireGrpcError(t, setup.expectedErr, err)
			testhelper.ProtoEqual(t, setup.expectedResponse, response)
		})
	}
}
