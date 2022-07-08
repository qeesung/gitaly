package linguist

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestNewLanguageStats(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0])
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	t.Run("non-existing cache", func(t *testing.T) {
		s, err := newLanguageStats(repo)
		require.NoError(t, err)
		require.Empty(t, s.Totals)
		require.Empty(t, s.ByFile)
	})

	t.Run("pre-existing cache", func(t *testing.T) {
		s, err := newLanguageStats(repo)
		require.NoError(t, err)

		s.Totals["C"] = 555
		require.NoError(t, s.save(repo, "badcafe"))

		require.Equal(t, ByteCountPerLanguage{"C": 555}, s.Totals)
	})

	t.Run("corrupt cache", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, languageStatsFilename), []byte("garbage"), 0o644))

		s, err := newLanguageStats(repo)
		require.Errorf(t, err, "new language stats zlib reader: invalid header")
		require.Empty(t, s.Totals)
		require.Empty(t, s.ByFile)
	})
}

func TestLanguageStats_add(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	repoProto, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	for _, tc := range []struct {
		desc string
		run  func(*testing.T, *languageStats)
	}{
		{
			desc: "adds to the total",
			run: func(t *testing.T, s *languageStats) {
				s.add("main.go", "Go", 100)

				require.Equal(t, uint64(100), s.Totals["Go"])
				require.Len(t, s.ByFile, 1)
				require.Equal(t, ByteCountPerLanguage{"Go": 100}, s.ByFile["main.go"])
			},
		},
		{
			desc: "accumulates",
			run: func(t *testing.T, s *languageStats) {
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
			run: func(t *testing.T, s *languageStats) {
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
			run: func(t *testing.T, s *languageStats) {
				s.add("main.go", "Go", 60)
				s.add("main.go", "Go", 30)

				require.Equal(t, uint64(30), s.Totals["Go"])
				require.Len(t, s.ByFile, 1)
				require.Equal(t, ByteCountPerLanguage{"Go": 30}, s.ByFile["main.go"])
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			s, err := newLanguageStats(repo)
			require.NoError(t, err)

			tc.run(t, s)
		})
	}
}

func TestLanguageStats_drop(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	repoProto, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	for _, tc := range []struct {
		desc string
		run  func(*testing.T, *languageStats)
	}{
		{
			desc: "existing file",
			run: func(t *testing.T, s *languageStats) {
				s.drop("main.go")

				require.Equal(t, uint64(20), s.Totals["Go"])
				require.Len(t, s.ByFile, 1)
				require.Equal(t, ByteCountPerLanguage{"Go": 20}, s.ByFile["main_test.go"])
			},
		},
		{
			desc: "non-existing file",
			run: func(t *testing.T, s *languageStats) {
				s.drop("foo.go")

				require.Equal(t, uint64(100), s.Totals["Go"])
				require.Len(t, s.ByFile, 2)
				require.Equal(t, ByteCountPerLanguage{"Go": 80}, s.ByFile["main.go"])
				require.Equal(t, ByteCountPerLanguage{"Go": 20}, s.ByFile["main_test.go"])
			},
		},
		{
			desc: "all files",
			run: func(t *testing.T, s *languageStats) {
				s.drop("main.go", "main_test.go")

				require.Empty(t, s.Totals)
				require.Empty(t, s.ByFile)
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			s, err := newLanguageStats(repo)
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

	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0])
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	s, err := newLanguageStats(repo)
	require.NoError(t, err)

	s.Totals["Go"] = 100
	s.ByFile["main.go"] = ByteCountPerLanguage{"Go": 80}
	s.ByFile["main_test.go"] = ByteCountPerLanguage{"Go": 20}

	err = s.save(repo, "buzz")
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(repoPath, languageStatsFilename))

	loaded, err := newLanguageStats(repo)
	require.NoError(t, err)

	require.Equal(t, "buzz", loaded.CommitID)
	require.Equal(t, languageStatsVersion, loaded.Version)
	require.Equal(t, s.Totals, loaded.Totals)
	require.Equal(t, s.ByFile, loaded.ByFile)
}
