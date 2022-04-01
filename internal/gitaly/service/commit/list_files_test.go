package commit

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

var defaultFiles = [][]byte{
	[]byte(".gitattributes"), []byte(".gitignore"), []byte(".gitmodules"),
	[]byte("CHANGELOG"), []byte("CONTRIBUTING.md"), []byte("Gemfile.zip"),
	[]byte("LICENSE"), []byte("MAINTENANCE.md"), []byte("PROCESS.md"),
	[]byte("README"), []byte("README.md"), []byte("VERSION"),
	[]byte("bar/branch-test.txt"), []byte("custom-highlighting/test.gitlab-custom"),
	[]byte("encoding/feature-1.txt"), []byte("encoding/feature-2.txt"),
	[]byte("encoding/hotfix-1.txt"), []byte("encoding/hotfix-2.txt"),
	[]byte("encoding/iso8859.txt"), []byte("encoding/russian.rb"),
	[]byte("encoding/test.txt"), []byte("encoding/テスト.txt"), []byte("encoding/テスト.xls"),
	[]byte("files/html/500.html"), []byte("files/images/6049019_460s.jpg"),
	[]byte("files/images/logo-black.png"), []byte("files/images/logo-white.png"),
	[]byte("files/images/wm.svg"), []byte("files/js/application.js"),
	[]byte("files/js/commit.coffee"), []byte("files/lfs/lfs_object.iso"),
	[]byte("files/markdown/ruby-style-guide.md"), []byte("files/ruby/popen.rb"),
	[]byte("files/ruby/regex.rb"), []byte("files/ruby/version_info.rb"),
	[]byte("files/whitespace"), []byte("foo/bar/.gitkeep"),
	[]byte("gitaly/file-with-multiple-chunks"), []byte("gitaly/logo-white.png"),
	[]byte("gitaly/mode-file"), []byte("gitaly/mode-file-with-mods"),
	[]byte("gitaly/no-newline-at-the-end"), []byte("gitaly/renamed-file"),
	[]byte("gitaly/renamed-file-with-mods"), []byte("gitaly/symlink-to-be-regular"),
	[]byte("gitaly/tab\tnewline\n file"), []byte("gitaly/テスト.txt"),
	[]byte("with space/README.md"),
}

var defaultOIDs = []string{
	"36814a3da051159a1683479e7a1487120309db8f", "dfaa3f97ca337e20154a98ac9d0be76ddd1fcc82",
	"efd587ccb47caf5f31fc954edb21f0a713d9ecc3", "83a8b398d925877cec468c5d9ebc129129b71e46",
	"49fb42273c4c4487be2e59e732584089249911cc", "8ebd6459371e67b1365d332888238f1e41f2cf2c",
	"e21d87d728e0d4169d647a8eaaf83ffd5890a3d0", "5d9c7c0470bf368d61d9b6cd076300dc9d061f14",
	"9ae30cf7efba20e837c254f887b7520dff4382b1", "3742e48c1108ced3bf45ac633b34b65ac3f2af04",
	"877cee6ab11f9094e1bcdb7f1fd9c0001b572185", "d223b45108b136f9cd9f48bbfcf5de8342375633",
	"93e123ac8a3e6a0b600953d7598af629dec7b735", "2be329bc180e151e5352a5f09ee722aeccdffac6",
	"8af7f880ce38649fc49f66e3f38857bfbec3f0b7", "16ca0b267f82cd2f5ca1157dd162dae98745eab8",
	"0fb47f093f769008049a0b0976ac3fa6d6125033", "4ae6c5e14452a35d04156277ae63e8356eb17cae",
	"b988ffed90cb6a9b7f98a3686a933edb3c5d70c0", "570f8e1dfe8149c1d17002712310d43dfeb43159",
	"7a17968582c21c9153ec24c6a9d5f33592ad9103", "f3064a3aa9c14277483f690250072e987e2c8356",
	"3a26c18b02e843b459732e7ade7ab9a154a1002b", "c84b9e90e4bee2666f5cb3688690d9824d88f3ee",
	"08cf843fd8fe1c50757df0a13fcc44661996b4df", "4a96572d570108366da2cf5aa8213f69b591a2a3",
	"bc2ef601a538d69ef99d5bdafa605e63f902e8e4", "1c257e06b82de5959f60be6452c0a456ce846823",
	"4a393dbfe81113d35d9aedeb5c76f763190c1b2f", "85bc2f9753afd5f4fc5d7c75f74f8d526f26b4f3",
	"0c304a93cb8430108629bbbcaa27db3343299bc0", "3da5c4ddbaf17d37aea52c485d8bb85b07d77816",
	"7e3e39ebb9b2bf433b4ad17313770fbe4051649c", "0e90b92a15bd76d1c51c466d9edcd39a6c4a3c4c",
	"ed009dc458d6bd348bebb26e3ba514c7eafa320f", "7d70e02340bac451f281cecf0a980907974bd8be",
	"e69de29bb2d1d6434b8b29ae775ad8c2e48c5391", "1c69c4d2a65ad05c24ac3b6780b5748b97ffd3aa",
	"bc2ef601a538d69ef99d5bdafa605e63f902e8e4", "ead5a0eee1391308803cfebd8a2a8530495645eb",
	"8e5177d718c561d36efde08bad36b43687ee6bf0", "b464dff7a75ccc92fbd920fd9ae66a84b9d2bf94",
	"4e76e90b3c7e52390de9311a23c0a77575aed8a8", "3856c00e9450a51a62096327167fc43d3be62eef",
	"f9e5cc857610185e6feeb494a26bf27551a4f02b", "a135e3e0d4af177a902ca57dcc4c7fc6f30858b1",
	"e69de29bb2d1d6434b8b29ae775ad8c2e48c5391", "8c3014aceae45386c3c026a7ea4a1f68660d51d6",
}

func TestListFiles_success(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg, repo, repoPath, client := setupCommitServiceWithRepo(ctx, t, true)

	gittest.Exec(t, cfg, "-C", repoPath, "symbolic-ref", "HEAD", "refs/heads/test-do-not-touch")

	tests := []struct {
		desc     string
		revision string
		files    [][]byte
		oids     []string
	}{
		{
			desc:     "valid object ID",
			revision: "54fcc214b94e78d7a41a9a8fe6d87a5e59500e51",
			files: [][]byte{
				[]byte(".gitignore"), []byte(".gitmodules"), []byte("CHANGELOG"),
				[]byte("CONTRIBUTING.md"), []byte("Gemfile.zip"), []byte("LICENSE"),
				[]byte("MAINTENANCE.md"), []byte("PROCESS.md"), []byte("README"),
				[]byte("README.md"), []byte("VERSION"), []byte("encoding/feature-1.txt"),
				[]byte("encoding/feature-2.txt"), []byte("encoding/hotfix-1.txt"), []byte("encoding/hotfix-2.txt"),
				[]byte("encoding/iso8859.txt"), []byte("encoding/russian.rb"), []byte("encoding/test.txt"),
				[]byte("encoding/テスト.txt"), []byte("encoding/テスト.xls"), []byte("files/html/500.html"),
				[]byte("files/images/6049019_460s.jpg"), []byte("files/images/logo-black.png"), []byte("files/images/logo-white.png"),
				[]byte("files/images/wm.svg"), []byte("files/js/application.js"), []byte("files/js/commit.js.coffee"),
				[]byte("files/lfs/lfs_object.iso"), []byte("files/markdown/ruby-style-guide.md"), []byte("files/ruby/popen.rb"),
				[]byte("files/ruby/regex.rb"), []byte("files/ruby/version_info.rb"), []byte("files/whitespace"),
				[]byte("foo/bar/.gitkeep"),
			},
			oids: []string{
				"dfaa3f97ca337e20154a98ac9d0be76ddd1fcc82",
				"efd587ccb47caf5f31fc954edb21f0a713d9ecc3",
				"53855584db773c3df5b5f61f72974cb298822fbb",
				"c1788657b95998a2f177a4f86d68a60f2a80117f",
				"8ebd6459371e67b1365d332888238f1e41f2cf2c",
				"50b27c6518be44c42c4d87966ae2481ce895624c",
				"95d9f0a5e7bb054e9dd3975589b8dfc689e20e88",
				"bf757025c40c62e6ffa6f11d3819c769a76dbe09",
				"3742e48c1108ced3bf45ac633b34b65ac3f2af04",
				"faaf198af3a36dbf41961466703cc1d47c61d051",
				"998707b421c89bd9a3063333f9f728ef3e43d101",
				"8af7f880ce38649fc49f66e3f38857bfbec3f0b7",
				"16ca0b267f82cd2f5ca1157dd162dae98745eab8",
				"0fb47f093f769008049a0b0976ac3fa6d6125033",
				"4ae6c5e14452a35d04156277ae63e8356eb17cae",
				"b988ffed90cb6a9b7f98a3686a933edb3c5d70c0",
				"570f8e1dfe8149c1d17002712310d43dfeb43159",
				"7a17968582c21c9153ec24c6a9d5f33592ad9103",
				"f3064a3aa9c14277483f690250072e987e2c8356",
				"3a26c18b02e843b459732e7ade7ab9a154a1002b",
				"c84b9e90e4bee2666f5cb3688690d9824d88f3ee",
				"08cf843fd8fe1c50757df0a13fcc44661996b4df",
				"4a96572d570108366da2cf5aa8213f69b591a2a3",
				"bc2ef601a538d69ef99d5bdafa605e63f902e8e4",
				"1c257e06b82de5959f60be6452c0a456ce846823",
				"4a393dbfe81113d35d9aedeb5c76f763190c1b2f",
				"5f53439ca4b009096571d3c8bc3d09d30e7431b3",
				"0c304a93cb8430108629bbbcaa27db3343299bc0",
				"3da5c4ddbaf17d37aea52c485d8bb85b07d77816",
				"7e3e39ebb9b2bf433b4ad17313770fbe4051649c",
				"0e90b92a15bd76d1c51c466d9edcd39a6c4a3c4c",
				"ed009dc458d6bd348bebb26e3ba514c7eafa320f",
				"7d70e02340bac451f281cecf0a980907974bd8be",
				"e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
			},
		},
		{
			desc:     "valid branch",
			revision: "test-do-not-touch",
			files:    defaultFiles,
			oids:     defaultOIDs,
		},
		{
			desc:     "valid fully qualified branch",
			revision: "refs/heads/test-do-not-touch",
			files:    defaultFiles,
			oids:     defaultOIDs,
		},
		{
			desc:     "missing object ID uses default branch",
			revision: "",
			files:    defaultFiles,
			oids:     defaultOIDs,
		},
		{
			desc:     "invalid object ID",
			revision: "1234123412341234",
			files:    [][]byte{},
			oids:     []string{},
		},
		{
			desc:     "invalid branch",
			revision: "non/existing",
			files:    [][]byte{},
			oids:     []string{},
		},
		{
			desc:     "nonexisting fully qualified branch",
			revision: "refs/heads/foobar",
			files:    [][]byte{},
			oids:     []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			rpcRequest := gitalypb.ListFilesRequest{
				Repository: repo, Revision: []byte(tc.revision),
			}

			c, err := client.ListFiles(ctx, &rpcRequest)
			require.NoError(t, err)

			var files [][]byte
			for {
				resp, err := c.Recv()
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				files = append(files, resp.GetPaths()...)
			}

			require.ElementsMatch(t, files, tc.files)
		})
	}
}

func TestListFiles_unbornBranch(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, _, _, client := setupCommitServiceWithRepo(ctx, t, true)
	repo, _ := gittest.CreateRepository(ctx, t, cfg)

	tests := []struct {
		desc     string
		revision string
		code     codes.Code
	}{
		{
			desc:     "HEAD",
			revision: "HEAD",
		},
		{
			desc:     "unborn branch",
			revision: "refs/heads/master",
		},
		{
			desc:     "nonexisting branch",
			revision: "i-dont-exist",
		},
		{
			desc:     "nonexisting fully qualified branch",
			revision: "refs/heads/i-dont-exist",
		},
		{
			desc:     "missing revision without default branch",
			revision: "",
			code:     codes.FailedPrecondition,
		},
		{
			desc:     "valid object ID",
			revision: "54fcc214b94e78d7a41a9a8fe6d87a5e59500e51",
		},
		{
			desc:     "invalid object ID",
			revision: "1234123412341234",
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			rpcRequest := gitalypb.ListFilesRequest{
				Repository: repo, Revision: []byte(tc.revision),
			}

			c, err := client.ListFiles(ctx, &rpcRequest)
			require.NoError(t, err)

			var files [][]byte
			for {
				var resp *gitalypb.ListFilesResponse
				resp, err = c.Recv()
				if err != nil {
					break
				}

				require.NoError(t, err)
				files = append(files, resp.GetPaths()...)
			}

			if tc.code != codes.OK {
				testhelper.RequireGrpcCode(t, err, tc.code)
			} else {
				require.Equal(t, err, io.EOF)
			}
			require.Empty(t, files)
		})
	}
}

func TestListFiles_failure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, _, _, client := setupCommitServiceWithRepo(ctx, t, true)

	tests := []struct {
		desc string
		repo *gitalypb.Repository
		code codes.Code
	}{
		{
			desc: "nil repo",
			repo: nil,
			code: codes.InvalidArgument,
		},
		{
			desc: "empty repo object",
			repo: &gitalypb.Repository{},
			code: codes.InvalidArgument,
		},
		{
			desc: "non-existing repo",
			repo: &gitalypb.Repository{StorageName: "foo", RelativePath: "bar"},
			code: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			rpcRequest := gitalypb.ListFilesRequest{
				Repository: tc.repo, Revision: []byte("master"),
			}

			c, err := client.ListFiles(ctx, &rpcRequest)
			require.NoError(t, err)

			err = drainListFilesResponse(c)
			testhelper.RequireGrpcCode(t, err, tc.code)
		})
	}
}

func drainListFilesResponse(c gitalypb.CommitService_ListFilesClient) error {
	var err error
	for err == nil {
		_, err = c.Recv()
	}
	if err == io.EOF {
		return nil
	}
	return err
}

func TestListFiles_invalidRevision(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupCommitServiceWithRepo(ctx, t, true)

	stream, err := client.ListFiles(ctx, &gitalypb.ListFilesRequest{
		Repository: repo,
		Revision:   []byte("--output=/meow"),
	})
	require.NoError(t, err)

	_, err = stream.Recv()
	testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
}
