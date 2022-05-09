package commit

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetTreeEntries_curlyBraces(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, repoPath, client := setupCommitServiceWithRepo(ctx, t)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(), gittest.WithTreeEntries(gittest.TreeEntry{
		Path: "issue-46261", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
			{
				Path: "folder", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "test1.txt", Mode: "100644", Content: "test1"},
				}),
			},
			{
				Path: "{{curly}}", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "test2.txt", Mode: "100644", Content: "test2"},
				}),
			},
		}),
	}))

	for _, tc := range []struct {
		desc      string
		revision  []byte
		path      []byte
		recursive bool
		filename  []byte
	}{
		{
			desc:     "with a normal folder",
			revision: []byte("master"),
			path:     []byte("issue-46261/folder"),
			filename: []byte("issue-46261/folder/test1.txt"),
		},
		{
			desc:     "with a folder with curly braces",
			revision: []byte("master"),
			path:     []byte("issue-46261/{{curly}}"),
			filename: []byte("issue-46261/{{curly}}/test2.txt"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			request := &gitalypb.GetTreeEntriesRequest{
				Repository: repo,
				Revision:   []byte(commitID.String()),
				Path:       tc.path,
				Recursive:  tc.recursive,
			}

			c, err := client.GetTreeEntries(ctx, request)
			require.NoError(t, err)

			fetchedEntries, _ := getTreeEntriesFromTreeEntryClient(t, c, nil)
			require.Equal(t, 1, len(fetchedEntries))
			require.Equal(t, tc.filename, fetchedEntries[0].FlatPath)
		})
	}
}

func TestGetTreeEntries_successful(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	commitID := "d25b6d94034242f3930dfcfeb6d8d9aac3583992"
	rootOid := "21bdc8af908562ae485ed46d71dd5426c08b084a"

	_, repo, _, client := setupCommitServiceWithRepo(ctx, t)

	rootEntries := []*gitalypb.TreeEntry{
		{
			Oid:       "fd90a3d2d21d6b4f9bec2c33fb7f49780c55f0d2",
			RootOid:   rootOid,
			Path:      []byte(".DS_Store"),
			FlatPath:  []byte(".DS_Store"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "470ad2fcf1e33798f1afc5781d08e60c40f51e7a",
			RootOid:   rootOid,
			Path:      []byte(".gitignore"),
			FlatPath:  []byte(".gitignore"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "fdaada1754989978413d618ee1fb1c0469d6a664",
			RootOid:   rootOid,
			Path:      []byte(".gitmodules"),
			FlatPath:  []byte(".gitmodules"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "c74175afd117781cbc983664339a0f599b5bb34e",
			RootOid:   rootOid,
			Path:      []byte("CHANGELOG"),
			FlatPath:  []byte("CHANGELOG"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "c1788657b95998a2f177a4f86d68a60f2a80117f",
			RootOid:   rootOid,
			Path:      []byte("CONTRIBUTING.md"),
			FlatPath:  []byte("CONTRIBUTING.md"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "50b27c6518be44c42c4d87966ae2481ce895624c",
			RootOid:   rootOid,
			Path:      []byte("LICENSE"),
			FlatPath:  []byte("LICENSE"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "95d9f0a5e7bb054e9dd3975589b8dfc689e20e88",
			RootOid:   rootOid,
			Path:      []byte("MAINTENANCE.md"),
			FlatPath:  []byte("MAINTENANCE.md"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "bf757025c40c62e6ffa6f11d3819c769a76dbe09",
			RootOid:   rootOid,
			Path:      []byte("PROCESS.md"),
			FlatPath:  []byte("PROCESS.md"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "faaf198af3a36dbf41961466703cc1d47c61d051",
			RootOid:   rootOid,
			Path:      []byte("README.md"),
			FlatPath:  []byte("README.md"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "998707b421c89bd9a3063333f9f728ef3e43d101",
			RootOid:   rootOid,
			Path:      []byte("VERSION"),
			FlatPath:  []byte("VERSION"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "3c122d2b7830eca25235131070602575cf8b41a1",
			RootOid:   rootOid,
			Path:      []byte("encoding"),
			FlatPath:  []byte("encoding"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
		{
			Oid:       "b4a3321157f6e80c42b031ecc9ba79f784c8a557",
			RootOid:   rootOid,
			Path:      []byte("files"),
			FlatPath:  []byte("files"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
		{
			Oid:       "6fd00c6336d6385ef6efe553a29107b35d18d380",
			RootOid:   rootOid,
			Path:      []byte("level-0"),
			FlatPath:  []byte("level-0"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
		{
			Oid:       "409f37c4f05865e4fb208c771485f211a22c4c2d",
			RootOid:   rootOid,
			Path:      []byte("six"),
			FlatPath:  []byte("six"),
			Type:      gitalypb.TreeEntry_COMMIT,
			Mode:      0o160000,
			CommitOid: commitID,
		},
	}

	// Order: Tree, Blob, Submodules
	sortedRootEntries := append(rootEntries[10:13], rootEntries[0:10]...)
	sortedRootEntries = append(sortedRootEntries, rootEntries[13])
	sortedAndPaginated := []*gitalypb.TreeEntry{rootEntries[10], rootEntries[11], rootEntries[12], rootEntries[0]}

	filesDirEntries := []*gitalypb.TreeEntry{
		{
			Oid:       "60d7a906c2fd9e4509aeb1187b98d0ea7ce827c9",
			RootOid:   rootOid,
			Path:      []byte("files/.DS_Store"),
			FlatPath:  []byte("files/.DS_Store"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "2132d150328bd9334cc4e62a16a5d998a7e399b9",
			RootOid:   rootOid,
			Path:      []byte("files/flat"),
			FlatPath:  []byte("files/flat/path/correct"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
		{
			Oid:       "a1e8f8d745cc87e3a9248358d9352bb7f9a0aeba",
			RootOid:   rootOid,
			Path:      []byte("files/html"),
			FlatPath:  []byte("files/html"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
		{
			Oid:       "5e147e3af6740ee83103ec2ecdf846cae696edd1",
			RootOid:   rootOid,
			Path:      []byte("files/images"),
			FlatPath:  []byte("files/images"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
		{
			Oid:       "7853101769f3421725ddc41439c2cd4610e37ad9",
			RootOid:   rootOid,
			Path:      []byte("files/js"),
			FlatPath:  []byte("files/js"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
		{
			Oid:       "fd581c619bf59cfdfa9c8282377bb09c2f897520",
			RootOid:   rootOid,
			Path:      []byte("files/markdown"),
			FlatPath:  []byte("files/markdown"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
		{
			Oid:       "b59dbe4a27371d53e61bf3cb8bef66be53572db0",
			RootOid:   rootOid,
			Path:      []byte("files/ruby"),
			FlatPath:  []byte("files/ruby"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
	}

	recursiveEntries := []*gitalypb.TreeEntry{
		{
			Oid:       "d564d0bc3dd917926892c55e3706cc116d5b165e",
			RootOid:   rootOid,
			Path:      []byte("level-0/level-1-1"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
		{
			Oid:       "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
			RootOid:   rootOid,
			Path:      []byte("level-0/level-1-1/.gitkeep"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
		{
			Oid:       "02366a40d0cde8191e43a8c5b821176c0668522c",
			RootOid:   rootOid,
			Path:      []byte("level-0/level-1-2"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
		{
			Oid:       "d564d0bc3dd917926892c55e3706cc116d5b165e",
			RootOid:   rootOid,
			Path:      []byte("level-0/level-1-2/level-2"),
			Type:      gitalypb.TreeEntry_TREE,
			Mode:      0o40000,
			CommitOid: commitID,
		},
		{
			Oid:       "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
			RootOid:   rootOid,
			Path:      []byte("level-0/level-1-2/level-2/.gitkeep"),
			Type:      gitalypb.TreeEntry_BLOB,
			Mode:      0o100644,
			CommitOid: commitID,
		},
	}

	testCases := []struct {
		description string
		revision    []byte
		path        []byte
		recursive   bool
		sortBy      gitalypb.GetTreeEntriesRequest_SortBy
		entries     []*gitalypb.TreeEntry
		pageToken   string
		pageLimit   int32
		cursor      string
	}{
		{
			description: "with root path",
			revision:    []byte(commitID),
			path:        []byte("."),
			entries:     rootEntries,
		},
		{
			description: "with a folder",
			revision:    []byte(commitID),
			path:        []byte("files"),
			entries:     filesDirEntries,
		},
		{
			description: "with recursive",
			revision:    []byte(commitID),
			path:        []byte("level-0"),
			recursive:   true,
			entries:     recursiveEntries,
		},
		{
			description: "with a file",
			revision:    []byte(commitID),
			path:        []byte(".gitignore"),
			entries:     nil,
		},
		{
			description: "with a non-existing path",
			revision:    []byte(commitID),
			path:        []byte("i-dont/exist"),
			entries:     nil,
		},
		{
			description: "with a non-existing path",
			revision:    []byte(commitID),
			path:        []byte("i-dont/exist"),
			recursive:   true,
			entries:     nil,
		},
		{
			description: "with a non-existing revision, nonrecursive",
			revision:    []byte("this-revision-does-not-exist"),
			path:        []byte("."),
			entries:     nil,
		},
		{
			description: "with a non-existing revision, recursive",
			revision:    []byte("this-revision-does-not-exist"),
			path:        []byte("."),
			entries:     nil,
			recursive:   true,
		},
		{
			description: "with root path and sorted by trees first",
			revision:    []byte(commitID),
			path:        []byte("."),
			entries:     sortedRootEntries,
			sortBy:      gitalypb.GetTreeEntriesRequest_TREES_FIRST,
		},
		{
			description: "with root path and sorted by trees with pagination",
			revision:    []byte(commitID),
			path:        []byte("."),
			entries:     sortedAndPaginated,
			pageLimit:   4,
			sortBy:      gitalypb.GetTreeEntriesRequest_TREES_FIRST,
			cursor:      "fd90a3d2d21d6b4f9bec2c33fb7f49780c55f0d2",
		},
		{
			description: "with pagination parameters",
			revision:    []byte(commitID),
			path:        []byte("."),
			entries:     rootEntries[3:6],
			pageToken:   "fdaada1754989978413d618ee1fb1c0469d6a664",
			pageLimit:   3,
			cursor:      rootEntries[5].Oid,
		},
		{
			description: "with pagination parameters larger than length",
			revision:    []byte(commitID),
			path:        []byte("."),
			entries:     rootEntries[12:],
			pageToken:   "b4a3321157f6e80c42b031ecc9ba79f784c8a557",
			pageLimit:   20,
		},
		{
			description: "with pagination limit of -1",
			revision:    []byte(commitID),
			path:        []byte("."),
			entries:     rootEntries[2:],
			pageToken:   "470ad2fcf1e33798f1afc5781d08e60c40f51e7a",
			pageLimit:   -1,
		},
		{
			description: "with pagination limit of 0",
			revision:    []byte(commitID),
			path:        []byte("."),
			pageToken:   "470ad2fcf1e33798f1afc5781d08e60c40f51e7a",
			pageLimit:   0,
		},
		{
			description: "with a blank pagination token",
			revision:    []byte(commitID),
			path:        []byte("."),
			pageToken:   "",
			entries:     rootEntries[0:2],
			pageLimit:   2,
			cursor:      rootEntries[1].Oid,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.description, func(t *testing.T) {
			request := &gitalypb.GetTreeEntriesRequest{
				Repository: repo,
				Revision:   testCase.revision,
				Path:       testCase.path,
				Recursive:  testCase.recursive,
				Sort:       testCase.sortBy,
			}

			if testCase.pageToken != "" || testCase.pageLimit > 0 {
				request.PaginationParams = &gitalypb.PaginationParameter{
					PageToken: testCase.pageToken,
					Limit:     testCase.pageLimit,
				}
			}

			c, err := client.GetTreeEntries(ctx, request)

			require.NoError(t, err)
			fetchedEntries, cursor := getTreeEntriesFromTreeEntryClient(t, c, nil)
			require.Equal(t, testCase.entries, fetchedEntries)

			if testCase.pageLimit > 0 && len(testCase.entries) < len(rootEntries) {
				require.NotNil(t, cursor)
				require.Equal(t, testCase.cursor, cursor.NextCursor)
			}
		})
	}
}

func TestGetTreeEntries_unsuccessful(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	commitID := "d25b6d94034242f3930dfcfeb6d8d9aac3583992"

	_, repo, _, client := setupCommitServiceWithRepo(ctx, t)

	testCases := []struct {
		description   string
		revision      []byte
		path          []byte
		pageToken     string
		expectedError error
	}{
		{
			description:   "with non-existent token",
			revision:      []byte(commitID),
			path:          []byte("."),
			pageToken:     "non-existent",
			expectedError: status.Error(codes.Unknown, "could not find starting OID: non-existent"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.description, func(t *testing.T) {
			request := &gitalypb.GetTreeEntriesRequest{
				Repository: repo,
				Revision:   testCase.revision,
				Path:       testCase.path,
			}

			if testCase.pageToken != "" {
				request.PaginationParams = &gitalypb.PaginationParameter{
					PageToken: testCase.pageToken,
				}
			}

			c, err := client.GetTreeEntries(ctx, request)
			require.NoError(t, err)

			fetchedEntries, cursor := getTreeEntriesFromTreeEntryClient(t, c, testCase.expectedError)

			require.Empty(t, fetchedEntries)
			require.Nil(t, cursor)
		})
	}
}

func TestGetTreeEntries_deepFlatpath(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, repo, repoPath, client := setupCommitServiceWithRepo(ctx, t)

	nestingLevel := 12
	require.Greater(t, nestingLevel, defaultFlatTreeRecursion, "sanity check: construct folder deeper than default recursion value")

	// We create a tree structure that is one deeper than the flat-tree recursion limit.
	var treeID git.ObjectID
	for i := nestingLevel; i >= 0; i-- {
		var treeEntry gittest.TreeEntry
		if treeID == "" {
			treeEntry = gittest.TreeEntry{Path: ".gitkeep", Mode: "100644", Content: "something"}
		} else {
			// We use a numbered directory name to make it easier to see when things get
			// truncated.
			treeEntry = gittest.TreeEntry{Path: strconv.Itoa(i), Mode: "040000", OID: treeID}
		}

		treeID = gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{treeEntry})
	}
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(), gittest.WithTree(treeID))

	// We make a non-recursive request which tries to fetch tree entrie for the tree structure
	// we have created above. This should return a single entry, which is the directory we're
	// requesting.
	stream, err := client.GetTreeEntries(ctx, &gitalypb.GetTreeEntriesRequest{
		Repository: repo,
		Revision:   []byte(commitID),
		Path:       []byte("0"),
		Recursive:  false,
	})
	require.NoError(t, err)
	treeEntries, _ := getTreeEntriesFromTreeEntryClient(t, stream, nil)

	// We know that there is a directory "1/2/3/4/5/6/7/8/9/10/11/12", but here we only get
	// "1/2/3/4/5/6/7/8/9/10/11" as flat path. This proves that FlatPath recursion is bounded,
	// which is the point of this test.
	require.Equal(t, []*gitalypb.TreeEntry{{
		Oid:       "ba0cae41e396836584a4114feac0b943faf786da",
		RootOid:   treeID.String(),
		Path:      []byte("0/1"),
		FlatPath:  []byte("0/1/2/3/4/5/6/7/8/9/10"),
		Type:      gitalypb.TreeEntry_TREE,
		Mode:      0o40000,
		CommitOid: commitID.String(),
	}}, treeEntries)
}

func TestGetTreeEntries_file(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, repo, repoPath, client := setupCommitServiceWithRepo(ctx, t)

	commitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithTreeEntries(gittest.TreeEntry{
			Mode:    "100644",
			Path:    "README.md",
			Content: "something with spaces in between",
		}),
	)

	// request entries of the tree with single-folder structure on each level
	stream, err := client.GetTreeEntries(ctx, &gitalypb.GetTreeEntriesRequest{
		Repository: repo,
		Revision:   []byte(commitID.String()),
		Path:       []byte("README.md"),
		Recursive:  true,
	})
	require.NoError(t, err)

	// When trying to read a blob, the expectation is that we fail gracefully by just returning
	// nothing.
	entries, err := stream.Recv()
	require.Equal(t, io.EOF, err)
	require.Empty(t, entries)
}

func TestGetTreeEntries_validation(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	_, repo, _, client := setupCommitServiceWithRepo(ctx, t)

	revision := []byte("d42783470dc29fde2cf459eb3199ee1d7e3f3a72")
	path := []byte("a/b/c")

	rpcRequests := []*gitalypb.GetTreeEntriesRequest{
		{Repository: &gitalypb.Repository{StorageName: "fake", RelativePath: "path"}, Revision: revision, Path: path}, // Repository doesn't exist
		{Repository: nil, Revision: revision, Path: path},                                                             // Repository is nil
		{Repository: repo, Revision: nil, Path: path},                                                                 // Revision is empty
		{Repository: repo, Revision: revision},                                                                        // Path is empty
		{Repository: repo, Revision: []byte("--output=/meow"), Path: path},                                            // Revision is invalid
	}

	for _, rpcRequest := range rpcRequests {
		t.Run(fmt.Sprintf("%v", rpcRequest), func(t *testing.T) {
			c, err := client.GetTreeEntries(ctx, rpcRequest)
			require.NoError(t, err)

			err = drainTreeEntriesResponse(c)
			testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
		})
	}
}

func BenchmarkGetTreeEntries(b *testing.B) {
	ctx := testhelper.Context(b)
	cfg, client := setupCommitService(ctx, b)

	repo, _ := gittest.CloneRepo(b, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
		SourceRepo: "benchmark.git",
	})

	for _, tc := range []struct {
		desc            string
		request         *gitalypb.GetTreeEntriesRequest
		expectedEntries int
	}{
		{
			desc: "recursive from root",
			request: &gitalypb.GetTreeEntriesRequest{
				Repository: repo,
				Revision:   []byte("9659e770b131abb4e01d74306819192cd553c258"),
				Path:       []byte("."),
				Recursive:  true,
			},
			expectedEntries: 61027,
		},
		{
			desc: "non-recursive from root",
			request: &gitalypb.GetTreeEntriesRequest{
				Repository: repo,
				Revision:   []byte("9659e770b131abb4e01d74306819192cd553c258"),
				Path:       []byte("."),
				Recursive:  false,
			},
			expectedEntries: 101,
		},
		{
			desc: "recursive from subdirectory",
			request: &gitalypb.GetTreeEntriesRequest{
				Repository: repo,
				Revision:   []byte("9659e770b131abb4e01d74306819192cd553c258"),
				Path:       []byte("lib/gitlab"),
				Recursive:  true,
			},
			expectedEntries: 2642,
		},
	} {
		b.Run(tc.desc, func(b *testing.B) {
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				stream, err := client.GetTreeEntries(ctx, tc.request)
				require.NoError(b, err)

				entriesReceived := 0
				for {
					response, err := stream.Recv()
					if err != nil {
						if errors.Is(err, io.EOF) {
							break
						}
						require.NoError(b, err)
					}

					entriesReceived += len(response.Entries)
				}
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
			if err == io.EOF {
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
			testhelper.RequireGrpcError(t, expectedError, err)
			break
		}
	}
	return entries, cursor
}

func drainTreeEntriesResponse(c gitalypb.CommitService_GetTreeEntriesClient) error {
	var err error
	for err == nil {
		_, err = c.Recv()
	}
	return err
}
