package gitpipe

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestLsTree(t *testing.T) {
	cfg := testcfg.Build(t)

	repoProto, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	for _, tc := range []struct {
		desc            string
		revision        string
		options         []LsTreeOption
		expectedResults []RevisionResult
		expectedErr     error
	}{
		{
			desc:     "initial commit",
			revision: "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
			expectedResults: []RevisionResult{
				{OID: "470ad2fcf1e33798f1afc5781d08e60c40f51e7a", ObjectName: []byte(".gitignore")},
				{OID: "50b27c6518be44c42c4d87966ae2481ce895624c", ObjectName: []byte("LICENSE")},
				{OID: "faaf198af3a36dbf41961466703cc1d47c61d051", ObjectName: []byte("README.md")},
			},
		},
		{
			desc:     "includes submodule",
			revision: "cfe32cf61b73a0d5e9f13e774abde7ff789b1660",
			expectedResults: []RevisionResult{
				{OID: "470ad2fcf1e33798f1afc5781d08e60c40f51e7a", ObjectName: []byte(".gitignore")},
				{OID: "fdaada1754989978413d618ee1fb1c0469d6a664", ObjectName: []byte(".gitmodules")},
				{OID: "c1788657b95998a2f177a4f86d68a60f2a80117f", ObjectName: []byte("CONTRIBUTING.md")},
				{OID: "50b27c6518be44c42c4d87966ae2481ce895624c", ObjectName: []byte("LICENSE")},
				{OID: "faaf198af3a36dbf41961466703cc1d47c61d051", ObjectName: []byte("README.md")},
				{OID: "409f37c4f05865e4fb208c771485f211a22c4c2d", ObjectName: []byte("six")},
			},
		},
		{
			desc:     "filter blobs only",
			revision: "cfe32cf61b73a0d5e9f13e774abde7ff789b1660",
			options: []LsTreeOption{
				LsTreeWithBlobFilter(),
			},
			expectedResults: []RevisionResult{
				{OID: "470ad2fcf1e33798f1afc5781d08e60c40f51e7a", ObjectName: []byte(".gitignore")},
				{OID: "fdaada1754989978413d618ee1fb1c0469d6a664", ObjectName: []byte(".gitmodules")},
				{OID: "c1788657b95998a2f177a4f86d68a60f2a80117f", ObjectName: []byte("CONTRIBUTING.md")},
				{OID: "50b27c6518be44c42c4d87966ae2481ce895624c", ObjectName: []byte("LICENSE")},
				{OID: "faaf198af3a36dbf41961466703cc1d47c61d051", ObjectName: []byte("README.md")},
			},
		},
		{
			desc:     "empty tree",
			revision: "7efb185dd22fd5c51ef044795d62b7847900c341",
		},
		{
			desc:     "non-recursive",
			revision: "913c66a37b4a45b9769037c55c2d238bd0942d2e",
			expectedResults: []RevisionResult{
				{OID: "fd90a3d2d21d6b4f9bec2c33fb7f49780c55f0d2", ObjectName: []byte(".DS_Store")},
				{OID: "470ad2fcf1e33798f1afc5781d08e60c40f51e7a", ObjectName: []byte(".gitignore")},
				{OID: "fdaada1754989978413d618ee1fb1c0469d6a664", ObjectName: []byte(".gitmodules")},
				{OID: "c74175afd117781cbc983664339a0f599b5bb34e", ObjectName: []byte("CHANGELOG")},
				{OID: "c1788657b95998a2f177a4f86d68a60f2a80117f", ObjectName: []byte("CONTRIBUTING.md")},
				{OID: "50b27c6518be44c42c4d87966ae2481ce895624c", ObjectName: []byte("LICENSE")},
				{OID: "95d9f0a5e7bb054e9dd3975589b8dfc689e20e88", ObjectName: []byte("MAINTENANCE.md")},
				{OID: "bf757025c40c62e6ffa6f11d3819c769a76dbe09", ObjectName: []byte("PROCESS.md")},
				{OID: "faaf198af3a36dbf41961466703cc1d47c61d051", ObjectName: []byte("README.md")},
				{OID: "998707b421c89bd9a3063333f9f728ef3e43d101", ObjectName: []byte("VERSION")},
				{OID: "3c122d2b7830eca25235131070602575cf8b41a1", ObjectName: []byte("encoding")},
				{OID: "ab746f8ad0b84b147290041dc13cc9c7adc52930", ObjectName: []byte("files")},
				{OID: "409f37c4f05865e4fb208c771485f211a22c4c2d", ObjectName: []byte("six")},
			},
		},
		{
			desc:     "recursive",
			revision: "913c66a37b4a45b9769037c55c2d238bd0942d2e",
			options: []LsTreeOption{
				LsTreeWithRecursive(),
			},
			expectedResults: []RevisionResult{
				{OID: "fd90a3d2d21d6b4f9bec2c33fb7f49780c55f0d2", ObjectName: []byte(".DS_Store")},
				{OID: "470ad2fcf1e33798f1afc5781d08e60c40f51e7a", ObjectName: []byte(".gitignore")},
				{OID: "fdaada1754989978413d618ee1fb1c0469d6a664", ObjectName: []byte(".gitmodules")},
				{OID: "c74175afd117781cbc983664339a0f599b5bb34e", ObjectName: []byte("CHANGELOG")},
				{OID: "c1788657b95998a2f177a4f86d68a60f2a80117f", ObjectName: []byte("CONTRIBUTING.md")},
				{OID: "50b27c6518be44c42c4d87966ae2481ce895624c", ObjectName: []byte("LICENSE")},
				{OID: "95d9f0a5e7bb054e9dd3975589b8dfc689e20e88", ObjectName: []byte("MAINTENANCE.md")},
				{OID: "bf757025c40c62e6ffa6f11d3819c769a76dbe09", ObjectName: []byte("PROCESS.md")},
				{OID: "faaf198af3a36dbf41961466703cc1d47c61d051", ObjectName: []byte("README.md")},
				{OID: "998707b421c89bd9a3063333f9f728ef3e43d101", ObjectName: []byte("VERSION")},
				{OID: "8af7f880ce38649fc49f66e3f38857bfbec3f0b7", ObjectName: []byte("encoding/feature-1.txt")},
				{OID: "16ca0b267f82cd2f5ca1157dd162dae98745eab8", ObjectName: []byte("encoding/feature-2.txt")},
				{OID: "0fb47f093f769008049a0b0976ac3fa6d6125033", ObjectName: []byte("encoding/hotfix-1.txt")},
				{OID: "4ae6c5e14452a35d04156277ae63e8356eb17cae", ObjectName: []byte("encoding/hotfix-2.txt")},
				{OID: "570f8e1dfe8149c1d17002712310d43dfeb43159", ObjectName: []byte("encoding/russian.rb")},
				{OID: "7a17968582c21c9153ec24c6a9d5f33592ad9103", ObjectName: []byte("encoding/test.txt")},
				{OID: "f3064a3aa9c14277483f690250072e987e2c8356", ObjectName: []byte("encoding/\343\203\206\343\202\271\343\203\210.txt")},
				{OID: "3a26c18b02e843b459732e7ade7ab9a154a1002b", ObjectName: []byte("encoding/\343\203\206\343\202\271\343\203\210.xls")},
				{OID: "60d7a906c2fd9e4509aeb1187b98d0ea7ce827c9", ObjectName: []byte("files/.DS_Store")},
				{OID: "c84b9e90e4bee2666f5cb3688690d9824d88f3ee", ObjectName: []byte("files/html/500.html")},
				{OID: "4a96572d570108366da2cf5aa8213f69b591a2a3", ObjectName: []byte("files/images/logo-black.png")},
				{OID: "bc2ef601a538d69ef99d5bdafa605e63f902e8e4", ObjectName: []byte("files/images/logo-white.png")},
				{OID: "4a393dbfe81113d35d9aedeb5c76f763190c1b2f", ObjectName: []byte("files/js/application.js")},
				{OID: "5f53439ca4b009096571d3c8bc3d09d30e7431b3", ObjectName: []byte("files/js/commit.js.coffee")},
				{OID: "3da5c4ddbaf17d37aea52c485d8bb85b07d77816", ObjectName: []byte("files/markdown/ruby-style-guide.md")},
				{OID: "e2fbafb389911ef68e4da2da7da917f6acb9e86a", ObjectName: []byte("files/ruby/popen.rb")},
				{OID: "d18fc8bf2cecbe21bbbd5da692eaf83e32c48638", ObjectName: []byte("files/ruby/regex.rb")},
				{OID: "6ee41e85cc9bf33c10b690df09ca735b22f3790f", ObjectName: []byte("files/ruby/version_info.rb")},
				{OID: "409f37c4f05865e4fb208c771485f211a22c4c2d", ObjectName: []byte("six")},
			},
		},
		{
			desc:     "invalid revision",
			revision: "refs/heads/does-not-exist",
			expectedErr: errors.New("ls-tree pipeline command: exit status 128, stderr: " +
				"\"fatal: Not a valid object name refs/heads/does-not-exist\\n\""),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			it := LsTree(ctx, repo, tc.revision, tc.options...)

			var results []RevisionResult
			for it.Next() {
				results = append(results, it.Result())
			}

			// We're converting the error here to a plain un-nested error such that we
			// don't have to replicate the complete error's structure.
			err := it.Err()
			if err != nil {
				err = errors.New(err.Error())
			}

			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedResults, results)
		})
	}
}
