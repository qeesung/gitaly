package linguist

import (
	"compress/zlib"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestInitLanguageStats(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	for _, tc := range []struct {
		desc string
		run  func(*testing.T, *localrepo.Repo, string)
	}{
		{
			desc: "non-existing cache",
			run: func(t *testing.T, repo *localrepo.Repo, repoPath string) {
				stats, err := initLanguageStats(ctx, repo)
				require.NoError(t, err)
				require.Empty(t, stats.Totals)
				require.Empty(t, stats.ByFile)
			},
		},
		{
			desc: "pre-existing cache",
			run: func(t *testing.T, repo *localrepo.Repo, repoPath string) {
				stats, err := initLanguageStats(ctx, repo)
				require.NoError(t, err)

				stats.Totals["C"] = 555
				require.NoError(t, stats.save(ctx, repo, "badcafe"))

				require.Equal(t, ByteCountPerLanguage{"C": 555}, stats.Totals)
			},
		},
		{
			desc: "corrupt cache",
			run: func(t *testing.T, repo *localrepo.Repo, repoPath string) {
				require.NoError(t, os.WriteFile(filepath.Join(repoPath, languageStatsFilename), []byte("garbage"), perm.PrivateWriteOnceFile))

				stats, err := initLanguageStats(ctx, repo)
				require.Errorf(t, err, "new language stats zlib reader: invalid header")
				require.Empty(t, stats.Totals)
				require.Empty(t, stats.ByFile)
			},
		},
		{
			desc: "incorrect version cache",
			run: func(t *testing.T, repo *localrepo.Repo, repoPath string) {
				stats, err := initLanguageStats(ctx, repo)
				require.NoError(t, err)

				stats.Totals["C"] = 555
				stats.Version = "faulty"

				// Copy save() behavior, but with a faulty version
				file, err := os.OpenFile(filepath.Join(repoPath, languageStatsFilename), os.O_WRONLY|os.O_CREATE, perm.PrivateWriteOnceFile)
				require.NoError(t, err)
				w := zlib.NewWriter(file)
				require.NoError(t, json.NewEncoder(w).Encode(stats))
				require.NoError(t, w.Close())
				require.NoError(t, file.Sync())
				require.NoError(t, file.Close())

				newStats, err := initLanguageStats(ctx, repo)
				require.Error(t, err, fmt.Errorf("new language stats version mismatch %s vs %s", languageStatsVersion, "faulty"))
				require.Empty(t, newStats.Totals)
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})
			repo := localrepo.NewTestRepo(t, cfg, repoProto)
			tc.run(t, repo, repoPath)
		})
	}
}

func TestLanguageStats_add(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	for _, tc := range []struct {
		desc string
		run  func(*testing.T, languageStats)
	}{
		{
			desc: "adds to the total",
			run: func(t *testing.T, s languageStats) {
				s.add("main.go", "Go", 100)

				require.Equal(t, uint64(100), s.Totals["Go"])
				require.Len(t, s.ByFile, 1)
				require.Equal(t, ByteCountPerLanguage{"Go": 100}, s.ByFile["main.go"])
			},
		},
		{
			desc: "accumulates",
			run: func(t *testing.T, s languageStats) {
				s.add("main.go", "Go", 100)
				s.add("main_test.go", "Go", 80)

				require.Equal(t, uint64(180), s.Totals["Go"])
				require.Len(t, s.ByFile, 2)
				require.Equal(t, ByteCountPerLanguage{"Go": 100}, s.ByFile["main.go"])
				require.Equal(t, ByteCountPerLanguage{"Go": 80}, s.ByFile["main_test.go"])
			},
		},
		{
			desc: "languages don't interfere",
			run: func(t *testing.T, s languageStats) {
				s.add("main.go", "Go", 60)
				s.add("Makefile", "Make", 30)

				require.Equal(t, uint64(60), s.Totals["Go"])
				require.Equal(t, uint64(30), s.Totals["Make"])
				require.Len(t, s.ByFile, 2)
				require.Equal(t, ByteCountPerLanguage{"Go": 60}, s.ByFile["main.go"])
				require.Equal(t, ByteCountPerLanguage{"Make": 30}, s.ByFile["Makefile"])
			},
		},
		{
			desc: "updates the stat for a file",
			run: func(t *testing.T, s languageStats) {
				s.add("main.go", "Go", 60)
				s.add("main.go", "Go", 30)

				require.Equal(t, uint64(30), s.Totals["Go"])
				require.Len(t, s.ByFile, 1)
				require.Equal(t, ByteCountPerLanguage{"Go": 30}, s.ByFile["main.go"])
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			s, err := initLanguageStats(ctx, repo)
			require.NoError(t, err)

			tc.run(t, s)
		})
	}
}

func TestLanguageStats_drop(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	for _, tc := range []struct {
		desc string
		run  func(*testing.T, languageStats)
	}{
		{
			desc: "existing file",
			run: func(t *testing.T, s languageStats) {
				s.drop("main.go")

				require.Equal(t, uint64(20), s.Totals["Go"])
				require.Len(t, s.ByFile, 1)
				require.Equal(t, ByteCountPerLanguage{"Go": 20}, s.ByFile["main_test.go"])
			},
		},
		{
			desc: "non-existing file",
			run: func(t *testing.T, s languageStats) {
				s.drop("foo.go")

				require.Equal(t, uint64(100), s.Totals["Go"])
				require.Len(t, s.ByFile, 2)
				require.Equal(t, ByteCountPerLanguage{"Go": 80}, s.ByFile["main.go"])
				require.Equal(t, ByteCountPerLanguage{"Go": 20}, s.ByFile["main_test.go"])
			},
		},
		{
			desc: "all files",
			run: func(t *testing.T, s languageStats) {
				s.drop("main.go", "main_test.go")

				require.Empty(t, s.Totals)
				require.Empty(t, s.ByFile)
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			s, err := initLanguageStats(ctx, repo)
			require.NoError(t, err)

			s.Totals["Go"] = 100
			s.ByFile["main.go"] = ByteCountPerLanguage{"Go": 80}
			s.ByFile["main_test.go"] = ByteCountPerLanguage{"Go": 20}

			tc.run(t, s)
		})
	}
}

func TestLanguageStats_save(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	s, err := initLanguageStats(ctx, repo)
	require.NoError(t, err)

	s.Totals["Go"] = 100
	s.ByFile["main.go"] = ByteCountPerLanguage{"Go": 80}
	s.ByFile["main_test.go"] = ByteCountPerLanguage{"Go": 20}

	err = s.save(ctx, repo, "buzz")
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(repoPath, languageStatsFilename))

	loaded, err := initLanguageStats(ctx, repo)
	require.NoError(t, err)

	require.Equal(t, git.ObjectID("buzz"), loaded.CommitID)
	require.Equal(t, languageStatsVersion, loaded.Version)
	require.Equal(t, s.Totals, loaded.Totals)
	require.Equal(t, s.ByFile, loaded.ByFile)
}
