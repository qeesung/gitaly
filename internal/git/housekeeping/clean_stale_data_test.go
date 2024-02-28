package housekeeping

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

const (
	Delete entryFinalState = iota
	Keep

	ancient = 240 * time.Hour
	recent  = 24 * time.Hour
)

type mockDirEntry struct {
	fs.DirEntry
	isDir bool
	name  string
	fi    fs.FileInfo
}

func (m mockDirEntry) Name() string {
	return m.name
}

func (m mockDirEntry) IsDir() bool {
	return m.isDir
}

func (m mockDirEntry) Info() (fs.FileInfo, error) {
	return m.fi, nil
}

type mockFileInfo struct {
	fs.FileInfo
	modTime time.Time
}

func (m mockFileInfo) ModTime() time.Time {
	return m.modTime
}

func TestIsStaleTemporaryObject(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		dirEntry      fs.DirEntry
		expectIsStale bool
	}{
		{
			name: "regular_file",
			dirEntry: mockDirEntry{
				name: "objects",
				fi: mockFileInfo{
					modTime: time.Now().Add(-1 * time.Hour),
				},
			},
			expectIsStale: false,
		},
		{
			name: "directory",
			dirEntry: mockDirEntry{
				name:  "tmp",
				isDir: true,
				fi: mockFileInfo{
					modTime: time.Now().Add(-1 * time.Hour),
				},
			},
			expectIsStale: false,
		},
		{
			name: "recent time file",
			dirEntry: mockDirEntry{
				name: "tmp_DELETEME",
				fi: mockFileInfo{
					modTime: time.Now().Add(-1 * time.Hour),
				},
			},
			expectIsStale: false,
		},
		{
			name: "recent time file",
			dirEntry: mockDirEntry{
				name: "tmp_DELETEME",
				fi: mockFileInfo{
					modTime: time.Now().Add(-23 * time.Hour),
				},
			},
			expectIsStale: false,
		},
		{
			name: "very old temp file",
			dirEntry: mockDirEntry{
				name: "tmp_DELETEME",
				fi: mockFileInfo{
					modTime: time.Now().Add(-25 * time.Hour),
				},
			},
			expectIsStale: true,
		},
		{
			name: "very old temp file",
			dirEntry: mockDirEntry{
				name: "tmp_DELETEME",
				fi: mockFileInfo{
					modTime: time.Now().Add(-8 * 24 * time.Hour),
				},
			},
			expectIsStale: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isStale, err := isStaleTemporaryObject(tc.dirEntry)
			require.NoError(t, err)
			require.Equal(t, tc.expectIsStale, isStale)
		})
	}
}

func TestPruneEmptyConfigSections(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)
	configPath := filepath.Join(repoPath, "config")

	for _, tc := range []struct {
		desc                    string
		configData              string
		expectedData            string
		expectedSkippedSections int
	}{
		{
			desc:         "empty",
			configData:   "",
			expectedData: "",
		},
		{
			desc:         "newline only",
			configData:   "\n",
			expectedData: "\n",
		},
		{
			desc:         "no stripping",
			configData:   "[foo]\nbar = baz\n",
			expectedData: "[foo]\nbar = baz\n",
		},
		{
			desc:         "no stripping with missing newline",
			configData:   "[foo]\nbar = baz",
			expectedData: "[foo]\nbar = baz",
		},
		{
			desc:         "multiple sections",
			configData:   "[foo]\nbar = baz\n[bar]\nfoo = baz\n",
			expectedData: "[foo]\nbar = baz\n[bar]\nfoo = baz\n",
		},
		{
			desc:         "missing newline",
			configData:   "[foo]\nbar = baz",
			expectedData: "[foo]\nbar = baz",
		},
		{
			desc:         "single comment",
			configData:   "# foobar\n",
			expectedData: "# foobar\n",
		},
		{
			// This is not correct, but we really don't want to start parsing
			// the config format completely. So we err on the side of caution
			// and just say this is fine.
			desc:                    "empty section with comment",
			configData:              "[foo]\n# comment\n[bar]\n[baz]\n",
			expectedData:            "[foo]\n# comment\n",
			expectedSkippedSections: 1,
		},
		{
			desc:         "empty section",
			configData:   "[foo]\n",
			expectedData: "",
		},
		{
			desc:                    "empty sections",
			configData:              "[foo]\n[bar]\n[baz]\n",
			expectedData:            "",
			expectedSkippedSections: 2,
		},
		{
			desc:                    "empty sections with missing newline",
			configData:              "[foo]\n[bar]\n[baz]",
			expectedData:            "",
			expectedSkippedSections: 2,
		},
		{
			desc:         "trailing empty section",
			configData:   "[foo]\nbar = baz\n[foo]\n",
			expectedData: "[foo]\nbar = baz\n",
		},
		{
			desc:                    "mixed keys and sections",
			configData:              "[empty]\n[nonempty]\nbar = baz\nbar = baz\n[empty]\n",
			expectedData:            "[nonempty]\nbar = baz\nbar = baz\n",
			expectedSkippedSections: 1,
		},
		{
			desc: "real world example",
			configData: `[core]
        repositoryformatversion = 0
        filemode = true
        bare = true
[uploadpack]
        allowAnySHA1InWant = true
[remote "tmp-8be1695862b62390d1f873f9164122e4"]
[remote "tmp-d97f78c39fde4b55e0d0771dfc0501ef"]
[remote "tmp-23a2471e7084e1548ef47bbc9d6afff6"]
[remote "tmp-6ef9759bb14db34ca67de4681f0a812a"]
[remote "tmp-992cb6a0ea428a511cc2de3cde051227"]
[remote "tmp-a720c2b6794fdbad50f36f0a4e9501ff"]
[remote "tmp-4b4f6d68031aa1288613f40b1a433278"]
[remote "tmp-fc12da796c907e8ea5faed134806acfb"]
[remote "tmp-49e1fbb6eccdb89059a7231eef785d03"]
[remote "tmp-e504bbbed5d828cd96b228abdef4b055"]
[remote "tmp-36e856371fdacb7b4909240ba6bc0b34"]
[remote "tmp-9a1bc23bb2200b9426340a5ba934f5ba"]
[remote "tmp-49ead30f732995498e0585b569917c31"]
[remote "tmp-8419f1e1445ccd6e1c60aa421573447c"]
[remote "tmp-f7a91ec9415f984d3747cf608b0a7e9c"]
        prune = true
[remote "tmp-ea77d1e5348d07d693aa2bf8a2c98637"]
[remote "tmp-3f190ab463b804612cb007487e0cbb4d"]`,
			expectedData: `[core]
        repositoryformatversion = 0
        filemode = true
        bare = true
[uploadpack]
        allowAnySHA1InWant = true
[remote "tmp-f7a91ec9415f984d3747cf608b0a7e9c"]
        prune = true
`,
			expectedSkippedSections: 15,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			require.NoError(t, os.WriteFile(configPath, []byte(tc.configData), perm.SharedFile))

			skippedSections, err := PruneEmptyConfigSections(ctx, repo)
			require.NoError(t, err)
			require.Equal(t, tc.expectedSkippedSections, skippedSections)

			require.Equal(t, tc.expectedData, string(testhelper.MustReadFile(t, configPath)))
		})
	}
}
