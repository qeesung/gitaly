package commit

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestGetTreeEntries(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	cfg.SocketPath = startTestServices(t, cfg)
	client := newCommitServiceClient(t, cfg.SocketPath)

	type setupData struct {
		request             *gitalypb.GetTreeEntriesRequest
		expectedTreeEntries []*gitalypb.TreeEntry
		expectedCursor      *gitalypb.PaginationCursor
		expectedErr         error
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "path with curly braces exists",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blob := gittest.WriteBlob(t, cfg, repoPath, []byte("test1"))
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(gittest.TreeEntry{
					Path: "issue-46261", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
							{Path: "test1.txt", Mode: "100644", OID: blob},
						})},
						{Path: "{{curly}}", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
							{Path: "test2.txt", Mode: "100644", Content: "test2"},
						})},
					}),
				}))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("issue-46261/folder"),
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       blob.String(),
							Path:      []byte("issue-46261/folder/test1.txt"),
							Type:      0,
							Mode:      0o100644,
							CommitOid: commitID.String(),
							FlatPath:  []byte("issue-46261/folder/test1.txt"),
						},
					},
				}
			},
		},
		{
			desc: "path with curly braces exists and is requested",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blob := gittest.WriteBlob(t, cfg, repoPath, []byte("test2"))
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(gittest.TreeEntry{
					Path: "issue-46261", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
							{Path: "test1.txt", Mode: "100644", Content: "test1"},
						})},
						{Path: "{{curly}}", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
							{Path: "test2.txt", Mode: "100644", OID: blob},
						})},
					}),
				}))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("issue-46261/{{curly}}"),
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       blob.String(),
							Path:      []byte("issue-46261/{{curly}}/test2.txt"),
							Type:      0,
							Mode:      0o100644,
							CommitOid: commitID.String(),
							FlatPath:  []byte("issue-46261/{{curly}}/test2.txt"),
						},
					},
				}
			},
		},
		{
			desc: "repository does not exist",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: &gitalypb.Repository{StorageName: "fake", RelativePath: "path"},
						Revision:   []byte(gittest.DefaultObjectHash.EmptyTreeOID),
						Path:       []byte("folder"),
					},
					expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
						"%w", storage.NewStorageNotFoundError("fake"),
					)),
				}
			},
		},
		{
			desc: "repository is nil",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: nil,
						Revision:   []byte(gittest.DefaultObjectHash.EmptyTreeOID),
						Path:       []byte("folder"),
					},
					expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
				}
			},
		},
		{
			desc: "revision is empty",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   nil,
						Path:       []byte("folder"),
					},
					expectedErr: structerr.NewInvalidArgument("empty revision"),
				}
			},
		},
		{
			desc: "path is empty",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(gittest.TreeEntry{
					Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "test.txt", Mode: "100644", Content: "test"},
					}),
				}))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
					},
					expectedErr: structerr.NewInvalidArgument("empty path").WithDetail(&gitalypb.GetTreeEntriesError{
						Error: &gitalypb.GetTreeEntriesError_Path{
							Path: &gitalypb.PathError{
								ErrorType: gitalypb.PathError_ERROR_TYPE_EMPTY_PATH,
							},
						},
					}),
				}
			},
		},
		{
			desc: "revision is invalid",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte("--output=/meow"),
						Path:       []byte("folder"),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't start with '-'"),
				}
			},
		},
		{
			desc: "non existent token",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(gittest.TreeEntry{
					Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "test.txt", Mode: "100644", Content: "test"},
					}),
				}))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("folder"),
						PaginationParams: &gitalypb.PaginationParameter{
							PageToken: "non-existent",
						},
					},
					expectedErr: status.Error(codes.Internal, "could not find starting OID: non-existent"),
				}
			},
		},
		{
			desc: "path points to a file",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(gittest.TreeEntry{
						Mode:    "100644",
						Path:    "README.md",
						Content: "something with spaces in between",
					}),
				)

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID.String()),
						Path:       []byte("README.md"),
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewInvalidArgument("path not treeish").WithDetail(&gitalypb.GetTreeEntriesError{
							Error: &gitalypb.GetTreeEntriesError_ResolveTree{
								ResolveTree: &gitalypb.ResolveRevisionError{
									Revision: []byte(commitID),
								},
							},
						}),
						structerr.MetadataItem{Key: "path", Value: "README.md"},
						structerr.MetadataItem{Key: "revision", Value: commitID},
					),
				}
			},
		},
		{
			desc: "path points to a file plus recursive",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(gittest.TreeEntry{
						Mode:    "100644",
						Path:    "README.md",
						Content: "something with spaces in between",
					}),
				)

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID.String()),
						Path:       []byte("README.md"),
						Recursive:  true,
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewInvalidArgument("path not treeish").WithDetail(&gitalypb.GetTreeEntriesError{
							Error: &gitalypb.GetTreeEntriesError_ResolveTree{
								ResolveTree: &gitalypb.ResolveRevisionError{
									Revision: []byte(commitID),
								},
							},
						}),
						structerr.MetadataItem{Key: "path", Value: "README.md"},
						structerr.MetadataItem{Key: "revision", Value: commitID},
					),
				}
			},
		},
		{
			desc: "path resolves outside the repo",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(gittest.TreeEntry{
						Mode:    "100644",
						Path:    "README.md",
						Content: "something with spaces in between",
					}),
				)

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID.String()),
						Path:       []byte("./.."),
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewInvalidArgument("invalid revision or path").WithDetail(&gitalypb.GetTreeEntriesError{
							Error: &gitalypb.GetTreeEntriesError_ResolveTree{
								ResolveTree: &gitalypb.ResolveRevisionError{
									Revision: []byte(commitID),
								},
							},
						}),
						structerr.MetadataItem{Key: "path", Value: "./.."},
						structerr.MetadataItem{Key: "revision", Value: commitID},
					),
				}
			},
		},
		{
			desc: "path contains relative path syntax ..",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(gittest.TreeEntry{
					Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "test.txt", Mode: "100644", Content: "test"},
					}),
				}))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID.String()),
						Path:       []byte("./folder/.."),
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewInvalidArgument("invalid revision or path").WithDetail(&gitalypb.GetTreeEntriesError{
							Error: &gitalypb.GetTreeEntriesError_ResolveTree{
								ResolveTree: &gitalypb.ResolveRevisionError{
									Revision: []byte(commitID),
								},
							},
						}),
						structerr.MetadataItem{Key: "path", Value: "./folder/.."},
						structerr.MetadataItem{Key: "revision", Value: commitID},
					),
				}
			},
		},
		{
			desc: "path contains relative path syntax ./",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(gittest.TreeEntry{
					Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "test.txt", Mode: "100644", Content: "test"},
					}),
				}))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID.String()),
						Path:       []byte("./folder/test.txt"),
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewInvalidArgument("invalid revision or path").WithDetail(&gitalypb.GetTreeEntriesError{
							Error: &gitalypb.GetTreeEntriesError_ResolveTree{
								ResolveTree: &gitalypb.ResolveRevisionError{
									Revision: []byte(commitID),
								},
							},
						}),
						structerr.MetadataItem{Key: "path", Value: "./folder/test.txt"},
						structerr.MetadataItem{Key: "revision", Value: commitID},
					),
				}
			},
		},
		{
			desc: "path with .. in request raises no errors",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "test.txt", Mode: "100644", OID: blobID},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(gittest.TreeEntry{
					OID:  treeID,
					Mode: "040000",
					Path: "a..b",
				}))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID.String()),
						Path:       []byte("a..b"),
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       blobID.String(),
							Path:      []byte("a..b/test.txt"),
							Mode:      0o100644,
							CommitOid: commitID.String(),
							FlatPath:  []byte("a..b/test.txt"),
						},
					},
				}
			},
		},
		{
			desc: "path is .",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "test.txt", Mode: "100644", Content: "test"},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(gittest.TreeEntry{
					OID:  treeID,
					Mode: "040000",
					Path: "folder",
				}))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID.String()),
						// when path is ".", we resolve it to ""
						Path: []byte("."),
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       treeID.String(),
							Path:      []byte("folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
							FlatPath:  []byte("folder"),
						},
					},
				}
			},
		},
		{
			desc: "absolute path is used",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "test.txt", Mode: "100644", Content: "test"},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(gittest.TreeEntry{
					OID:  treeID,
					Mode: "040000",
					Path: "folder",
				}))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID.String()),
						Path:       []byte(repoPath + "folder"),
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewInvalidArgument("invalid revision or path").WithDetail(&gitalypb.GetTreeEntriesError{
							Error: &gitalypb.GetTreeEntriesError_ResolveTree{
								ResolveTree: &gitalypb.ResolveRevisionError{
									Revision: []byte(commitID),
								},
							},
						}),
						structerr.MetadataItem{Key: "path", Value: repoPath + "folder"},
						structerr.MetadataItem{Key: "revision", Value: commitID},
					),
				}
			},
		},
		{
			desc: "deeply nested flat path",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				nestingLevel := 12
				require.Greater(t, nestingLevel, defaultFlatTreeRecursion, "sanity check: construct folder deeper than default recursion value")

				// We create a tree structure that is one deeper than the flat-tree recursion limit.
				var treeIDs []git.ObjectID
				for i := nestingLevel; i >= 0; i-- {
					var treeEntry gittest.TreeEntry
					if len(treeIDs) == 0 {
						treeEntry = gittest.TreeEntry{Path: ".gitkeep", Mode: "100644", Content: "something"}
					} else {
						// We use a numbered directory name to make it easier to see when things get
						// truncated.
						treeEntry = gittest.TreeEntry{Path: strconv.Itoa(i), Mode: "040000", OID: treeIDs[len(treeIDs)-1]}
					}

					treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{treeEntry})
					treeIDs = append(treeIDs, treeID)
				}
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTree(treeIDs[len(treeIDs)-1]))

				return setupData{
					// We make a non-recursive request which tries to fetch tree entrie for the tree structure
					// we have created above. This should return a single entry, which is the directory we're
					// requesting.
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("0"),
						Recursive:  false,
					},
					// We know that there is a directory "1/2/3/4/5/6/7/8/9/10/11/12", but here we only get
					// "1/2/3/4/5/6/7/8/9/10/11" as flat path. This proves that FlatPath recursion is bounded,
					// which is the point of this test.
					expectedTreeEntries: []*gitalypb.TreeEntry{{
						Oid:       treeIDs[nestingLevel-2].String(),
						Path:      []byte("0/1"),
						FlatPath:  []byte("0/1/2/3/4/5/6/7/8/9/10"),
						Type:      gitalypb.TreeEntry_TREE,
						Mode:      0o40000,
						CommitOid: commitID.String(),
					}},
				}
			},
		},
		{
			desc: "with root path but only files in repo",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				fileOID := gittest.WriteBlob(t, cfg, repoPath, []byte("file"))
				file2OID := gittest.WriteBlob(t, cfg, repoPath, []byte("file2"))

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: file2OID, Mode: "100644", Path: "bar"},
					gittest.TreeEntry{OID: fileOID, Mode: "100644", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("."),
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       file2OID.String(),
							Path:      []byte("bar"),
							Type:      gitalypb.TreeEntry_BLOB,
							Mode:      0o100644,
							CommitOid: commitID.String(),
							FlatPath:  []byte("bar"),
						},
						{
							Oid:       fileOID.String(),
							Path:      []byte("foo"),
							Type:      gitalypb.TreeEntry_BLOB,
							Mode:      0o100644,
							CommitOid: commitID.String(),
							FlatPath:  []byte("foo"),
						},
					},
				}
			},
		},
		{
			desc: "with root path and disabled flat path but only files in repo",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				fileOID := gittest.WriteBlob(t, cfg, repoPath, []byte("file"))
				file2OID := gittest.WriteBlob(t, cfg, repoPath, []byte("file2"))

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: file2OID, Mode: "100644", Path: "bar"},
					gittest.TreeEntry{OID: fileOID, Mode: "100644", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository:    repo,
						Revision:      []byte(commitID),
						Path:          []byte("."),
						SkipFlatPaths: true,
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       file2OID.String(),
							Path:      []byte("bar"),
							Type:      gitalypb.TreeEntry_BLOB,
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
						{
							Oid:       fileOID.String(),
							Path:      []byte("foo"),
							Type:      gitalypb.TreeEntry_BLOB,
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "with root path and repo with folders",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "test.txt", Mode: "100644", Content: "test"},
					})},
				})

				folder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder2", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "folder3", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
							{Path: "test2.txt", Mode: "100644", Content: "test2"},
						})},
					})},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folder2OID, Mode: "040000", Path: "bar"},
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("."),
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       folder2OID.String(),
							Path:      []byte("bar"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
							FlatPath:  []byte("bar/folder2/folder3"),
						},
						{
							Oid:       folderOID.String(),
							Path:      []byte("foo"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
							FlatPath:  []byte("foo/folder"),
						},
					},
				}
			},
		},
		{
			desc: "with specific folder",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				subFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "test.txt", Mode: "100644", Content: "test"},
				})
				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolderOID},
				})

				folder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder2", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "folder3", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
							{Path: "test2.txt", Mode: "100644", Content: "test2"},
						})},
					})},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folder2OID, Mode: "040000", Path: "bar"},
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("foo"),
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       subFolderOID.String(),
							Path:      []byte("foo/folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
							FlatPath:  []byte("foo/folder"),
						},
					},
				}
			},
		},
		{
			desc: "with specific folder and disabled flatpath",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				subFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "test.txt", Mode: "100644", Content: "test"},
				})
				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolderOID},
				})

				folder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder2", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "folder3", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
							{Path: "test2.txt", Mode: "100644", Content: "test2"},
						})},
					})},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folder2OID, Mode: "040000", Path: "bar"},
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository:    repo,
						Revision:      []byte(commitID),
						Path:          []byte("foo"),
						SkipFlatPaths: true,
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       subFolderOID.String(),
							Path:      []byte("foo/folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "with recursive",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobOID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blobOID, Mode: "100644", Path: "test"},
				})
				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolderOID},
				})

				blob2OID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subSubFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blob2OID, Mode: "100644", Path: "test"},
				})
				subFolder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: subSubFolderOID, Mode: "040000", Path: "folder2"},
				})
				folder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolder2OID},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folder2OID, Mode: "040000", Path: "bar"},
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("."),
						Recursive:  true,
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       folder2OID.String(),
							Path:      []byte("bar"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subFolder2OID.String(),
							Path:      []byte("bar/folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subSubFolderOID.String(),
							Path:      []byte("bar/folder/folder2"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       blob2OID.String(),
							Path:      []byte("bar/folder/folder2/test"),
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
						{
							Oid:       folderOID.String(),
							Path:      []byte("foo"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subFolderOID.String(),
							Path:      []byte("foo/folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       blobOID.String(),
							Path:      []byte("foo/folder/test"),
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "with non-existent path",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "test.txt", Mode: "100644", Content: "test"},
					})},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("does-not-exist"),
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewInvalidArgument("invalid revision or path").WithDetail(&gitalypb.GetTreeEntriesError{
							Error: &gitalypb.GetTreeEntriesError_ResolveTree{
								ResolveTree: &gitalypb.ResolveRevisionError{
									Revision: []byte(commitID),
								},
							},
						}),
						structerr.MetadataItem{Key: "path", Value: "does-not-exist"},
						structerr.MetadataItem{Key: "revision", Value: commitID},
					),
				}
			},
		},
		{
			desc: "with non-existent path plus recursive",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "test.txt", Mode: "100644", Content: "test"},
					})},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("does-not-exist"),
						Recursive:  true,
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewNotFound("invalid revision or path").WithDetail(&gitalypb.GetTreeEntriesError{
							Error: &gitalypb.GetTreeEntriesError_ResolveTree{
								ResolveTree: &gitalypb.ResolveRevisionError{
									Revision: []byte(commitID),
								},
							},
						}),
						structerr.MetadataItem{Key: "path", Value: "does-not-exist"},
						structerr.MetadataItem{Key: "revision", Value: commitID},
					),
				}
			},
		},
		{
			desc: "with non-existent revision",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "test.txt", Mode: "100644", Content: "test"},
					})},
				})

				gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte("does-not-exist"),
						Path:       []byte("."),
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewInvalidArgument("invalid revision or path").WithDetail(&gitalypb.GetTreeEntriesError{
							Error: &gitalypb.GetTreeEntriesError_ResolveTree{
								ResolveTree: &gitalypb.ResolveRevisionError{
									Revision: []byte("does-not-exist"),
								},
							},
						}),
						structerr.MetadataItem{
							Key:   "path",
							Value: "",
						},
						structerr.MetadataItem{Key: "revision", Value: "does-not-exist"},
					),
				}
			},
		},
		{
			desc: "with non-existent revision plus recursive",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
						{Path: "test.txt", Mode: "100644", Content: "test"},
					})},
				})

				gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte("does-not-exist"),
						Path:       []byte("."),
						Recursive:  true,
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewNotFound("invalid revision or path").WithDetail(&gitalypb.GetTreeEntriesError{
							Error: &gitalypb.GetTreeEntriesError_ResolveTree{
								ResolveTree: &gitalypb.ResolveRevisionError{
									Revision: []byte("does-not-exist"),
								},
							},
						}),
						structerr.MetadataItem{Key: "path", Value: ""},
						structerr.MetadataItem{Key: "revision", Value: "does-not-exist"},
					),
				}
			},
		},
		{
			desc: "sorted by trees first",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobOID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blobOID, Mode: "100644", Path: "test"},
				})
				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolderOID},
				})

				blob2OID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subSubFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blob2OID, Mode: "100644", Path: "test"},
				})
				subFolder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: subSubFolderOID, Mode: "040000", Path: "folder2"},
				})
				folder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolder2OID},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folder2OID, Mode: "040000", Path: "bar"},
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("."),
						Recursive:  true,
						Sort:       gitalypb.GetTreeEntriesRequest_TREES_FIRST,
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       folder2OID.String(),
							Path:      []byte("bar"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subFolder2OID.String(),
							Path:      []byte("bar/folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subSubFolderOID.String(),
							Path:      []byte("bar/folder/folder2"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       folderOID.String(),
							Path:      []byte("foo"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subFolderOID.String(),
							Path:      []byte("foo/folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       blob2OID.String(),
							Path:      []byte("bar/folder/folder2/test"),
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
						{
							Oid:       blobOID.String(),
							Path:      []byte("foo/folder/test"),
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "ensure page token is for the same tree entry",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				// Initial tree structure
				blobOID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				treeOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blobOID, Mode: "100644", Path: "test"},
				})
				initialCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: treeOID, Mode: "040000", Path: "folder"},
				))

				// New tree structure
				newBlobOID := gittest.WriteBlob(t, cfg, repoPath, []byte("new_test"))
				newTreeOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: newBlobOID, Mode: "100644", Path: "new_test"},
				})
				newCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: newTreeOID, Mode: "040000", Path: "folder"},
				))

				stream, err := client.GetTreeEntries(ctx, &gitalypb.GetTreeEntriesRequest{
					Repository: repo,
					Revision:   []byte(initialCommitID),
					Path:       []byte("."),
					Recursive:  true,
					PaginationParams: &gitalypb.PaginationParameter{
						Limit: 1,
					},
				})
				require.NoError(t, err)

				var resp *gitalypb.GetTreeEntriesResponse
				resp, err = stream.Recv()
				require.NoError(t, err)
				require.NotNil(t, resp.PaginationCursor)
				require.NotEmpty(t, resp.PaginationCursor.NextCursor)
				initialPageToken := resp.PaginationCursor.NextCursor

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(newCommitID),
						Path:       []byte("."),
						Recursive:  true,
						PaginationParams: &gitalypb.PaginationParameter{
							PageToken: initialPageToken,
							Limit:     1,
						},
					},
					expectedErr: status.Error(codes.Internal, "could not find starting OID: folder"),
				}
			},
		},
		{
			desc: "sorted by trees first and paginated",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobOID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blobOID, Mode: "100644", Path: "test"},
				})
				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolderOID},
				})

				blob2OID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subSubFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blob2OID, Mode: "100644", Path: "test"},
				})
				subFolder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: subSubFolderOID, Mode: "040000", Path: "folder2"},
				})
				folder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolder2OID},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folder2OID, Mode: "040000", Path: "bar"},
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				expectedTreeEntries := []*gitalypb.TreeEntry{
					{
						Oid:       folder2OID.String(),
						Path:      []byte("bar"),
						Type:      gitalypb.TreeEntry_TREE,
						Mode:      0o40000,
						CommitOid: commitID.String(),
					},
					{
						Oid:       subFolder2OID.String(),
						Path:      []byte("bar/folder"),
						Type:      gitalypb.TreeEntry_TREE,
						Mode:      0o40000,
						CommitOid: commitID.String(),
					},
					{
						Oid:       subSubFolderOID.String(),
						Path:      []byte("bar/folder/folder2"),
						Type:      gitalypb.TreeEntry_TREE,
						Mode:      0o40000,
						CommitOid: commitID.String(),
					},
				}

				cursor, err := encodePageToken(expectedTreeEntries[2])
				require.NoError(t, err)

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("."),
						Recursive:  true,
						Sort:       gitalypb.GetTreeEntriesRequest_TREES_FIRST,
						PaginationParams: &gitalypb.PaginationParameter{
							Limit: 3,
						},
					},
					expectedTreeEntries: expectedTreeEntries,
					expectedCursor: &gitalypb.PaginationCursor{
						NextCursor: cursor,
					},
				}
			},
		},
		{
			desc: "sorted by trees first and paginated with token",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobOID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blobOID, Mode: "100644", Path: "test"},
				})
				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolderOID},
				})

				blob2OID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subSubFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blob2OID, Mode: "100644", Path: "test"},
				})
				subFolder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: subSubFolderOID, Mode: "040000", Path: "folder2"},
				})
				folder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolder2OID},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folder2OID, Mode: "040000", Path: "bar"},
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("."),
						Recursive:  true,
						Sort:       gitalypb.GetTreeEntriesRequest_TREES_FIRST,
						PaginationParams: &gitalypb.PaginationParameter{
							PageToken: folderOID.String(),
							Limit:     3,
						},
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       subFolderOID.String(),
							Path:      []byte("foo/folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       blob2OID.String(),
							Path:      []byte("bar/folder/folder2/test"),
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
						{
							Oid:       blobOID.String(),
							Path:      []byte("foo/folder/test"),
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "sorted by trees first with high pagination limit",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobOID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blobOID, Mode: "100644", Path: "test"},
				})
				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolderOID},
				})

				blob2OID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subSubFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blob2OID, Mode: "100644", Path: "test"},
				})
				subFolder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: subSubFolderOID, Mode: "040000", Path: "folder2"},
				})
				folder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolder2OID},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folder2OID, Mode: "040000", Path: "bar"},
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("."),
						Recursive:  true,
						Sort:       gitalypb.GetTreeEntriesRequest_TREES_FIRST,
						PaginationParams: &gitalypb.PaginationParameter{
							Limit: 100,
						},
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       folder2OID.String(),
							Path:      []byte("bar"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subFolder2OID.String(),
							Path:      []byte("bar/folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subSubFolderOID.String(),
							Path:      []byte("bar/folder/folder2"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       folderOID.String(),
							Path:      []byte("foo"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subFolderOID.String(),
							Path:      []byte("foo/folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       blob2OID.String(),
							Path:      []byte("bar/folder/folder2/test"),
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
						{
							Oid:       blobOID.String(),
							Path:      []byte("foo/folder/test"),
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "sorted by trees first with 0 pagination limit",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobOID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blobOID, Mode: "100644", Path: "test"},
				})
				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolderOID},
				})

				blob2OID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subSubFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blob2OID, Mode: "100644", Path: "test"},
				})
				subFolder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: subSubFolderOID, Mode: "040000", Path: "folder2"},
				})
				folder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolder2OID},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folder2OID, Mode: "040000", Path: "bar"},
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("."),
						Recursive:  true,
						Sort:       gitalypb.GetTreeEntriesRequest_TREES_FIRST,
						PaginationParams: &gitalypb.PaginationParameter{
							Limit: 0,
						},
					},
				}
			},
		},
		{
			desc: "sorted by trees first with -1 pagination limit",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobOID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blobOID, Mode: "100644", Path: "test"},
				})
				folderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolderOID},
				})

				blob2OID := gittest.WriteBlob(t, cfg, repoPath, []byte("test"))
				subSubFolderOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: blob2OID, Mode: "100644", Path: "test"},
				})
				subFolder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{OID: subSubFolderOID, Mode: "040000", Path: "folder2"},
				})
				folder2OID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "folder", Mode: "040000", OID: subFolder2OID},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{OID: folder2OID, Mode: "040000", Path: "bar"},
					gittest.TreeEntry{OID: folderOID, Mode: "040000", Path: "foo"},
				))

				return setupData{
					request: &gitalypb.GetTreeEntriesRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Path:       []byte("."),
						Recursive:  true,
						Sort:       gitalypb.GetTreeEntriesRequest_TREES_FIRST,
						PaginationParams: &gitalypb.PaginationParameter{
							Limit: -1,
						},
					},
					expectedTreeEntries: []*gitalypb.TreeEntry{
						{
							Oid:       folder2OID.String(),
							Path:      []byte("bar"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subFolder2OID.String(),
							Path:      []byte("bar/folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subSubFolderOID.String(),
							Path:      []byte("bar/folder/folder2"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       folderOID.String(),
							Path:      []byte("foo"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       subFolderOID.String(),
							Path:      []byte("foo/folder"),
							Type:      gitalypb.TreeEntry_TREE,
							Mode:      0o40000,
							CommitOid: commitID.String(),
						},
						{
							Oid:       blob2OID.String(),
							Path:      []byte("bar/folder/folder2/test"),
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
						{
							Oid:       blobOID.String(),
							Path:      []byte("foo/folder/test"),
							Mode:      0o100644,
							CommitOid: commitID.String(),
						},
					},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			data := tc.setup(t)

			c, err := client.GetTreeEntries(ctx, data.request)
			require.NoError(t, err)

			fetchedEntries, cursor := getTreeEntriesFromTreeEntryClient(t, c, data.expectedErr)
			testhelper.ProtoEqual(t, data.expectedTreeEntries, fetchedEntries)
			if data.expectedCursor != nil || cursor.GetNextCursor() != "" {
				testhelper.ProtoEqual(t, data.expectedCursor, cursor)
			}
		})
	}
}

func BenchmarkGetTreeEntries(b *testing.B) {
	ctx := testhelper.Context(b)
	cfg, client := setupCommitService(b, ctx)

	repo, repoPath := gittest.CreateRepository(b, ctx, cfg)
	commitID := populateRepoWithTreesBlobs(b, repoPath, cfg, 20)

	for _, tc := range []struct {
		desc            string
		request         *gitalypb.GetTreeEntriesRequest
		expectedEntries int
	}{
		{
			desc: "recursive from root",
			request: &gitalypb.GetTreeEntriesRequest{
				Repository: repo,
				Revision:   []byte(commitID),
				Path:       []byte("."),
				Recursive:  true,
			},
			expectedEntries: 40419,
		},
		{
			desc: "non-recursive from root",
			request: &gitalypb.GetTreeEntriesRequest{
				Repository: repo,
				Revision:   []byte(commitID),
				Path:       []byte("."),
				Recursive:  false,
			},
			expectedEntries: 21,
		},
		{
			desc: "recursive from subdirectory",
			request: &gitalypb.GetTreeEntriesRequest{
				Repository: repo,
				Revision:   []byte(commitID),
				Path:       []byte("folder1/folder2/folder3"),
				Recursive:  true,
			},
			expectedEntries: 34356,
		},
	} {
		b.Run(tc.desc, func(b *testing.B) {
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				stream, err := client.GetTreeEntries(ctx, tc.request)
				require.NoError(b, err)

				entriesReceived, err := testhelper.ReceiveAndFold(stream.Recv, func(result int, response *gitalypb.GetTreeEntriesResponse) int {
					return result + len(response.Entries)
				})
				require.NoError(b, err)
				require.Equal(b, tc.expectedEntries, entriesReceived)
			}
		})
	}
}

func getTreeEntriesFromTreeEntryClient(t *testing.T, client gitalypb.CommitService_GetTreeEntriesClient, expectedError error) ([]*gitalypb.TreeEntry, *gitalypb.PaginationCursor) {
	t.Helper()

	var entries []*gitalypb.TreeEntry
	var cursor *gitalypb.PaginationCursor
	firstEntryReceived := false

	for {
		resp, err := client.Recv()

		if expectedError == nil {
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)
			entries = append(entries, resp.Entries...)

			if !firstEntryReceived {
				cursor = resp.PaginationCursor
				firstEntryReceived = true
			} else {
				require.Equal(t, nil, resp.PaginationCursor)
			}
		} else {
			testhelper.RequireGrpcError(t, expectedError, err, protocmp.SortRepeatedFields(&spb.Status{}, "details"))
			break
		}
	}
	return entries, cursor
}

func populateRepoWithTreesBlobs(tb testing.TB, repoPath string, cfg config.Cfg, depth int) git.ObjectID {
	var treeOID git.ObjectID
	treeCount, blobCount := 20, 100

	writeTree := func(path string) gittest.TreeEntry {
		entries := []gittest.TreeEntry{}

		for i := 0; i < blobCount; i++ {
			entries = append(entries, gittest.TreeEntry{
				OID: gittest.WriteBlob(tb, cfg, repoPath, []byte(fmt.Sprintf("%d", i))), Mode: "100644", Path: fmt.Sprintf("%d", i),
			})
		}

		return gittest.TreeEntry{
			OID:  gittest.WriteTree(tb, cfg, repoPath, entries),
			Mode: "040000",
			Path: path,
		}
	}

	for i := depth; i > 0; i-- {
		entries := []gittest.TreeEntry{}

		for j := 0; j < treeCount; j++ {
			entries = append(entries, writeTree(fmt.Sprintf("%d", j)))
		}

		if treeOID != "" {
			entries = append(entries, gittest.TreeEntry{
				OID:  treeOID,
				Mode: "040000",
				Path: fmt.Sprintf("folder%d", i),
			})
		}

		treeOID = gittest.WriteTree(tb, cfg, repoPath, entries)
	}

	return gittest.WriteCommit(tb, cfg, repoPath, gittest.WithTree(treeOID))
}
