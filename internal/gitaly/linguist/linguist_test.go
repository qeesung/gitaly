package linguist

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-enry/go-enry/v2"
	enrydata "github.com/go-enry/go-enry/v2/data"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

// TestNew_knownLanguages tests the compatibility between the Ruby and the Go
// implementation. This test will be removed together with the Ruby implementation.
func TestNew_knownLanguages(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t, testcfg.WithRealLinguist())
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)

	linguist, err := New(cfg, gitCmdFactory)
	require.NoError(t, err)

	t.Run("by name", func(t *testing.T) {
		linguistLanguages := make([]string, 0, len(linguist.colorMap))
		for language := range linguist.colorMap {
			linguistLanguages = append(linguistLanguages, language)
		}

		enryLanguages := make([]string, 0, len(enrydata.IDByLanguage))
		for language := range enrydata.IDByLanguage {
			enryLanguages = append(enryLanguages, language)
		}

		require.ElementsMatch(t, linguistLanguages, enryLanguages)
	})

	t.Run("with their color", func(t *testing.T) {
		exclude := map[string]struct{}{}

		linguistLanguages := make(map[string]string, len(linguist.colorMap))
		for language, color := range linguist.colorMap {
			if color.Color == "" {
				exclude[language] = struct{}{}
				continue
			}
			linguistLanguages[language] = color.Color
		}

		enryLanguages := make(map[string]string, len(enrydata.IDByLanguage))
		for language := range enrydata.IDByLanguage {
			if _, excluded := exclude[language]; excluded {
				continue
			}

			color := enry.GetColor(language)
			enryLanguages[language] = color
		}

		require.Equal(t, linguistLanguages, enryLanguages)
	})
}

func TestInstance_Stats(t *testing.T) {
	testhelper.NewFeatureSets(featureflag.GoLanguageStats).
		Run(t, testInstanceStats)
}

func testInstanceStats(t *testing.T, ctx context.Context) {
	cfg := testcfg.Build(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)

	linguist, err := New(cfg, gitCmdFactory)
	require.NoError(t, err)

	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	commitID := git.ObjectID("1e292f8fedd741b75372e19097c76d327140c312")

	languageStatsFilename := filenameForCache(ctx)

	for _, tc := range []struct {
		desc          string
		setup         func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID)
		expectedStats ByteCountPerLanguage
		expectedErr   string
	}{
		{
			desc: "successful",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0])
				return repoProto, repoPath, commitID
			},
			expectedStats: map[string]uint64{
				"CoffeeScript": 107,
				"HTML":         349,
				"JavaScript":   1014,
				"Ruby":         2943,
			},
		},
		{
			desc: "preexisting cache",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0])
				repo := localrepo.NewTestRepo(t, cfg, repoProto)

				// We simply run the linguist once before so that it can already
				// write the cache.
				_, err := linguist.Stats(ctx, repo, commitID.String(), catfileCache)
				require.NoError(t, err)
				require.FileExists(t, filepath.Join(repoPath, languageStatsFilename))

				// Make sure it isn't able to generate stats from scratch
				require.NoError(t, os.RemoveAll(filepath.Join(repoPath, "objects", "pack")))

				return repoProto, repoPath, commitID
			},
			expectedStats: map[string]uint64{
				"CoffeeScript": 107,
				"HTML":         349,
				"JavaScript":   1014,
				"Ruby":         2943,
			},
		},
		{
			desc: "corrupted cache",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0])

				require.NoError(t, os.WriteFile(filepath.Join(repoPath, languageStatsFilename), []byte("garbage"), 0o644))

				return repoProto, repoPath, commitID
			},
			expectedStats: map[string]uint64{
				"CoffeeScript": 107,
				"HTML":         349,
				"JavaScript":   1014,
				"Ruby":         2943,
			},
		},
		{
			desc: "old cache",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				repo := localrepo.NewTestRepo(t, cfg, repoProto)

				oldCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "main.rb", Content: "require 'fileutils'", Mode: "100644"},
				))
				newCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(oldCommitID), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "main.go", Content: "package main", Mode: "100644"},
				))

				// Precreate the cache with the old commit. This ensures that
				// linguist knows to update the cache.
				stats, err := linguist.Stats(ctx, repo, oldCommitID.String(), catfileCache)
				require.NoError(t, err)
				require.FileExists(t, filepath.Join(repoPath, languageStatsFilename))
				require.Equal(t, ByteCountPerLanguage{
					"Ruby": 19,
				}, stats)

				return repoProto, repoPath, newCommitID
			},
			expectedStats: map[string]uint64{
				"Go": 12,
			},
		},
		{
			desc: "missing repository",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoPath := filepath.Join(testhelper.TempDir(t), "nonexistent")
				repoProto := &gitalypb.Repository{StorageName: cfg.Storages[0].Name, RelativePath: "nonexistent"}

				return repoProto, repoPath, commitID
			},
			expectedErr: "GetRepoPath: not a git repository",
		},
		{
			desc: "missing commit",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				return repoProto, repoPath, commitID
			},
			expectedErr: "linguist",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, repoPath, objectID := tc.setup(t)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			stats, err := linguist.Stats(ctx, repo, objectID.String(), catfileCache)
			if tc.expectedErr == "" {
				require.NoError(t, err)
				require.Equal(t, tc.expectedStats, stats)
				require.FileExists(t, filepath.Join(repoPath, languageStatsFilename))
			} else {
				require.Contains(t, err.Error(), tc.expectedErr)
			}
		})
	}
}

func TestInstance_Stats_unmarshalJSONError(t *testing.T) {
	cfg := testcfg.Build(t)
	ctx := featureflag.ContextWithFeatureFlag(testhelper.Context(t), featureflag.GoLanguageStats, false)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	invalidRepo := &gitalypb.Repository{StorageName: "fake", RelativePath: "path"}

	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	repo := localrepo.New(config.NewLocator(cfg), gitCmdFactory, catfileCache, invalidRepo)

	ling, err := New(cfg, gitCmdFactory)
	require.NoError(t, err)

	// When an error occurs, this used to trigger JSON marshelling of a plain string
	// the new behaviour shouldn't do that, and return an command error
	_, err = ling.Stats(ctx, repo, "deadbeef", catfileCache)
	require.Error(t, err)

	_, ok := err.(*json.SyntaxError)
	require.False(t, ok, "expected the error not be a json Syntax Error")
}

func TestInstance_Stats_incremental(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	logger, hook := test.NewNullLogger()
	ctx := testhelper.Context(t, testhelper.ContextWithLogger(logrus.NewEntry(logger)))
	ctx = featureflag.ContextWithFeatureFlag(ctx, featureflag.GoLanguageStats, true)

	gitCmdFactory := gittest.NewCommandFactory(t, cfg)

	linguist, err := New(cfg, gitCmdFactory)
	require.NoError(t, err)

	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	repoProto, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0])
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	cleanStats, err := linguist.Stats(ctx, repo, "1e292f8fedd741b75372e19097c76d327140c312", catfileCache)
	require.NoError(t, err)
	require.Len(t, hook.AllEntries(), 0)
	require.NoError(t, os.Remove(filepath.Join(repoPath, languageStatsFilename)))

	_, err = linguist.Stats(ctx, repo, "cfe32cf61b73a0d5e9f13e774abde7ff789b1660", catfileCache)
	require.NoError(t, err)
	require.Len(t, hook.AllEntries(), 0)
	require.FileExists(t, filepath.Join(repoPath, languageStatsFilename))

	incStats, err := linguist.Stats(ctx, repo, "1e292f8fedd741b75372e19097c76d327140c312", catfileCache)
	require.NoError(t, err)
	require.Len(t, hook.AllEntries(), 0)
	require.FileExists(t, filepath.Join(repoPath, languageStatsFilename))

	require.Equal(t, cleanStats, incStats)
}

func TestNew(t *testing.T) {
	cfg := testcfg.Build(t, testcfg.WithRealLinguist())

	ling, err := New(cfg, gittest.NewCommandFactory(t, cfg))
	require.NoError(t, err)

	require.Equal(t, "#701516", ling.Color("Ruby"), "color value for 'Ruby'")
}

func TestNew_loadLanguagesCustomPath(t *testing.T) {
	jsonPath, err := filepath.Abs("testdata/fake-languages.json")
	require.NoError(t, err)

	cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{Ruby: config.Ruby{LinguistLanguagesPath: jsonPath}}))

	ling, err := New(cfg, gittest.NewCommandFactory(t, cfg))
	require.NoError(t, err)

	require.Equal(t, "foo color", ling.Color("FooBar"))
}

// filenameForCache returns the filename where the cache is stored, depending on
// the feature flag.
func filenameForCache(ctx context.Context) string {
	if featureflag.GoLanguageStats.IsDisabled(ctx) {
		return "language-stats.cache"
	}
	return languageStatsFilename
}

func BenchmarkInstance_Stats(b *testing.B) {
	testhelper.NewFeatureSets(featureflag.GoLanguageStats).
		Bench(b, benchmarkInstanceStats)
}

func benchmarkInstanceStats(b *testing.B, ctx context.Context) {
	cfg := testcfg.Build(b)
	gitCmdFactory := gittest.NewCommandFactory(b, cfg)
	languageStatsFilename := filenameForCache(ctx)

	linguist, err := New(cfg, gitCmdFactory)
	require.NoError(b, err)

	catfileCache := catfile.NewCache(cfg)
	b.Cleanup(catfileCache.Stop)

	repoProto, repoPath := gittest.CloneRepo(b, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
		SourceRepo: "benchmark.git",
	})
	repo := localrepo.NewTestRepo(b, cfg, repoProto)

	var scratchStat ByteCountPerLanguage
	var incStats ByteCountPerLanguage

	b.Run("from scratch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			require.NoError(b, os.RemoveAll(filepath.Join(repoPath, languageStatsFilename)))
			b.StartTimer()

			scratchStat, err = linguist.Stats(ctx, repo, "f5dfdd0057cd6bffc6259a5c8533dde5bf6a9d37", catfileCache)
			require.NoError(b, err)
		}
	})

	b.Run("incremental", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			require.NoError(b, os.RemoveAll(filepath.Join(repoPath, languageStatsFilename)))
			// a commit about 3 months older than the next
			_, err = linguist.Stats(ctx, repo, "3c813b292d25a9b2ffda70e7f609f623bfc0cb37", catfileCache)
			b.StartTimer()

			incStats, err = linguist.Stats(ctx, repo, "f5dfdd0057cd6bffc6259a5c8533dde5bf6a9d37", catfileCache)
			require.NoError(b, err)
		}
	})

	require.Equal(b, scratchStat, incStats)
}
