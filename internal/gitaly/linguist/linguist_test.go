package linguist

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestInstance_Stats(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	for _, tc := range []struct {
		desc          string
		setup         func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID)
		expectedStats ByteCountPerLanguage
		expectedErr   string
	}{
		{
			desc: "successful",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "webpack.coffee", Mode: "100644", Content: strings.Repeat("a", 107)},
					gittest.TreeEntry{Path: "show_user.html", Mode: "100644", Content: strings.Repeat("a", 349)},
					gittest.TreeEntry{Path: "api.javascript", Mode: "100644", Content: strings.Repeat("a", 1014)},
					gittest.TreeEntry{Path: "application.rb", Mode: "100644", Content: strings.Repeat("a", 2943)},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"CoffeeScript": 107,
				"HTML":         349,
				"JavaScript":   1014,
				"Ruby":         2943,
			},
		},
		{
			desc: "documentation is ignored",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				docTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "readme.md", Mode: "100644", Content: strings.Repeat("a", 500)},
					{Path: "index.html", Mode: "100644", Content: strings.Repeat("a", 120)},
					{Path: "formatter.rb", Mode: "100644", Content: strings.Repeat("a", 403)},
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "docs", Mode: "040000", OID: docTree},
					gittest.TreeEntry{Path: "main.c", Mode: "100644", Content: strings.Repeat("a", 85)},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"C": 85,
			},
		},
		{
			desc: "documentation with overrides",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				docTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "readme.md", Mode: "100644", Content: strings.Repeat("a", 500)},
					{Path: "index.html", Mode: "100644", Content: strings.Repeat("a", 120)},
					{Path: "formatter.rb", Mode: "100644", Content: strings.Repeat("a", 403)},
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "docs", Mode: "040000", OID: docTree},
					gittest.TreeEntry{Path: "main.c", Mode: "100644", Content: strings.Repeat("a", 85)},
					gittest.TreeEntry{Path: ".gitattributes", Mode: "100644", Content: "formatter.rb -linguist-documentation"},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"C":    85,
				"Ruby": 403,
			},
		},
		{
			desc: "vendor is ignored",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				vendorTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "app.rb", Mode: "100644", Content: strings.Repeat("a", 500)},
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "vendor", Mode: "040000", OID: vendorTree},
					gittest.TreeEntry{Path: "main.c", Mode: "100644", Content: strings.Repeat("a", 85)},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"C": 85,
			},
		},
		{
			desc: "vendor with overrides",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				vendorTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "app.rb", Mode: "100644", Content: strings.Repeat("a", 500)},
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "vendor", Mode: "040000", OID: vendorTree},
					gittest.TreeEntry{Path: "main.c", Mode: "100644", Content: strings.Repeat("a", 85)},
					gittest.TreeEntry{Path: ".gitattributes", Mode: "100644", Content: "*.rb -linguist-vendored"},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"C":    85,
				"Ruby": 500,
			},
		},
		{
			desc: "generated is ignored",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				podsTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "app.swift", Mode: "100644", Content: strings.Repeat("a", 500)},
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "Pods", Mode: "040000", OID: podsTree},
					gittest.TreeEntry{Path: "main.c", Mode: "100644", Content: strings.Repeat("a", 85)},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"C": 85,
			},
		},
		{
			desc: "generated with overrides",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				podsTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "app.swift", Mode: "100644", Content: strings.Repeat("a", 500)},
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "Pods", Mode: "040000", OID: podsTree},
					gittest.TreeEntry{Path: "main.c", Mode: "100644", Content: strings.Repeat("a", 85)},
					gittest.TreeEntry{Path: ".gitattributes", Mode: "100644", Content: "Pods/* -linguist-generated"},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"C":     85,
				"Swift": 500,
			},
		},
		{
			desc: "undetectable languages are ignored",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "config.json", Mode: "100644", Content: strings.Repeat("a", 234)},
					gittest.TreeEntry{Path: "manual.md", Mode: "100644", Content: strings.Repeat("a", 553)},
					gittest.TreeEntry{Path: "main.c", Mode: "100644", Content: strings.Repeat("a", 85)},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"C": 85,
			},
		},
		{
			desc: "undetectable languages with overrides",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "config.json", Mode: "100644", Content: strings.Repeat("a", 234)},
					gittest.TreeEntry{Path: "manual.md", Mode: "100644", Content: strings.Repeat("a", 553)},
					gittest.TreeEntry{Path: "main.c", Mode: "100644", Content: strings.Repeat("a", 85)},
					gittest.TreeEntry{
						Path: ".gitattributes",
						Mode: "100644",
						Content: "*.md linguist-detectable\n" +
							"*.json linguist-detectable\n",
					},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"C":        85,
				"JSON":     234,
				"Markdown": 553,
			},
		},
		{
			desc: "file specific documentation override",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				docTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "readme.md", Mode: "100644", Content: strings.Repeat("a", 500)},
					{Path: "index.html", Mode: "100644", Content: strings.Repeat("a", 120)},
					{Path: "formatter.rb", Mode: "100644", Content: strings.Repeat("a", 403)},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "docu", Mode: "040000", OID: docTree},
					gittest.TreeEntry{
						Path: ".gitattributes",
						Mode: "100644",
						Content: "docu/* linguist-documentation\n" +
							"docu/formatter.rb -linguist-documentation",
					},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"Ruby": 403,
			},
		},
		{
			desc: "detectable overrides",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "keeb.kicad_pcb", Mode: "100644", Content: strings.Repeat("a", 500)},
					gittest.TreeEntry{Path: "keeb.sch", Mode: "100644", Content: strings.Repeat("a", 120)},
					gittest.TreeEntry{Path: "export_bom.py", Mode: "100644", Content: strings.Repeat("a", 403)},
					gittest.TreeEntry{
						Path: ".gitattributes",
						Mode: "100644",
						Content: "*.kicad_pcb linguist-detectable\n" +
							"*.sch linguist-detectable\n" +
							"export_bom.py -linguist-detectable",
					},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"KiCad Layout": 500,
				"XML":          120,
			},
		},
		{
			desc: "double star file pattern documentation override",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				subSubTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "first.rb", Mode: "100644", Content: strings.Repeat("a", 483)},
					{Path: "second.rb", Mode: "100644", Content: strings.Repeat("a", 888)},
				})

				subTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "main.rb", Mode: "100644", Content: strings.Repeat("a", 500)},
					{Path: "formatter.rb", Mode: "100644", Content: strings.Repeat("a", 120)},
					{Path: "example", Mode: "040000", OID: subSubTree},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "scripts", Mode: "040000", OID: subTree},
					gittest.TreeEntry{Path: "run.rb", Mode: "100644", Content: strings.Repeat("a", 55)},
					gittest.TreeEntry{
						Path: ".gitattributes",
						Mode: "100644",
						Content: "scripts/** linguist-documentation\n" +
							"scripts/formatter.rb -linguist-documentation",
					},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"Ruby": 175,
			},
		},
		{
			desc: "empty code files",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				emptyBlob := gittest.WriteBlob(t, cfg, repoPath, []byte{})
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "Hello world!"},
					gittest.TreeEntry{Path: "index.html", Mode: "100644", OID: emptyBlob},
					gittest.TreeEntry{Path: "app.js", Mode: "100644", OID: emptyBlob},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{},
		},
		{
			desc: "preexisting cache",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "webpack.coffee", Mode: "100644", Content: strings.Repeat("a", 107)},
					gittest.TreeEntry{Path: "show_user.html", Mode: "100644", Content: strings.Repeat("a", 349)},
					gittest.TreeEntry{Path: "api.javascript", Mode: "100644", Content: strings.Repeat("a", 1014)},
					gittest.TreeEntry{Path: "application.rb", Mode: "100644", Content: strings.Repeat("a", 2943)},
				))
				repo := localrepo.NewTestRepo(t, cfg, repoProto)

				// We simply run the linguist once before so that it can already
				// write the cache.
				_, err := New(cfg, testhelper.NewLogger(t), catfileCache, repo).Stats(ctx, commitID)
				require.NoError(t, err)
				require.FileExists(t, filepath.Join(repoPath, languageStatsFilename))

				// Make sure it isn't able to generate stats from scratch
				require.NoError(t, os.RemoveAll(filepath.Join(repoPath, "objects", "pack")))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"CoffeeScript": 107,
				"HTML":         349,
				"JavaScript":   1014,
				"Ruby":         2943,
			},
		},
		{
			desc: "preexisting cache with .gitattributes modified",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "webpack.coffee", Mode: "100644", Content: strings.Repeat("a", 107)},
					gittest.TreeEntry{Path: "show_user.html", Mode: "100644", Content: strings.Repeat("a", 349)},
					gittest.TreeEntry{Path: "api.javascript", Mode: "100644", Content: strings.Repeat("a", 1014)},
					gittest.TreeEntry{Path: ".gitattributes", Mode: "100644", Content: "*.html linguist-vendored"},
				))
				repo := localrepo.NewTestRepo(t, cfg, repoProto)

				_, err := New(cfg, testhelper.NewLogger(t), catfileCache, repo).Stats(ctx, commitID)
				require.NoError(t, err)
				require.FileExists(t, filepath.Join(repoPath, languageStatsFilename))

				commitID = gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "webpack.coffee", Mode: "100644", Content: strings.Repeat("a", 107)},
					gittest.TreeEntry{Path: "show_user.html", Mode: "100644", Content: strings.Repeat("a", 349)},
					gittest.TreeEntry{Path: "api.javascript", Mode: "100644", Content: strings.Repeat("a", 1014)},
					gittest.TreeEntry{Path: ".gitattributes", Mode: "100644", Content: "*.coffee linguist-vendored"},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"HTML":       349,
				"JavaScript": 1014,
			},
		},
		{
			desc: "buggy behavior",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				includeTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "ffx_a.h", Mode: "100644", Content: "#include <stdio.h>\n"},
				})
				thirdPartyTree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "ffx_a.c", Mode: "100644", Content: "#include <include/ffx_a.h>\nstatic int something() {}"},
					{Path: "include", Mode: "040000", OID: includeTree},
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "thirdparty", Mode: "040000", OID: thirdPartyTree},
					gittest.TreeEntry{
						Path:    ".gitattributes",
						Mode:    "100644",
						Content: "*.h linguist-language=cpp\nthirdparty/* linguist-vendored",
					},
				))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{},
		},
		{
			desc: "corrupted cache",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "webpack.coffee", Mode: "100644", Content: strings.Repeat("a", 107)},
					gittest.TreeEntry{Path: "show_user.html", Mode: "100644", Content: strings.Repeat("a", 349)},
					gittest.TreeEntry{Path: "api.javascript", Mode: "100644", Content: strings.Repeat("a", 1014)},
					gittest.TreeEntry{Path: "application.rb", Mode: "100644", Content: strings.Repeat("a", 2943)},
				))

				require.NoError(t, os.WriteFile(filepath.Join(repoPath, languageStatsFilename), []byte("garbage"), perm.PrivateWriteOnceFile))

				return repoProto, repoPath, commitID
			},
			expectedStats: ByteCountPerLanguage{
				"CoffeeScript": 107,
				"HTML":         349,
				"JavaScript":   1014,
				"Ruby":         2943,
			},
		},
		{
			desc: "old cache",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				repo := localrepo.NewTestRepo(t, cfg, repoProto)

				oldCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "main.rb", Content: "require 'fileutils'", Mode: "100644"},
				))
				newCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(oldCommitID), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "main.go", Content: "package main", Mode: "100644"},
				))

				// Precreate the cache with the old commit. This ensures that
				// linguist knows to update the cache.
				stats, err := New(cfg, testhelper.NewLogger(t), catfileCache, repo).Stats(ctx, oldCommitID)
				require.NoError(t, err)
				require.FileExists(t, filepath.Join(repoPath, languageStatsFilename))
				require.Equal(t, ByteCountPerLanguage{
					"Ruby": 19,
				}, stats)

				return repoProto, repoPath, newCommitID
			},
			expectedStats: ByteCountPerLanguage{
				"Go": 12,
			},
		},
		{
			desc: "missing repository",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoPath := filepath.Join(testhelper.TempDir(t), "nonexistent")
				repoProto := &gitalypb.Repository{StorageName: cfg.Storages[0].Name, RelativePath: "nonexistent"}

				return repoProto, repoPath, git.ObjectID("b1bb1d1b0b1d1b00")
			},
			expectedErr: storage.ErrRepositoryNotFound.Error(),
		},
		{
			desc: "missing commit",
			setup: func(t *testing.T) (*gitalypb.Repository, string, git.ObjectID) {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				return repoProto, repoPath, git.ObjectID("b1bb1d1b0b1d1b00")
			},
			expectedErr: "linguist",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, repoPath, objectID := tc.setup(t)

			repo := localrepo.NewTestRepo(t, cfg, repoProto)
			linguist := New(cfg, testhelper.NewLogger(t), catfileCache, repo)
			stats, err := linguist.Stats(ctx, objectID)
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

func TestInstance_Stats_failureGitattributes(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)
	locator := config.NewLocator(cfg)
	logger := testhelper.NewLogger(t)

	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	gitCmdFactory := gittest.NewInterceptingCommandFactory(t, ctx, cfg, func(execEnv git.ExecutionEnvironment) string {
		return fmt.Sprintf(`#!/usr/bin/env bash
		if [[ ! "$@" =~ "check-attr" ]]; then
			exec %q "$@"
		fi
		exit 1'
		`, execEnv.BinaryPath)
	})

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "main.rb", Mode: "100644", Content: strings.Repeat("a", 107)},
		gittest.TreeEntry{Path: ".gitattributes", Mode: "100644", Content: "*.rb -linguist-vendored"},
	))

	repo := localrepo.New(logger, locator, gitCmdFactory, catfileCache, repoProto)

	linguist := New(cfg, logger, catfileCache, repo)
	_, err := linguist.Stats(ctx, commitID)

	expectedErr := `linguist object iterator: ls-tree skip: new file instance: checking attribute:`
	require.ErrorContains(t, err, expectedErr)
}

func TestColor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		language      string
		expectedColor string
	}{
		{language: "Go", expectedColor: "#00ADD8"},
		{language: "Ruby", expectedColor: "#701516"},
		{language: "HTML", expectedColor: "#e34c26"},
		{language: "Markdown", expectedColor: "#083fa1"},
		{language: "Javascript", expectedColor: "#75712c"},
		{language: "SSH Config", expectedColor: "#d1dbe0"},    // grouped into INI by go-enry
		{language: "Wozzle Wuzzle", expectedColor: "#3adbcf"}, // non-existing language
	} {
		t.Run(tc.language, func(t *testing.T) {
			require.Equal(t, tc.expectedColor, Color(tc.language), "color value for '%v'", tc.language)
		})
	}
}

func BenchmarkInstance_Stats(b *testing.B) {
	cfg := testcfg.Build(b)
	ctx := testhelper.Context(b)

	catfileCache := catfile.NewCache(cfg)
	b.Cleanup(catfileCache.Stop)

	repoProto, repoPath := gittest.CreateRepository(b, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		Seed:                   "benchmark.git",
	})
	repo := localrepo.NewTestRepo(b, cfg, repoProto)

	linguist := New(cfg, testhelper.NewLogger(b), catfileCache, repo)

	var scratchStat ByteCountPerLanguage
	var incStats ByteCountPerLanguage

	b.Run("from scratch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			require.NoError(b, os.RemoveAll(filepath.Join(repoPath, languageStatsFilename)))
			b.StartTimer()

			var err error
			scratchStat, err = linguist.Stats(ctx, "f5dfdd0057cd6bffc6259a5c8533dde5bf6a9d37")
			require.NoError(b, err)
		}
	})

	b.Run("incremental", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			require.NoError(b, os.RemoveAll(filepath.Join(repoPath, languageStatsFilename)))
			// a commit about 3 months older than the next
			_, err := linguist.Stats(ctx, "3c813b292d25a9b2ffda70e7f609f623bfc0cb37")
			require.NoError(b, err)
			b.StartTimer()

			incStats, err = linguist.Stats(ctx, "f5dfdd0057cd6bffc6259a5c8533dde5bf6a9d37")
			require.NoError(b, err)
		}
	})

	require.Equal(b, scratchStat, incStats)
}
